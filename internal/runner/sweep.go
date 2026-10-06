package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

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
// locked, or holding submodules) stays, with one runner note on the task per time it was set done. A missing
// folder, or one git does not own as a work tree, is left alone. The task's branch is kept.
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
		if _, err := os.Stat(dir); err != nil {
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
		if !isWorkTree(ctx, dir) {
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

// noteKeptWorktree notes on task that git would not remove its worktree dir, unless the same note was written
// since the task was last set done.
func (r *Runner) noteKeptWorktree(ctx context.Context, task int, dir string, gitErr error) {
	why := gitErr.Error()
	var ge *gitcmd.Error
	if errors.As(gitErr, &ge) && ge.Stderr != "" {
		why = ge.Stderr
	}
	text := fmt.Sprintf("worktree %s was not removed: %s; remove it by hand once its changes are safe", dir, clip(why))
	d, err := r.o.Store.GetTask(ctx, task)
	if err != nil {
		r.logErr("T%d: read the task", task, err)
		return
	}
	if notedSinceDone(d.History, text) {
		return
	}
	r.note(ctx, store.Actor{}, task, []string{model.TagRunner}, text)
}

// notedSinceDone reports whether history holds a note reading text after its newest status write to done.
func notedSinceDone(history []model.Event, text string) bool {
	for i := len(history) - 1; i >= 0; i-- {
		e := history[i]
		switch e.Kind {
		case model.KindNote:
			var n model.NoteData
			if json.Unmarshal(e.Data, &n) == nil && n.Text == text {
				return true
			}
		case model.KindSet, model.KindTask:
			var p model.Patch
			if json.Unmarshal(e.Data, &p) == nil && p.Status != nil && *p.Status == model.StatusDone {
				return false
			}
		}
	}
	return false
}
