package backup_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/backup"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestRunErrorNeverCarriesTheRemote(t *testing.T) {
	paths := testutil.NewMachine(t).Paths
	st, err := store.Open(paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// git names a path remote it cannot read in its error, as it names some URLs.
	remote := filepath.Join(paths.DataDir, "desk-user:s3cret-token@nowhere", "repo.git")
	_, err = backup.Run(context.Background(), st, paths, remote)
	if err == nil {
		t.Fatal("Run() to a missing remote succeeded")
	}
	if strings.Contains(err.Error(), "s3cret-token") || !strings.Contains(err.Error(), "git push") {
		t.Fatalf("Run() error = %q, want the push named and the remote left out", err)
	}
}
