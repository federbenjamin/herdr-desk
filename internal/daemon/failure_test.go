package daemon_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/daemon"
	"github.com/federbenjamin/desk/internal/testutil"
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
