package cli_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
	"github.com/federbenjamin/herdr-desk/internal/version"
)

// TestMain lets this test binary answer as a home's rpc helper, for client machines.
func TestMain(m *testing.M) {
	testutil.ServeRPCIfAsked()
	os.Exit(m.Run())
}

func TestTickerStatusReportsTheHomeWithNoTickerRunning(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"ticker", "status"}, "", nil)
	if result.exit != 0 {
		t.Fatalf("ticker status exit = %d, stderr = %q", result.exit, result.stderr)
	}
	var status struct {
		Version string `json:"version"`
		Ticker  struct {
			Running bool `json:"running"`
		} `json:"ticker"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &status); err != nil {
		t.Fatalf("ticker status stdout = %q, not JSON: %v", result.stdout, err)
	}
	if status.Version != version.Version || status.Ticker.Running {
		t.Fatalf("ticker status = %+v, want version %q and no ticker", status, version.Version)
	}
}

func TestTickerStatusOnAClientWithTheHomeDownExitsOne(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := testutil.NewClientMachine(t, home)
	home.Stop()

	result := runDeskWithEnv(t, client, t.TempDir(), []string{"ticker", "status"}, "", nil)
	if result.exit != 1 || !strings.Contains(result.stderr, "did not answer") || strings.Contains(result.stderr, "home-unreachable") {
		t.Fatalf("ticker status with the home down = (%d, %q, %q), want exit 1 naming why, without the code", result.exit, result.stdout, result.stderr)
	}
}

func TestTickerStatusPrintsTheLastBackupOutcome(t *testing.T) {
	cfg := config.Default()
	cfg.Backup.GitRemote = filepath.Join(t.TempDir(), "desk-user:s3cret-token@nowhere", "repo.git")
	home := testutil.StartHome(t, testutil.HomeOptions{Config: cfg})
	status := func() map[string]json.RawMessage {
		t.Helper()
		result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"ticker", "status"}, "", nil)
		var keys map[string]json.RawMessage
		if result.exit != 0 || json.Unmarshal([]byte(result.stdout), &keys) != nil {
			t.Fatalf("ticker status = (%d, %q, %q), want its JSON", result.exit, result.stdout, result.stderr)
		}
		return keys
	}
	if got := status(); string(got["backup_ts"]) != "null" || string(got["backup_error"]) != `""` {
		t.Fatalf("ticker status before any backup = backup_ts %s, backup_error %s; want null and empty", got["backup_ts"], got["backup_error"])
	}

	if run := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"backup"}, "", nil); run.exit != 3 {
		t.Fatalf("backup to a missing remote = (%d, %q, %q), want exit 3", run.exit, run.stdout, run.stderr)
	}
	got := status()
	var msg string
	if err := json.Unmarshal(got["backup_error"], &msg); err != nil || string(got["backup_ts"]) != "null" || !strings.Contains(msg, "git push") {
		t.Fatalf("ticker status after a failed backup = backup_ts %s, backup_error %s; want null and the push's error", got["backup_ts"], got["backup_error"])
	}
	if strings.Contains(msg, "s3cret-token") {
		t.Fatalf("ticker status backup_error = %q, want the remote left out", msg)
	}
}

func TestTickerOnAClientIsANoopThatNamesTheHome(t *testing.T) {
	client := testutil.NewClientMachine(t, testutil.StartHome(t, testutil.HomeOptions{}))
	remote := runDeskWithEnv(t, client, t.TempDir(), []string{"ticker"}, "", nil)
	want := "herdr-desk ticker: this machine is a client of home; the ticker runs on the home"
	if remote.exit != 0 || strings.TrimSpace(remote.stdout) != want {
		t.Fatalf("ticker on a client = (%d, %q, %q), want %q", remote.exit, remote.stdout, remote.stderr, want)
	}
}

func TestBackupThatFailsToPushTellsTheCallerWhichGitStepFailed(t *testing.T) {
	cfg := config.Default()
	remote := filepath.Join(t.TempDir(), "missing.git")
	cfg.Backup.GitRemote = remote
	home := testutil.StartHome(t, testutil.HomeOptions{Config: cfg})

	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"backup"}, "", nil)
	if result.exit != 3 || result.stdout != "" {
		t.Fatalf("backup to a missing remote = (%d, %q, %q), want exit 3", result.exit, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stderr, "git push") || strings.Contains(result.stderr, "internal error") {
		t.Fatalf("backup stderr = %q, want the git step that failed", result.stderr)
	}
	if strings.Contains(result.stderr, remote) {
		t.Fatalf("backup stderr = %q names the remote", result.stderr)
	}
}
func TestRootsAddResolvesRelativePathsAndListAndRemovePersistTheEdit(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, "project"), 0o700); err != nil {
		t.Fatalf("make root: %v", err)
	}
	add := runDeskWithEnv(t, home.Machine, cwd, []string{"roots", "add", "project", "--about", "CLI tests", "--isolation", "worktree"}, "", nil)
	if add.exit != 0 {
		t.Fatalf("roots add exit = %d, stderr = %q", add.exit, add.stderr)
	}
	wantPath := filepath.Join(cwd, "project")
	cfg, err := config.Load(home.Paths.ConfigFile())
	if err != nil || len(cfg.Roots) != 1 || cfg.Roots[0].Path != wantPath || cfg.Roots[0].Isolation != "worktree" {
		t.Fatalf("roots config = %#v, %v; want absolute root %q", cfg.Roots, err, wantPath)
	}
	listed := runDeskWithEnv(t, home.Machine, cwd, []string{"roots", "--json"}, "", nil)
	var roots []config.Root
	if listed.exit != 0 || json.Unmarshal([]byte(listed.stdout), &roots) != nil || len(roots) != 1 || roots[0].Path != wantPath {
		t.Fatalf("roots --json = (%d, %q, %q), want %q", listed.exit, listed.stdout, listed.stderr, wantPath)
	}
	removed := runDeskWithEnv(t, home.Machine, cwd, []string{"roots", "remove", wantPath}, "", nil)
	if removed.exit != 0 {
		t.Fatalf("roots remove exit = %d, stderr = %q", removed.exit, removed.stderr)
	}
	cfg, err = config.Load(home.Paths.ConfigFile())
	if err != nil || len(cfg.Roots) != 0 {
		t.Fatalf("roots after remove = %#v, %v; want none", cfg.Roots, err)
	}
}

func TestClientAddSavesTheHomeOnlyWhenItAnswers(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := testutil.NewClientMachine(t, home)
	before, err := os.ReadFile(client.Paths.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}

	home.Stop()
	down := runDeskWithEnv(t, client, t.TempDir(), []string{"client", "add", "other-home"}, "", nil)
	if down.exit != 3 || !strings.Contains(down.stderr, "home-unreachable") {
		t.Fatalf("client add with the home down = (%d, %q, %q), want exit 3 home-unreachable", down.exit, down.stdout, down.stderr)
	}
	if after, err := os.ReadFile(client.Paths.ConfigFile()); err != nil || string(after) != string(before) {
		t.Fatalf("client add with the home down changed the config to %q, %v", after, err)
	}

	home.Restart(t)
	added := runDeskWithEnv(t, client, t.TempDir(), []string{"client", "add", "other-home"}, "", nil)
	if added.exit != 0 || strings.TrimSpace(added.stdout) != "client of other-home" {
		t.Fatalf("client add = (%d, %q, %q)", added.exit, added.stdout, added.stderr)
	}
	cfg, err := config.Load(client.Paths.ConfigFile())
	if err != nil || cfg.Client.Home != "other-home" {
		t.Fatalf("client config = %#v, %v; want home other-home", cfg, err)
	}
}

func TestSetupRejectsAnUnknownProfileWithoutWritingConfigAndVersionNeedsNoHome(t *testing.T) {
	machine := testutil.NewMachine(t)
	setup := runDeskWithEnv(t, machine, t.TempDir(), []string{"setup", "--profile", "no-such-profile", "--no-herdr"}, "", nil)
	if setup.exit != 2 {
		t.Fatalf("setup with an unknown profile exit = %d, want 2; stderr = %q", setup.exit, setup.stderr)
	}
	if _, err := os.Stat(machine.Paths.ConfigFile()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("setup with an unknown profile wrote config: %v", err)
	}

	versionResult := runDeskWithEnv(t, machine, t.TempDir(), []string{"version"}, "", nil)
	want := "herdr-desk " + version.Version
	if versionResult.exit != 0 || strings.TrimSpace(versionResult.stdout) != want {
		t.Fatalf("version = (%d, %q, %q), want %q", versionResult.exit, versionResult.stdout, versionResult.stderr, want)
	}
}

func TestRemovedCommandsAndAConfigWithARemovedKeyAreUsageErrors(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	for _, args := range [][]string{{"daemon"}, {"token"}, {"setup", "--listen", "127.0.0.1:7411"}} {
		if r := runDeskWithEnv(t, home.Machine, t.TempDir(), args, "", nil); r.exit != 2 {
			t.Errorf("%v = (%d, %q, %q), want exit 2", args, r.exit, r.stdout, r.stderr)
		}
	}
	if err := os.WriteFile(home.Paths.ConfigFile(), []byte("[home]\nlisten = \"127.0.0.1:7411\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"list"}, "", nil)
	if r.exit != 2 || !strings.Contains(r.stderr, "unknown key home") {
		t.Fatalf("list with [home] listen in the config = (%d, %q, %q), want exit 2 naming the key", r.exit, r.stdout, r.stderr)
	}
}
