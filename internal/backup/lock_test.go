package backup_test

import (
	"context"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/backup"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestRunWaitsForAnExistingBackupBeforeUsingTheBackupDirectory(t *testing.T) {
	machine := testutil.NewMachine(t)
	st, err := store.Open(machine.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	unlock, err := config.Lock(machine.Paths.BackupLock())
	if err != nil {
		t.Fatalf("take backup lock: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := backup.Run(context.Background(), st, machine.Paths, "")
		done <- err
	}()

	deadline := time.NewTimer(75 * time.Millisecond)
	defer deadline.Stop()
	select {
	case err := <-done:
		t.Fatalf("Run() returned before the earlier backup released its lock: %v", err)
	case <-deadline.C:
	}
	if err := unlock(); err != nil {
		t.Fatalf("release backup lock: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run() with an empty remote succeeded, want its push to fail after the lock is released")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not finish after the earlier backup released its lock")
	}
}
