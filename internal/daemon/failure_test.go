package daemon_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/daemon"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

// brokenListener fails every Accept for good, as a listener whose socket broke does.
type brokenListener struct{}

func (brokenListener) Accept() (net.Conn, error) { return nil, errors.New("the listener broke") }
func (brokenListener) Close() error              { return nil }
func (brokenListener) Addr() net.Addr            { return &net.UnixAddr{Name: "broken", Net: "unix"} }

func TestAListenerThatFailsClosesTheDaemonAndFreesTheLock(t *testing.T) {
	paths := testutil.NewMachine(t).Paths
	instance, err := daemon.Start(context.Background(), paths, config.Default())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })

	daemon.Serve(instance, brokenListener{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := daemon.Wait(ctx, instance); err == nil || !strings.Contains(err.Error(), "the listener broke") {
		t.Fatalf("Wait() = %v, want the listener's failure", err)
	}
	next, err := daemon.Start(context.Background(), paths, config.Default())
	if err != nil {
		t.Fatalf("Start() after the failure = %v, want the lock free", err)
	}
	_ = next.Close()
}

func TestTwoSpawnsAtOnceOnAStoppedHomeBothSucceed(t *testing.T) {
	paths := testutil.NewMachine(t).Paths
	t.Setenv("DESK_TEST_DAEMON_CHILD", "1")
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(paths.ConfigDir))
	t.Setenv("XDG_STATE_HOME", filepath.Dir(paths.StateDir))
	t.Setenv("XDG_DATA_HOME", filepath.Dir(paths.DataDir))
	t.Setenv("XDG_CACHE_HOME", filepath.Dir(paths.CacheDir))
	t.Cleanup(func() { _ = daemon.Stop(paths, 5*time.Second) })

	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- daemon.Spawn(paths) }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("Spawn() error = %v, want both spawns to find the daemon that won", err)
		}
	}
}

// longPaths is a machine whose socket path is over the limit.
func longPaths(t *testing.T) config.Paths {
	t.Helper()
	p := testutil.NewMachine(t).Paths
	p.StateDir = filepath.Join(p.StateDir, strings.Repeat("s", 100))
	if len(p.Socket()) <= 103 {
		t.Fatalf("test setup: socket path is %d bytes, want over 103", len(p.Socket()))
	}
	return p
}

func TestSpawnAndStartNameAnOverlongSocketPathWithItsLengthAndTheLimit(t *testing.T) {
	p := longPaths(t)
	want := []string{p.Socket(), strconv.Itoa(len(p.Socket())) + " bytes", "103"}

	if err := daemon.Spawn(p); err == nil {
		t.Error("Spawn() error = nil, want the socket path refused")
	} else {
		for _, w := range want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("Spawn() error = %q, want it to hold %q", err, w)
			}
		}
	}
	if _, err := os.Stat(p.DaemonLog()); err == nil {
		t.Error("Spawn() opened the daemon log, so it started a child for a path that cannot work")
	}
	if _, err := daemon.Start(context.Background(), p, config.Default()); err == nil || !strings.Contains(err.Error(), want[1]) {
		t.Errorf("Start() error = %v, want the same reason", err)
	}
}
