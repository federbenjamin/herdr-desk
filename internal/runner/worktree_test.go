package runner_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// A worktree sits inside its root, where a root's tooling can require it, and a root that ignores nothing shows
// no untracked folder for it.
func TestStartNestsTheWorktreeInTheRootAndKeepsTheRootStatusClean(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	spawnGitRoot(t, root)
	f := newFixture(t, root, "worktree")
	task := f.armRoute("nest it", root, "worktree")
	want := filepath.Join(root, ".claude", "worktrees", "T"+strconv.Itoa(task.Number))

	f.startRun(f.runner(), task.Number)

	if got := f.herdr.Workspaces()[0].Cwd; got != want {
		t.Fatalf("workspace cwd = %q, want %q", got, want)
	}
	if status := spawnGit(t, root, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("root status = %q, want clean", status)
	}
	exclude, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil || strings.Count(string(exclude), "/.claude/worktrees/\n") != 1 {
		t.Fatalf("info/exclude = %q (%v), want the worktrees folder once", exclude, err)
	}
}

// A root whose own .gitignore already ignores the folder is left as it is.
func TestStartLeavesAnIgnoringRootsExcludeFileAlone(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	spawnGitRoot(t, root)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".claude/worktrees/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, root, "worktree")
	task := f.armRoute("already ignored", root, "worktree")

	f.startRun(f.runner(), task.Number)

	if run := f.run(task.Number); run.State != model.RunRunning {
		t.Fatalf("run = %#v, want running", run)
	}
	if exclude, _ := os.ReadFile(filepath.Join(root, ".git", "info", "exclude")); strings.Contains(string(exclude), "worktrees") {
		t.Fatalf("info/exclude = %q, want no worktrees rule added", exclude)
	}
}

// A run started before worktrees nested has its tree beside the root: a resume uses it, and adds no second tree
// for the branch.
func TestStartResumesAWorktreeAtTheOldPath(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	spawnGitRoot(t, root)
	f := newFixture(t, root, "worktree")
	task := f.armRoute("old path", root, "worktree")
	old := filepath.Join(parent, "repo-T"+strconv.Itoa(task.Number))
	branch := "desk/T" + strconv.Itoa(task.Number) + "-old-path"
	spawnGit(t, root, "worktree", "add", "-b", branch, old)

	f.startRun(f.runner(), task.Number)

	run := f.run(task.Number)
	if run.State != model.RunRunning || f.herdr.Workspaces()[0].Cwd != old || spawnGitBranch(t, old) != branch {
		t.Fatalf("run = %#v; workspaces = %#v, want it running in %s on %s", run, f.herdr.Workspaces(), old, branch)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "worktrees")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat of the nested folder = %v, want none made", err)
	}
	if n := strings.Count(spawnGit(t, root, "worktree", "list"), "\n"); n != 2 {
		t.Fatalf("worktree list has %d trees, want the root and the old one", n)
	}
}

func TestJobsRemovesACleanDoneWorktreeAtTheOldPath(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	spawnGitRoot(t, root)
	f := newFixture(t, root, "worktree")
	task := f.armRoute("old path done", root, "worktree")
	old := filepath.Join(parent, "repo-T"+strconv.Itoa(task.Number))
	spawnGit(t, root, "worktree", "add", "-b", "desk/T"+strconv.Itoa(task.Number)+"-old-path-done", old)
	done := model.StatusDone
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &done}); err != nil {
		t.Fatalf("finish task: %v", err)
	}

	f.runner().Jobs(f.ctx)

	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old worktree stat error = %v, want removed", err)
	}
}
