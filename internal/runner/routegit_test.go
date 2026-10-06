package runner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// routeGitFixture is a fixture whose one root is the top of a git work tree with no isolation set, and a ready task
// with no route of its own.
func routeGitFixture(t *testing.T) (*fixture, string, model.Task) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "repo")
	fireGitRoot(t, root)
	f := newFixture(t, root, "")
	return f, parent, f.armThread("route me", "agent")
}

// requireNoRunStarted fails unless Start returned a plain error, not a refusal, and the store holds no run.
func requireNoRunStarted(t *testing.T, f *fixture, run model.Run, err error) {
	t.Helper()
	if err == nil || fireCode(err) != "" {
		t.Fatalf("Start() = %#v, %v; want an error that is no refusal", run, err)
	}
	if runs := f.runs(); len(runs) != 0 {
		t.Fatalf("runs = %#v, want none: a root git could not answer for must not run in-place", runs)
	}
}

// A work-tree root with no isolation, while git is not on the PATH, is an error: the run is never stored in-place.
func TestStartRefusesToDefaultTheIsolationWhenGitCannotBeAsked(t *testing.T) {
	f, _, task := routeGitFixture(t)
	t.Setenv("PATH", t.TempDir())

	run, err := f.runner().Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{Root: f.root})

	requireNoRunStarted(t, f, run, err)
}

// A root whose folder cannot be read is an error too, not a root that is no work tree.
func TestStartRefusesToDefaultTheIsolationWhenTheRootCannotBeRead(t *testing.T) {
	f, parent, task := routeGitFixture(t)
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	run, err := f.runner().Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{Root: f.root})

	if chmodErr := os.Chmod(parent, 0o700); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	requireNoRunStarted(t, f, run, err)
}
