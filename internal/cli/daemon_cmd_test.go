package cli_test

import (
	"fmt"
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
	if strings.Contains(result.stderr, "home-unreachable") || !strings.Contains(result.stderr, "did not answer") {
		t.Fatalf("daemon status on a stopped home: stderr = %q, want the reason with no code (home-unreachable means exit 3)", result.stderr)
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

func TestDaemonRunAlreadyRunningPrintsOnlyThePidOfTheDaemonOnTheSocket(t *testing.T) {
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
	if err := os.WriteFile(machine.Paths.DaemonInfo(), []byte(`{"pid":4242}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result = runDeskWithEnv(t, machine, t.TempDir(), []string{"daemon", "run"}, "", nil, nil)
	if result.exit != 3 || strings.Contains(result.stdout, "4242") {
		t.Fatalf("daemon run with an info file and no daemon on the socket = (%d, %q, %q), want exit 3", result.exit, result.stdout, result.stderr)
	}

	home := testutil.StartHome(t, testutil.HomeOptions{})
	current, err := os.ReadFile(home.Paths.DaemonInfo())
	if err != nil {
		t.Fatal(err)
	}
	stale := []byte(`{"pid":4242,"started_ts":"2000-01-01T00:00:00Z"}`)
	if err := os.WriteFile(home.Paths.DaemonInfo(), stale, 0o600); err != nil {
		t.Fatal(err)
	}
	result = runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"daemon", "run"}, "", nil, nil)
	if result.exit != 3 || strings.Contains(result.stdout, "4242") || !strings.Contains(result.stderr, "info file") {
		t.Fatalf("daemon run with a stale info file = (%d, %q, %q), want exit 3 and never the stale pid", result.exit, result.stdout, result.stderr)
	}

	t.Cleanup(cli.SetInfoWait(5 * time.Second))
	late := time.AfterFunc(50*time.Millisecond, func() {
		_ = os.WriteFile(home.Paths.DaemonInfo(), current, 0o600)
	})
	defer late.Stop()
	result = runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"daemon", "run"}, "", nil, nil)
	if want := fmt.Sprintf("desk daemon: already running (pid %d)", os.Getpid()); result.exit != 0 || strings.TrimSpace(result.stdout) != want {
		t.Fatalf("daemon run once the running daemon's info file is back = (%d, %q, %q), want %q", result.exit, result.stdout, result.stderr, want)
	}
}
