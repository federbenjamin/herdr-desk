package herdr_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/herdr"
)

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFindUsesDeskHerdrAsIsAndNeverSearchesPathWhenItIsSet(t *testing.T) {
	good := filepath.Join(t.TempDir(), "my-herdr")
	writeExecutable(t, good, "exit 0")
	onPath := t.TempDir()
	writeExecutable(t, filepath.Join(onPath, "herdr"), "exit 0")
	t.Setenv("PATH", onPath)

	t.Run("set and good", func(t *testing.T) {
		t.Setenv("DESK_HERDR", good)
		got, err := herdr.Find()
		if err != nil || got != good {
			t.Fatalf("Find() = %q, %v, want %q", got, err, good)
		}
	})
	plain := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"set and missing":    filepath.Join(t.TempDir(), "no-such-herdr"),
		"set and relative":   "herdr",
		"set to a folder":    t.TempDir(),
		"set and not a tool": plain,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DESK_HERDR", value)
			got, err := herdr.Find()
			if err == nil {
				t.Fatalf("Find() = %q, nil; want an error (PATH holds a herdr, which must not be used)", got)
			}
			if !strings.Contains(err.Error(), "DESK_HERDR") {
				t.Fatalf("Find() error = %q, want it to name DESK_HERDR", err)
			}
		})
	}
}

func TestFindSearchesPathWhenDeskHerdrIsUnsetOrEmpty(t *testing.T) {
	onPath := t.TempDir()
	want := filepath.Join(onPath, "herdr")
	writeExecutable(t, want, "exit 0")
	t.Setenv("PATH", onPath)

	t.Run("empty", func(t *testing.T) {
		t.Setenv("DESK_HERDR", "")
		got, err := herdr.Find()
		if err != nil || got != want {
			t.Fatalf("Find() = %q, %v, want %q", got, err, want)
		}
	})
	t.Run("unset", func(t *testing.T) {
		t.Setenv("DESK_HERDR", "")
		os.Unsetenv("DESK_HERDR")
		got, err := herdr.Find()
		if err != nil || got != want {
			t.Fatalf("Find() = %q, %v, want %q", got, err, want)
		}
	})
}

func TestClientWithEmptyBinReturnsFindsErrorAndRunsNothing(t *testing.T) {
	onPath := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	writeExecutable(t, filepath.Join(onPath, "herdr"), "touch "+marker)
	t.Setenv("PATH", onPath)
	t.Setenv("DESK_HERDR", filepath.Join(t.TempDir(), "no-such-herdr"))

	_, err := (&herdr.Client{}).Panes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "DESK_HERDR") {
		t.Fatalf("Panes() error = %v, want one naming DESK_HERDR", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("a herdr on PATH ran although DESK_HERDR was set")
	}
}
