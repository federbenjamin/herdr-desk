package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

func TestOpenMakesAnExistingLooseStorePrivate(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "desk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "desk.db")
	first, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	for f, mode := range map[string]os.FileMode{dir: 0o755, path: 0o644} {
		if err := os.Chmod(f, mode); err != nil {
			t.Fatal(err)
		}
	}

	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for f, want := range map[string]os.FileMode{dir: 0o700, path: 0o600} {
		info, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %04o, want %04o", f, got, want)
		}
	}
}

func TestMergedRefusesASecretInItsBranchAndWritesNothing(t *testing.T) {
	t.Parallel()

	st := openStore(t, store.Options{})
	before := eventCount(t, st)
	_, err := st.Merged(context.Background(), store.Actor{}, model.MergedData{Branch: "feature/AKIAABCDEFGHIJKLMNOP"})
	if code := refusalCode(t, err); code != model.CodeSecretDetected {
		t.Fatalf("Merged() with a secret branch = %v, want secret-detected", err)
	}
	if after := eventCount(t, st); after != before {
		t.Fatalf("events = %d after the refusal, want %d", after, before)
	}
}
