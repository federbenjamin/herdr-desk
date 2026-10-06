package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/federbenjamin/herdr-desk/internal/gitcmd"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

var runFileName = regexp.MustCompile(`^run-([0-9]+)\.md$`)

// sweepRunFiles removes the first-message file of every run that ended, failed, or was killed. A file whose
// name is not run-<id>.md, or whose id names no run, is left alone; with no run file the store is not read.
func (r *Runner) sweepRunFiles(ctx context.Context) {
	dir := r.o.Paths.RunsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			r.logErr("read %s", dir, err)
		}
		return
	}
	files := map[int64]string{}
	for _, e := range entries {
		m := runFileName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		files[id] = filepath.Join(dir, e.Name())
	}
	if len(files) == 0 {
		return
	}
	runs, err := r.o.Store.ListRuns(ctx)
	if err != nil {
		r.logErr("read the runs", err)
		return
	}
	for _, run := range runs {
		path, ok := files[run.ID]
		if !ok || !model.RunFinal(run.State) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			r.logErr("T%d run %d: remove %s", run.Task, run.ID, path, err)
		}
	}
}

// sweepWorktrees removes the worktree of each done task, archived or not, whose isolation is worktree and whose
// newest run is not live, with `git worktree remove` and no --force. A tree git refuses to remove (dirty,
// locked, or holding submodules) stays, with one runner note on the task per time it was set done, whatever git's
// reason on later ticks. A missing folder, or one git says is not a work tree of its own, is left alone; a folder
// that cannot be read, or that git cannot answer for, is logged on every tick and noted nowhere. The task's branch
// is kept.
func (r *Runner) sweepWorktrees(ctx context.Context) {
	done := []model.Status{model.StatusDone}
	var tasks []model.Task
	for _, f := range []store.Filter{{Statuses: done}, {Statuses: done, Archived: true}} {
		ts, err := r.o.Store.ListTasks(ctx, f)
		if err != nil {
			r.logErr("read done tasks", err)
			return
		}
		tasks = append(tasks, ts...)
	}
	for _, t := range tasks {
		if t.Isolation != "worktree" || t.Root == "" {
			continue
		}
		dir := worktreeDir(t.Root, t.Number)
		ours, err := isWorkTree(ctx, dir)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			r.logErr("T%d: worktree %s left alone, it could not be checked", t.Number, dir, err)
			continue
		}
		if !ours {
			continue
		}
		run, ok, err := r.o.Store.CurrentRun(ctx, t.Number)
		if err != nil {
			r.logErr("T%d: read its run", t.Number, err)
			continue
		}
		if ok && model.RunLive(run.State) {
			continue
		}
		if _, err := gitcmd.Run(ctx, t.Root, gitTimeout, "worktree", "remove", dir); err != nil {
			if ctx.Err() != nil {
				return
			}
			r.noteKeptWorktree(ctx, t.Number, dir, err)
			continue
		}
		r.o.Logf("herdr-desk runner: T%d: removed worktree %s", t.Number, dir)
	}
}

// keptWorktreeNote is the start of the note that says git would not remove dir; git's reason and what to do follow.
func keptWorktreeNote(dir string) string {
	return "worktree " + dir + " was not removed: "
}

// noteKeptWorktree notes on task that git would not remove its worktree dir, unless a runner note on keeping dir
// was written since the task's newest status write, its write to done: git's reason may change between ticks.
func (r *Runner) noteKeptWorktree(ctx context.Context, task int, dir string, gitErr error) {
	d, err := r.o.Store.GetTask(ctx, task)
	if err != nil {
		r.logErr("T%d: read the task", task, err)
		return
	}
	if keptNoted(d.History, dir) {
		return
	}
	why := gitErr.Error()
	var ge *gitcmd.Error
	if errors.As(gitErr, &ge) && ge.Stderr != "" {
		why = ge.Stderr
	}
	r.note(ctx, store.Actor{}, task, []string{model.TagRunner},
		keptWorktreeNote(dir)+clip(why)+"; remove it by hand once its changes are safe")
}

// keptNoted reports whether history holds a runner note on keeping dir after its newest status write.
func keptNoted(history []model.Event, dir string) bool {
	i, _, _ := newestStatusWrite(history)
	for _, e := range history[i+1:] {
		var n model.NoteData
		if e.Kind == model.KindNote && slices.Contains(e.Tags, model.TagRunner) && json.Unmarshal(e.Data, &n) == nil &&
			strings.HasPrefix(n.Text, keptWorktreeNote(dir)) {
			return true
		}
	}
	return false
}
