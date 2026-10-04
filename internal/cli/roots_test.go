package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/testutil"
)

func TestRootsAddOnAListedPathChangesOnlyTheFieldsWhoseFlagsWerePassed(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	cwd := t.TempDir()
	root := filepath.Join(cwd, "project")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		args      []string
		wantAbout string
		wantIso   string
	}{
		{[]string{"roots", "add", root, "--about", "desk itself", "--isolation", "worktree"}, "desk itself", "worktree"},
		{[]string{"roots", "add", root}, "desk itself", "worktree"},
		{[]string{"roots", "add", root, "--isolation", "self"}, "desk itself", "self"},
		{[]string{"roots", "add", root, "--about", "renamed"}, "renamed", "self"},
		{[]string{"roots", "add", root, "--about", ""}, "", "self"},
	} {
		result := runDeskWithEnv(t, home.Machine, cwd, step.args, "", nil, nil)
		if result.exit != 0 {
			t.Fatalf("%v exit = %d, stderr = %q", step.args, result.exit, result.stderr)
		}
		cfg, err := config.Load(home.Paths.ConfigFile())
		if err != nil || len(cfg.Roots) != 1 || cfg.Roots[0].About != step.wantAbout || cfg.Roots[0].Isolation != step.wantIso {
			t.Fatalf("after %v roots = %#v, %v; want about %q and isolation %q", step.args, cfg.Roots, err, step.wantAbout, step.wantIso)
		}
	}
}

func TestRootsAddRefusesAPathThatIsNotAnExistingDirectory(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	cwd := t.TempDir()
	file := filepath.Join(cwd, "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(cwd, "no", "such", "dir"), file} {
		result := runDeskWithEnv(t, home.Machine, cwd, []string{"roots", "add", path}, "", nil, nil)
		if result.exit != 2 || !strings.Contains(result.stderr, path) || result.stdout != "" {
			t.Errorf("roots add %s = (%d, %q, %q), want exit 2 naming the path", path, result.exit, result.stdout, result.stderr)
		}
		if cfg, err := config.Load(home.Paths.ConfigFile()); err != nil || len(cfg.Roots) != 0 {
			t.Errorf("roots after refusing %s = %#v, %v; want none", path, cfg.Roots, err)
		}
	}
}
