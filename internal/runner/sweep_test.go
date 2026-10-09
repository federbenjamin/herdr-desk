package runner_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestJobsDeletesOnlyFilesForFinalRuns(t *testing.T) {
	f := newFixture(t, "", "self")
	finalTask := f.armRoute("final run", f.root, "self")
	finalRun, err := f.store.StartRun(f.ctx, store.Actor{}, finalTask.Number, store.RunRoute{Root: f.root, Isolation: "self"}, store.RunCaps{Slots: 1, PerDay: 20})
	if err != nil {
		t.Fatalf("start final run: %v", err)
	}
	if ok, err := f.store.UpdateRun(f.ctx, finalRun.ID, model.RunStarting, store.RunUpdate{State: model.RunFailed}); err != nil || !ok {
		t.Fatalf("fail run = (%t, %v)", ok, err)
	}

	liveTask := f.armRoute("live run", f.root, "self")
	liveRun, err := f.store.StartRun(f.ctx, store.Actor{}, liveTask.Number, store.RunRoute{Root: f.root, Isolation: "self"}, store.RunCaps{Slots: 0, PerDay: 20})
	if err != nil {
		t.Fatalf("start waiting run: %v", err)
	}

	if err := os.MkdirAll(f.paths.RunsDir(), 0o700); err != nil {
		t.Fatalf("make runs directory: %v", err)
	}
	finalFile := f.paths.RunMessage(finalRun.ID)
	liveFile := f.paths.RunMessage(liveRun.ID)
	unknownFile := filepath.Join(f.paths.RunsDir(), "run-99999.md")
	nonRunFile := filepath.Join(f.paths.RunsDir(), "run-not-a-run.md")
	for _, path := range []string{finalFile, liveFile, unknownFile, nonRunFile} {
		if err := os.WriteFile(path, []byte("first message"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	f.runner().Jobs(f.ctx)

	if _, err := os.Stat(finalFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final run file stat error = %v, want removed", err)
	}
	for _, path := range []string{liveFile, unknownFile, nonRunFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("file %s stat error = %v, want retained", path, err)
		}
	}
}

func TestJobsRemovesACleanDoneWorktreeWithoutWritingANote(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	spawnGitRoot(t, root)
	f := newFixture(t, root, "worktree")
	task := f.armRoute("remove clean worktree", root, "worktree")
	r := f.runner()
	f.startRun(r, task.Number)
	worktree := filepath.Join(parent, "repo-T"+strconv.Itoa(task.Number))
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("worktree before done: %v", err)
	}

	done := model.StatusDone
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &done}); err != nil {
		t.Fatalf("finish task: %v", err)
	}
	before := len(f.task(task.Number).History)

	r.Jobs(f.ctx)

	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree after jobs stat error = %v, want removed", err)
	}
	if got := len(f.task(task.Number).History); got != before {
		t.Fatalf("task history grew from %d to %d, want no worktree-removal note", before, got)
	}
	if !strings.Contains(f.logged(), "T"+strconv.Itoa(task.Number)+": removed worktree "+worktree) {
		t.Fatalf("runner log = %q, want successful removal of %s", f.logged(), worktree)
	}
}

// A done hand-back leaves the worker's pane open in the worktree: Jobs closes that pane before it removes the tree,
// and a pane that does not close keeps the tree until a later tick closes it.
func TestJobsClosesADoneTasksPaneBeforeItRemovesTheWorktree(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	spawnGitRoot(t, root)
	f := newFixture(t, root, "worktree")
	task := f.armRoute("done with its pane open", root, "worktree")
	r := f.runner()
	run := f.startRun(r, task.Number)
	worktree := filepath.Join(parent, "repo-T"+strconv.Itoa(task.Number))

	review := model.StatusReview
	if _, err := f.store.SetTask(f.ctx, store.Actor{Session: run.Session, Run: run.ID}, task.Number, model.Patch{Status: &review}); err != nil {
		t.Fatalf("worker hands back: %v", err)
	}
	done := model.StatusDone
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &done}); err != nil {
		t.Fatalf("finish task: %v", err)
	}
	if got := f.run(task.Number); got.State != model.RunEnded || !got.LeftOpen {
		t.Fatalf("run after done = %#v, want ended with its pane owed a close", got)
	}

	f.herdr.Fail("ClosePane", errors.New("herdr would not close it"))
	r.Jobs(f.ctx)
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("worktree after a pane that did not close: %v, want it kept", err)
	}

	f.herdr.Fail("ClosePane", nil)
	r.Jobs(f.ctx)
	if closed := f.herdr.Closed(); len(closed) != 1 || closed[0] != run.Pane {
		t.Fatalf("closed panes = %q, want the run's pane %s", closed, run.Pane)
	}
	if got := f.run(task.Number); got.LeftOpen {
		t.Fatalf("run after its pane closed = %#v, want no close owed", got)
	}
	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree after its pane closed: stat error = %v, want removed", err)
	}
}

func TestJobsNotesADirtyWorktreeOnceForEachDoneCycle(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	spawnGitRoot(t, root)
	tracked := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("initial\n"), 0o600); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	spawnGit(t, root, "add", "tracked.txt")
	spawnGit(t, root, "-c", "user.name=desk test", "-c", "user.email=desk@example.test", "commit", "-m", "track file")

	f := newFixture(t, root, "worktree")
	task := f.armRoute("keep dirty worktree", root, "worktree")
	r := f.runner()
	f.startRun(r, task.Number)
	worktree := filepath.Join(parent, "repo-T"+strconv.Itoa(task.Number))
	if err := os.WriteFile(filepath.Join(worktree, "tracked.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("dirty worktree: %v", err)
	}

	done := model.StatusDone
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &done}); err != nil {
		t.Fatalf("finish task: %v", err)
	}
	w4RemovalNotes := func() []string {
		var notes []string
		for _, event := range f.task(task.Number).History {
			var note model.NoteData
			if event.Kind == model.KindNote && len(event.Tags) == 1 && event.Tags[0] == model.TagRunner && json.Unmarshal(event.Data, &note) == nil && strings.HasPrefix(note.Text, "worktree "+worktree+" was not removed:") {
				notes = append(notes, note.Text)
			}
		}
		return notes
	}

	r.Jobs(f.ctx)
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("dirty worktree stat error = %v, want retained", err)
	}
	first := w4RemovalNotes()
	if len(first) != 1 || !strings.Contains(first[0], "remove it by hand once its changes are safe") {
		t.Fatalf("dirty worktree notes = %#v, want one actionable runner note", first)
	}

	r.Jobs(f.ctx)
	if got := w4RemovalNotes(); len(got) != 1 {
		t.Fatalf("dirty worktree notes after another tick = %#v, want the first done cycle noted once", got)
	}

	open := model.StatusOpen
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &open}); err != nil {
		t.Fatalf("reopen task: %v", err)
	}
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &done}); err != nil {
		t.Fatalf("finish task again: %v", err)
	}
	r.Jobs(f.ctx)
	if got := w4RemovalNotes(); len(got) != 2 {
		t.Fatalf("dirty worktree notes after another done cycle = %#v, want one note for each cycle", got)
	}
}
