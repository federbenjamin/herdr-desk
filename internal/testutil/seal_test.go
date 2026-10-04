package testutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/federbenjamin/desk/internal/herdr"
	_ "github.com/federbenjamin/desk/internal/testutil"
)

func TestLinkingTestutilSealsHerdrAgainstAHerdrOnPath(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got, err := herdr.Find(); err == nil {
		t.Fatalf("Find() = %q, nil; want an error while testutil is linked", got)
	}
}
