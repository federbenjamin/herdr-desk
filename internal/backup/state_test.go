package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/backup"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestRunRecordsAFailureKeepsTheLastSuccessAndASuccessClearsTheError(t *testing.T) {
	ctx := context.Background()
	paths := testutil.NewMachine(t).Paths
	st, err := store.Open(paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	missing := filepath.Join(paths.DataDir, "desk-user:s3cret-token@nowhere", "repo.git")
	good := filepath.Join(paths.DataDir, "remote.git")
	git(t, "init", "--bare", good)

	if ts, msg := backup.Last(paths); ts != nil || msg != "" {
		t.Fatalf("Last() before any run = (%v, %q), want no record", ts, msg)
	}

	if _, err := backup.Run(ctx, st, paths, missing); err == nil {
		t.Fatal("Run() to a missing remote succeeded")
	}
	ts, msg := backup.Last(paths)
	if ts != nil || !strings.Contains(msg, "git push") {
		t.Fatalf("Last() after a first failed run = (%v, %q), want no success and the push's error", ts, msg)
	}
	if !backup.Due(paths, time.Now()) {
		t.Fatal("Due() = false after a failed run and no success")
	}
	state, err := os.ReadFile(paths.BackupState())
	if err != nil || strings.Contains(string(state), "s3cret-token") {
		t.Fatalf("backup state = %q (%v), want it written without the remote", state, err)
	}

	if _, err := backup.Run(ctx, st, paths, good); err != nil {
		t.Fatalf("Run() to a good remote: %v", err)
	}
	success, msg := backup.Last(paths)
	if success == nil || msg != "" || time.Since(*success) > time.Minute {
		t.Fatalf("Last() after a success = (%v, %q), want its time and no error", success, msg)
	}

	if _, err := backup.Run(ctx, st, paths, missing); err == nil {
		t.Fatal("Run() to a missing remote succeeded")
	}
	ts, msg = backup.Last(paths)
	if ts == nil || !ts.Equal(*success) || !strings.Contains(msg, "git push") {
		t.Fatalf("Last() after a later failure = (%v, %q), want the success at %v kept and the error", ts, msg, success)
	}
	if backup.Due(paths, success.Add(23*time.Hour)) || !backup.Due(paths, success.Add(25*time.Hour)) {
		t.Fatal("Due() after a later failure does not follow the last success")
	}

	if err := os.WriteFile(paths.BackupState(), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ts, msg := backup.Last(paths); ts != nil || msg != "" {
		t.Fatalf("Last() of a malformed state = (%v, %q), want no record", ts, msg)
	}
}
