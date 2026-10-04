package cli_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/testutil"
)

const restartHint = "desk daemon restart"

func daemonStatus(t *testing.T, home *testutil.Home) api.Status {
	t.Helper()
	result := runHomeDesk(t, home, "daemon", "status")
	requireSuccess(t, result)
	var st api.Status
	if err := json.Unmarshal([]byte(result.stdout), &st); err != nil {
		t.Fatalf("daemon status stdout = %q: %v", result.stdout, err)
	}
	return st
}

func TestACommandNamesTheRestartWhenTheConfigChangedAfterTheDaemonStarted(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	if result := runHomeDesk(t, home, "list"); strings.Contains(result.stderr, restartHint) {
		t.Fatalf("list before any config change: stderr = %q, want no restart hint", result.stderr)
	}
	if daemonStatus(t, home).ConfigChanged {
		t.Fatal("daemon status before any config change: config_changed = true")
	}

	cfg := config.Default()
	cfg.Backup.GitRemote = "somewhere:desk-backup.git"
	if err := cfg.Save(home.Paths.ConfigFile()); err != nil {
		t.Fatal(err)
	}

	result := runHomeDesk(t, home, "backup")
	if result.exit != 1 || strings.Count(result.stderr, restartHint) != 1 {
		t.Fatalf("backup after a config edit = (%d, %q), want the refusal and one line naming %q", result.exit, result.stderr, restartHint)
	}
	if !daemonStatus(t, home).ConfigChanged {
		t.Error("daemon status after a config edit: config_changed = false")
	}

	home.Restart(t)
	if result := runHomeDesk(t, home, "list"); strings.Contains(result.stderr, restartHint) {
		t.Errorf("list after the restart: stderr = %q, want no restart hint", result.stderr)
	}
	if daemonStatus(t, home).ConfigChanged {
		t.Error("daemon status after the restart: config_changed = true")
	}
}

func TestCommandsThatWriteTheConfigNameTheRestartOnlyWhileADaemonRuns(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	dir := t.TempDir()

	result := runDeskWithEnv(t, home.Machine, dir, []string{"roots", "add", dir}, "", nil, nil)
	requireSuccess(t, result)
	if !strings.Contains(result.stderr, restartHint) {
		t.Errorf("roots add beside a running daemon: stderr = %q, want a line naming %q", result.stderr, restartHint)
	}

	result = runDeskWithEnv(t, home.Machine, dir, []string{"setup", "--no-herdr"}, "", nil, nil)
	requireSuccess(t, result)
	if !strings.Contains(result.stderr, restartHint) {
		t.Errorf("setup beside a running daemon: stderr = %q, want a line naming %q", result.stderr, restartHint)
	}

	home.Stop()
	result = runDeskWithEnv(t, home.Machine, dir, []string{"roots", "remove", dir}, "", nil, nil)
	requireSuccess(t, result)
	if strings.Contains(result.stderr, restartHint) {
		t.Errorf("roots remove with no daemon: stderr = %q, want no restart hint", result.stderr)
	}
}

func TestAClientMachineNeverHearsOfARestart(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.NewClientMachine(t, home)
	cfg, err := config.Load(client.Paths.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(client.Paths.ConfigFile()); err != nil {
		t.Fatal(err)
	}
	result := runDesk(t, client.Getenv(nil), t.TempDir(), "", "list")
	requireSuccess(t, result)
	if strings.Contains(result.stderr, restartHint) {
		t.Errorf("client list: stderr = %q, want no restart hint", result.stderr)
	}
	if _, err := os.Stat(client.Paths.DaemonInfo()); err == nil {
		t.Fatal("a client has a daemon info file")
	}
}
