package config_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestLockHeldLeavesAMissingLockPathUntouched(t *testing.T) {
	m := testutil.NewMachine(t)
	path := m.Paths.TickerLock()

	held, err := config.LockHeld(path)
	if err != nil {
		t.Fatalf("LockHeld(missing) error = %v", err)
	}
	if held {
		t.Fatal("LockHeld(missing) = true; want false")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("LockHeld(missing) created %q: stat error = %v; want not exist", path, err)
	}
}

func TestTryLockKeepsOthersOutUntilItsUnlock(t *testing.T) {
	m := testutil.NewMachine(t)
	path := m.Paths.TickerLock()

	unlock, err := config.TryLock(path)
	if err != nil {
		t.Fatalf("first TryLock() error = %v", err)
	}
	defer func() {
		if unlock != nil {
			_ = unlock()
		}
	}()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("lock file permissions = %04o; want 0600", got)
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat lock directory: %v", err)
	}
	if got := parent.Mode().Perm(); got != 0o700 {
		t.Errorf("lock directory permissions = %04o; want 0700", got)
	}

	if _, err := config.TryLock(path); !errors.Is(err, config.ErrLocked) {
		t.Fatalf("second TryLock() error = %v; want ErrLocked", err)
	}
	held, err := config.LockHeld(path)
	if err != nil {
		t.Fatalf("LockHeld(held) error = %v", err)
	}
	if !held {
		t.Fatal("LockHeld(held) = false; want true")
	}

	if err := unlock(); err != nil {
		t.Fatalf("unlock() error = %v", err)
	}
	unlock = nil
	held, err = config.LockHeld(path)
	if err != nil {
		t.Fatalf("LockHeld(released) error = %v", err)
	}
	if held {
		t.Fatal("LockHeld(released) = true; want false")
	}

	again, err := config.TryLock(path)
	if err != nil {
		t.Fatalf("TryLock(after release) error = %v", err)
	}
	if err := again(); err != nil {
		t.Fatalf("second unlock() error = %v", err)
	}
}

func TestLockWaitsForTheHolderAndThenAcquires(t *testing.T) {
	m := testutil.NewMachine(t)
	path := m.Paths.BackupLock()
	holder, err := config.TryLock(path)
	if err != nil {
		t.Fatalf("TryLock() error = %v", err)
	}

	entered := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(entered)
		unlock, err := config.Lock(path)
		if err == nil {
			err = unlock()
		}
		result <- err
	}()
	<-entered

	if err := holder(); err != nil {
		t.Fatalf("holder unlock() error = %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("Lock() after release error = %v", err)
	}
}
