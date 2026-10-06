package runner_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// sweepDoneWorktree starts a run of a task on a git root with worktree isolation, makes its tree dirty when dirty
// is set (a tracked file changed), and sets the task done. It returns the fixture, the runner, the task, and the tree.
func sweepDoneWorktree(t *testing.T, dirty bool) (*fixture, *runner.Runner, model.Task, string) {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	spawnGitRoot(t, root)
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spawnGit(t, root, "add", "tracked.txt")
	spawnGit(t, root, "-c", "user.name=desk test", "-c", "user.email=desk@example.test", "commit", "-m", "track file")
	f := newFixture(t, root, "worktree")
	task := f.armRoute("sweep me", root, "worktree")
	r := f.runner()
	f.startRun(r, task.Number)
	tree := filepath.Join(parent, "repo-T"+strconv.Itoa(task.Number))
	if dirty {
		if err := os.WriteFile(filepath.Join(tree, "tracked.txt"), []byte("changed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	done := model.StatusDone
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &done}); err != nil {
		t.Fatalf("finish T%d: %v", task.Number, err)
	}
	return f, r, task, tree
}

// keptNotes returns the texts of the task's runner notes on keeping tree.
func (f *fixture) keptNotes(task int, tree string) []string {
	f.t.Helper()
	var out []string
	for _, e := range f.task(task).History {
		var n model.NoteData
		if e.Kind == model.KindNote && slices.Contains(e.Tags, model.TagRunner) && json.Unmarshal(e.Data, &n) == nil &&
			strings.HasPrefix(n.Text, "worktree "+tree+" was not removed:") {
			out = append(out, n.Text)
		}
	}
	return out
}

// sweepDB opens the fixture's store a second time, for a test to break one read or write the runner makes.
func sweepDB(t *testing.T, f *fixture) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", f.paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func sweepExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// git's reason for keeping a tree can change between ticks (locked, then dirty): the done cycle still gets one note.
func TestJobsNotesAKeptWorktreeOnceWhenGitsReasonChanges(t *testing.T) {
	f, r, task, tree := sweepDoneWorktree(t, true)
	spawnGit(t, f.root, "worktree", "lock", "--reason", "held by a test", tree)

	r.Jobs(f.ctx)
	first := f.keptNotes(task.Number, tree)
	if len(first) != 1 || !strings.Contains(first[0], "locked") {
		t.Fatalf("notes after the locked tick = %#v, want one naming the lock", first)
	}

	spawnGit(t, f.root, "worktree", "unlock", tree)
	r.Jobs(f.ctx)
	if got := f.keptNotes(task.Number, tree); len(got) != 1 {
		t.Fatalf("notes after the tree is unlocked but still dirty = %#v, want the one note of this done cycle", got)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("dirty tree: %v, want it kept", err)
	}
}

// A runner note on another tree does not stand for this one's.
func TestJobsNotesAKeptWorktreeEvenWhenAnotherTreesNoteIsInTheCycle(t *testing.T) {
	f, r, task, tree := sweepDoneWorktree(t, true)
	other := "worktree " + tree + "-other was not removed: dirty; remove it by hand once its changes are safe"
	if _, err := f.store.Note(f.ctx, store.Actor{}, store.NoteInput{NoteData: model.NoteData{Text: other}, Task: task.Number,
		Tags: []string{model.TagRunner}}); err != nil {
		t.Fatal(err)
	}
	r.Jobs(f.ctx)
	if got := f.keptNotes(task.Number, tree); len(got) != 1 {
		t.Fatalf("notes on %s = %#v, want one", tree, got)
	}
}

// A tree git cannot be asked about (git is not on the ticker's PATH) stays, is noted nowhere, and the log says why on
// every tick.
func TestJobsLogsAWorktreeGitCannotBeAskedAbout(t *testing.T) {
	f, r, task, tree := sweepDoneWorktree(t, false)
	before := len(f.task(task.Number).History)
	t.Setenv("PATH", t.TempDir())

	r.Jobs(f.ctx)
	r.Jobs(f.ctx)

	want := "T" + strconv.Itoa(task.Number) + ": worktree " + tree + " left alone, it could not be checked"
	if n := strings.Count(f.logged(), want); n != 2 {
		t.Fatalf("log holds %q %d times, want once per tick:\n%s", want, n, f.logged())
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("tree: %v, want it kept", err)
	}
	if got := len(f.task(task.Number).History); got != before {
		t.Fatalf("history grew from %d to %d, want no note", before, got)
	}
}

// A tree whose folder cannot be read stays and is logged.
func TestJobsLogsAWorktreeFolderThatCannotBeRead(t *testing.T) {
	f, r, task, tree := sweepDoneWorktree(t, false)
	parent := filepath.Dir(tree)
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	r.Jobs(f.ctx)

	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	want := "T" + strconv.Itoa(task.Number) + ": worktree " + tree + " left alone, it could not be checked"
	if !strings.Contains(f.logged(), want) || !strings.Contains(f.logged(), "permission denied") {
		t.Fatalf("log = %q, want %q with the permission error", f.logged(), want)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("tree: %v, want it kept", err)
	}
}

// A folder at the tree's path that git says is no repository is not ours: it stays, and nothing is logged about it.
func TestJobsLeavesAPlainFolderAtTheWorktreePathQuietly(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	spawnGitRoot(t, root)
	f := newFixture(t, root, "worktree")
	task := f.armRoute("plain folder", root, "worktree")
	done := model.StatusDone
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &done}); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(parent, "repo-T"+strconv.Itoa(task.Number))
	if err := os.Mkdir(plain, 0o700); err != nil {
		t.Fatal(err)
	}

	f.runner().Jobs(f.ctx)

	if _, err := os.Stat(plain); err != nil {
		t.Fatalf("plain folder: %v, want it kept", err)
	}
	if strings.Contains(f.logged(), plain) {
		t.Fatalf("log = %q, want nothing about %s", f.logged(), plain)
	}
}

// A run file that cannot be removed is logged with its run, and the sweep goes on to the next file; once the folder
// is writable again a later tick removes them all.
func TestJobsLogsEachRunFileItCannotRemoveAndGoesOn(t *testing.T) {
	f := newFixture(t, "", "self")
	var ids []int64
	for _, title := range []string{"first", "second"} {
		task := f.armRoute(title, f.root, "self")
		run, err := f.store.StartRun(f.ctx, task.Number, store.RunRoute{Root: f.root, Isolation: "self"}, store.RunCaps{Slots: 3, PerDay: 20})
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := f.store.UpdateRun(f.ctx, run.ID, model.RunStarting, store.RunUpdate{State: model.RunFailed}); err != nil || !ok {
			t.Fatalf("fail run %d = (%t, %v)", run.ID, ok, err)
		}
		ids = append(ids, run.ID)
	}
	dir := f.paths.RunsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := os.WriteFile(f.paths.RunMessage(id), []byte("first message"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	r := f.runner()

	r.Jobs(f.ctx)

	for _, id := range ids {
		want := "run " + strconv.FormatInt(id, 10) + ": remove " + f.paths.RunMessage(id)
		if !strings.Contains(f.logged(), want) {
			t.Errorf("log = %q, want %q", f.logged(), want)
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	r.Jobs(f.ctx)
	for _, id := range ids {
		if _, err := os.Stat(f.paths.RunMessage(id)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("run %d's file: %v, want it removed once the folder is writable", id, err)
		}
	}
}

// A runs folder that cannot be read is logged; the sweep reads no runs.
func TestJobsLogsARunsFolderThatCannotBeRead(t *testing.T) {
	f := newFixture(t, "", "self")
	if err := os.MkdirAll(filepath.Dir(f.paths.RunsDir()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.paths.RunsDir(), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.runner().Jobs(f.ctx)
	if want := "read " + f.paths.RunsDir(); !strings.Contains(f.logged(), want) {
		t.Fatalf("log = %q, want %q", f.logged(), want)
	}
}

// Done tasks that cannot be listed are logged, and no tree is touched.
func TestJobsLogsDoneTasksThatCannotBeRead(t *testing.T) {
	f, r, task, tree := sweepDoneWorktree(t, false)
	db := sweepDB(t, f)
	sweepExec(t, db, `UPDATE tasks SET created_ts = 'unreadable' WHERE number = ?`, task.Number)

	r.Jobs(f.ctx)

	if !strings.Contains(f.logged(), "read done tasks") {
		t.Fatalf("log = %q, want the failed read of done tasks", f.logged())
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("tree: %v, want it kept", err)
	}
}

// A kept tree whose task cannot be read is logged and noted nowhere; once the task reads again the next tick writes
// the one note.
func TestJobsLogsAKeptWorktreeWhoseTaskCannotBeReadThenNotesItOnce(t *testing.T) {
	f, r, task, tree := sweepDoneWorktree(t, true)
	db := sweepDB(t, f)
	var ts string
	if err := db.QueryRow(`SELECT ts FROM events WHERE task = ? AND kind = 'task'`, task.Number).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	sweepExec(t, db, `UPDATE events SET ts = 'unreadable' WHERE task = ? AND kind = 'task'`, task.Number)

	r.Jobs(f.ctx)

	if want := "T" + strconv.Itoa(task.Number) + ": read the task"; !strings.Contains(f.logged(), want) {
		t.Fatalf("log = %q, want %q", f.logged(), want)
	}
	sweepExec(t, db, `UPDATE events SET ts = ? WHERE task = ? AND kind = 'task'`, ts, task.Number)
	if got := f.keptNotes(task.Number, tree); len(got) != 0 {
		t.Fatalf("notes while the task could not be read = %#v, want none", got)
	}
	r.Jobs(f.ctx)
	r.Jobs(f.ctx)
	if got := f.keptNotes(task.Number, tree); len(got) != 1 {
		t.Fatalf("notes once the task reads again = %#v, want one", got)
	}
}

// A refused note is logged on each tick it is tried, and is not counted as written: once notes go through, the
// done cycle gets exactly one.
func TestJobsLogsARefusedKeptWorktreeNoteAndWritesItOnceLater(t *testing.T) {
	f, r, task, tree := sweepDoneWorktree(t, true)
	db := sweepDB(t, f)
	sweepExec(t, db, `CREATE TRIGGER refuse_notes BEFORE INSERT ON events WHEN NEW.kind = 'note'
		BEGIN SELECT RAISE(ABORT, 'note refused'); END`)

	r.Jobs(f.ctx)
	r.Jobs(f.ctx)

	want := "T" + strconv.Itoa(task.Number) + ": write a note"
	if n := strings.Count(f.logged(), want); n != 2 {
		t.Fatalf("log holds %q %d times, want once per tick:\n%s", want, n, f.logged())
	}
	sweepExec(t, db, `DROP TRIGGER refuse_notes`)
	if got := f.keptNotes(task.Number, tree); len(got) != 0 {
		t.Fatalf("notes while refused = %#v, want none", got)
	}
	r.Jobs(f.ctx)
	r.Jobs(f.ctx)
	if got := f.keptNotes(task.Number, tree); len(got) != 1 {
		t.Fatalf("notes once notes go through = %#v, want one", got)
	}
}
