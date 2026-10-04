package setup_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/federbenjamin/desk/internal/setup"
)

func TestRunCreatesTheStoreFolderPrivateAndOnlyTheScratchRootLooser(t *testing.T) {
	p, getenv := setupPaths(t)
	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, NoHerdr: true}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for dir, want := range map[string]os.FileMode{p.DataDir: 0o700, p.ScratchRoot(): 0o755} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %04o, want %04o", dir, got, want)
		}
	}
}

func TestRunReportsAHerdrConfigItCannotStatInsteadOfCallingItAbsent(t *testing.T) {
	p, getenv := setupPaths(t)
	herdr := filepath.Join(getenv("XDG_CONFIG_HOME"), "herdr")
	if err := os.MkdirAll(filepath.Dir(herdr), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(herdr, []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv})
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("Run() with herdr's config under a file = %v, want the stat error", err)
	}
	if _, err := os.Stat(p.ConfigFile()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Run() wrote the config before failing: %v", err)
	}
}
