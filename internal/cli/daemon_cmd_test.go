package cli_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/federbenjamin/desk/internal/cli"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/testutil"
)

func TestDaemonStatusOnAStoppedHomeExitsOneAndStartsNothing(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	home.Stop()
	spawned := 0
	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"daemon", "status"}, "", nil, func(config.Paths) error {
		spawned++
		home.Restart(t)
		return nil
	})
	if spawned != 0 {
		t.Fatalf("daemon status called Spawn %d times, want 0", spawned)
	}
	if result.exit != 1 || result.stdout != "" {
		t.Fatalf("daemon status on a stopped home = (%d, %q, %q), want exit 1 and no status", result.exit, result.stdout, result.stderr)
	}
}

// holdLock takes the daemon lock as a daemon does at start, before it writes its info file.
func holdLock(t *testing.T, p config.Paths) {
	t.Helper()
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(p.LockFile(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("take the lock: %v", err)
	}
}

func TestDaemonRunAlreadyRunningWaitsForTheInfoFileAndNeverPrintsPidZero(t *testing.T) {
	t.Cleanup(cli.SetInfoWait(100 * time.Millisecond))
	machine := testutil.NewMachine(t)
	holdLock(t, machine.Paths)

	result := runDeskWithEnv(t, machine, t.TempDir(), []string{"daemon", "run"}, "", nil, nil)
	if result.exit != 3 || strings.Contains(result.stdout, "pid 0") {
		t.Fatalf("daemon run with no info file = (%d, %q, %q), want exit 3 and no pid 0", result.exit, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stderr, "info file") {
		t.Fatalf("daemon run stderr = %q, want it to name the unreadable info file", result.stderr)
	}

	t.Cleanup(cli.SetInfoWait(5 * time.Second))
	late := time.AfterFunc(50*time.Millisecond, func() {
		_ = os.WriteFile(machine.Paths.DaemonInfo(), []byte(`{"pid":4242}`), 0o600)
	})
	defer late.Stop()
	result = runDeskWithEnv(t, machine, t.TempDir(), []string{"daemon", "run"}, "", nil, nil)
	if result.exit != 0 || strings.TrimSpace(result.stdout) != "desk daemon: already running (pid 4242)" {
		t.Fatalf("daemon run once the info file appears = (%d, %q, %q), want pid 4242", result.exit, result.stdout, result.stderr)
	}
}
