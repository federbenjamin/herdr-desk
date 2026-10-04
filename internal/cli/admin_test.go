package cli_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/testutil"
	"github.com/federbenjamin/desk/internal/version"
)

func TestDaemonStatusReportsTheRunningHomeAndNeverSpawns(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	spawned := 0
	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"daemon", "status"}, "", nil, func(config.Paths) error {
		spawned++
		return errors.New("daemon status must not spawn")
	})
	if result.exit != 0 {
		t.Fatalf("daemon status exit = %d, stderr = %q", result.exit, result.stderr)
	}
	if spawned != 0 {
		t.Fatalf("daemon status called Spawn %d times", spawned)
	}
	var status struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &status); err != nil {
		t.Fatalf("daemon status stdout = %q, not JSON: %v", result.stdout, err)
	}
	if status.Version != version.Version {
		t.Fatalf("daemon status version = %q, want %q", status.Version, version.Version)
	}
}

func TestDaemonRunTreatsAnExistingDaemonAndAClientAsSuccessfulNoops(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	running := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"daemon", "run"}, "", nil, nil)
	if running.exit != 0 || !strings.Contains(running.stdout, "desk daemon: already running") {
		t.Fatalf("daemon run while running = (%d, %q, %q)", running.exit, running.stdout, running.stderr)
	}

	client := testutil.NewClientMachine(t, home)
	remote := runDeskWithEnv(t, client, t.TempDir(), []string{"daemon", "run"}, "", nil, nil)
	want := "desk daemon: this machine is a client of " + home.Addr + "; nothing to run"
	if remote.exit != 0 || strings.TrimSpace(remote.stdout) != want {
		t.Fatalf("daemon run on client = (%d, %q, %q), want %q", remote.exit, remote.stdout, remote.stderr, want)
	}
}

func TestAdminCommandStartsAnUnavailableHomeThroughEnvSpawn(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	home.Stop()
	spawned := 0
	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"backup"}, "", nil, func(config.Paths) error {
		spawned++
		home.Restart(t)
		return nil
	})
	if spawned != 1 {
		t.Fatalf("backup Spawn calls = %d, want 1", spawned)
	}
	if result.exit != 1 || !strings.Contains(result.stderr, "backup-off") {
		t.Fatalf("backup after spawned home = (%d, %q, %q), want backup-off refusal", result.exit, result.stdout, result.stderr)
	}
}

func TestTokenShowAndRotateExposeTheCurrentToken(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	rotate := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"token", "rotate"}, "", nil, nil)
	after := strings.TrimSpace(rotate.stdout)
	if rotate.exit != 0 || len(after) != 64 {
		t.Fatalf("token rotate = (%d, %q, %q), want a 64-character token", rotate.exit, rotate.stdout, rotate.stderr)
	}
	stored, err := config.ReadToken(home.Paths)
	if err != nil || stored != after {
		t.Fatalf("rotated token stored = %q, %v; want %q", stored, err, after)
	}
	show := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"token", "show"}, "", nil, nil)
	if show.exit != 0 || strings.TrimSpace(show.stdout) != after {
		t.Fatalf("token show = (%d, %q, %q), want current token", show.exit, show.stdout, show.stderr)
	}
}

func TestClientAddReadsOnlyStdinOrTheNamedTokenFile(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.NewMachine(t)

	argument := runDeskWithEnv(t, client, t.TempDir(), []string{"client", "add", home.Addr, home.Token}, "", nil, nil)
	if argument.exit != 2 {
		t.Fatalf("client add with token argument exit = %d, want 2; stderr = %q", argument.exit, argument.stderr)
	}
	if _, err := os.Stat(client.Paths.ConfigFile()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("client add with token argument wrote config: %v", err)
	}

	added := runDeskWithEnv(t, client, t.TempDir(), []string{"client", "add", home.Addr}, home.Token+"\n", nil, nil)
	if added.exit != 0 || strings.TrimSpace(added.stdout) != "client of "+home.Addr {
		t.Fatalf("client add from stdin = (%d, %q, %q)", added.exit, added.stdout, added.stderr)
	}
	cfg, err := config.Load(client.Paths.ConfigFile())
	if err != nil || cfg.Client.Home != home.Addr {
		t.Fatalf("client config = %#v, %v; want home %q", cfg, err, home.Addr)
	}
	token, err := config.ReadToken(client.Paths)
	if err != nil || token != home.Token {
		t.Fatalf("client token = %q, %v; want home token", token, err)
	}
}

func TestRootsAddResolvesRelativePathsAndListAndRemovePersistTheEdit(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, "project"), 0o700); err != nil {
		t.Fatalf("make root: %v", err)
	}
	add := runDeskWithEnv(t, home.Machine, cwd, []string{"roots", "add", "project", "--about", "CLI tests", "--isolation", "worktree"}, "", nil, nil)
	if add.exit != 0 {
		t.Fatalf("roots add exit = %d, stderr = %q", add.exit, add.stderr)
	}
	wantPath := filepath.Join(cwd, "project")
	cfg, err := config.Load(home.Paths.ConfigFile())
	if err != nil || len(cfg.Roots) != 1 || cfg.Roots[0].Path != wantPath || cfg.Roots[0].Isolation != "worktree" {
		t.Fatalf("roots config = %#v, %v; want absolute root %q", cfg.Roots, err, wantPath)
	}
	listed := runDeskWithEnv(t, home.Machine, cwd, []string{"roots", "--json"}, "", nil, nil)
	var roots []config.Root
	if listed.exit != 0 || json.Unmarshal([]byte(listed.stdout), &roots) != nil || len(roots) != 1 || roots[0].Path != wantPath {
		t.Fatalf("roots --json = (%d, %q, %q), want %q", listed.exit, listed.stdout, listed.stderr, wantPath)
	}
	removed := runDeskWithEnv(t, home.Machine, cwd, []string{"roots", "remove", wantPath}, "", nil, nil)
	if removed.exit != 0 {
		t.Fatalf("roots remove exit = %d, stderr = %q", removed.exit, removed.stderr)
	}
	cfg, err = config.Load(home.Paths.ConfigFile())
	if err != nil || len(cfg.Roots) != 0 {
		t.Fatalf("roots after remove = %#v, %v; want none", cfg.Roots, err)
	}
}

func TestSetupRejectsWildcardListenWithoutWritingConfigAndVersionNeedsNoHome(t *testing.T) {
	machine := testutil.NewMachine(t)
	setup := runDeskWithEnv(t, machine, t.TempDir(), []string{"setup", "--listen", "0.0.0.0:7411", "--no-herdr"}, "", nil, nil)
	if setup.exit != 2 {
		t.Fatalf("setup wildcard listen exit = %d, want 2; stderr = %q", setup.exit, setup.stderr)
	}
	if _, err := os.Stat(machine.Paths.ConfigFile()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("setup wildcard listen wrote config: %v", err)
	}

	versionResult := runDeskWithEnv(t, machine, t.TempDir(), []string{"version"}, "", nil, func(config.Paths) error {
		return errors.New("version must not spawn")
	})
	want := "desk " + version.Version
	if versionResult.exit != 0 || strings.TrimSpace(versionResult.stdout) != want {
		t.Fatalf("version = (%d, %q, %q), want %q", versionResult.exit, versionResult.stdout, versionResult.stderr, want)
	}
}
