package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
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
		result := runDeskWithEnv(t, home.Machine, cwd, step.args, "", nil)
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
		result := runDeskWithEnv(t, home.Machine, cwd, []string{"roots", "add", path}, "", nil)
		if result.exit != 2 || !strings.Contains(result.stderr, path) || result.stdout != "" {
			t.Errorf("roots add %s = (%d, %q, %q), want exit 2 naming the path", path, result.exit, result.stdout, result.stderr)
		}
		if cfg, err := config.Load(home.Paths.ConfigFile()); err != nil || len(cfg.Roots) != 0 {
			t.Errorf("roots after refusing %s = %#v, %v; want none", path, cfg.Roots, err)
		}
	}
}

func TestRootsAddSetsFirstMessageAndAgentsMayStartAndOnlyAPersonGrantsAgents(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	cwd := t.TempDir()
	root := filepath.Join(cwd, "project")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	agent := map[string]string{"DESK_SESSION": "agent-session"}
	for _, step := range []struct {
		args      []string
		env       map[string]string
		exit      int
		wantFirst string
		wantMay   bool
	}{
		{[]string{"roots", "add", root, "--agents-may-start", "--first-message", "/build {task_file}"}, nil, 0, "/build {task_file}", true},
		{[]string{"roots", "add", root, "--about", "kept"}, nil, 0, "/build {task_file}", true},
		{[]string{"roots", "add", root, "--agents-may-start=false"}, agent, 0, "/build {task_file}", false},
		{[]string{"roots", "add", root, "--agents-may-start"}, agent, 1, "/build {task_file}", false},
		{[]string{"roots", "add", root, "--first-message", "/build"}, nil, 2, "/build {task_file}", false},
		{[]string{"roots", "add", root, "--first-message", ""}, nil, 0, "", false},
	} {
		result := runDeskWithEnv(t, home.Machine, cwd, step.args, "", step.env)
		if result.exit != step.exit {
			t.Fatalf("%v exit = %d, stderr = %q; want %d", step.args, result.exit, result.stderr, step.exit)
		}
		if step.exit == 1 && !strings.Contains(result.stderr, "not-allowed") {
			t.Fatalf("%v stderr = %q, want not-allowed", step.args, result.stderr)
		}
		cfg, err := config.Load(home.Paths.ConfigFile())
		if err != nil || len(cfg.Roots) != 1 || cfg.Roots[0].FirstMessage != step.wantFirst || cfg.Roots[0].AgentsMayStart != step.wantMay {
			t.Fatalf("after %v roots = %#v, %v; want first_message %q and agents_may_start %t", step.args, cfg.Roots, err, step.wantFirst, step.wantMay)
		}
	}
}
