package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// Start starts a run of the task on route, each field of which wins over the task's own and the defaults (Resolve).
// It refuses, in this order: not-allowed for a session that is not the recorded coordinator's, runner-off,
// runner-paused, no-herdr, a route Resolve refuses, and then, from the store, the task's own refusals and
// cap-reached once today's runs reach runner.max_runs_per_day, counted in the transaction that inserts the run. The
// store decides starting or waiting; a starting run is spawned, and the sidebar rows are reported. A task that
// already has a starting, waiting, or running run gets that run and no error. A task whose run was idle gets a new
// run once the idle run's pane, and any other pane an older run of the task is owed a close, is closed
// (closeReplaced); while one may still be open, the new run fails. A
// spawn that fails returns the failed run, its reason on it, and no error. A run whose state cannot be read back
// after its spawn is an error.
func (r *Runner) Start(ctx context.Context, a store.Actor, task int, route store.RunRoute) (model.Run, error) {
	if err := r.mayStart(ctx, a); err != nil {
		return model.Run{}, err
	}
	state, h, why := r.compute()
	refuse := func(code, msg string) (model.Run, error) {
		if why != nil {
			msg += ": " + why.Error()
		}
		return model.Run{}, &model.Refusal{Code: code, Msg: msg}
	}
	switch state {
	case StateOff:
		return refuse(model.CodeRunnerOff, "the runner is off (runner.enabled = false)")
	case StatePaused:
		return refuse(model.CodeRunnerPaused, "the runner is paused; herdr-desk runner resume starts runs again")
	case StateNoHerdr:
		return refuse(model.CodeNoHerdr, "herdr was not found, so no pane can be opened")
	}
	c := r.o.Config
	d, err := r.o.Store.GetTask(ctx, task)
	if err != nil {
		return model.Run{}, err
	}
	resolved, err := Resolve(d.Task, route, Roots(c, r.o.Paths), c.Agent.Models)
	if err != nil {
		return model.Run{}, err
	}
	run, err := r.o.Store.StartRun(ctx, task, resolved, store.RunCaps{Slots: c.Runner.Cap, PerDay: c.Runner.MaxRunsPerDay, Since: r.midnight()})
	if errors.Is(err, store.ErrRunLive) {
		return run, nil
	}
	if err != nil {
		return model.Run{}, err
	}
	open := r.closeReplaced(ctx, h, run)
	how := run.Isolation
	if run.Model != "" {
		how += ", " + run.Model
	}
	r.note(ctx, store.Actor{Run: run.ID}, task, []string{model.TagRunner}, fmt.Sprintf("run %d %s: %s (%s)", run.ID, run.State, run.Root, how))
	if open != "" {
		r.fail(ctx, run, run.State, fmt.Sprintf("run %d was not started: %s; the ticker closes it again", run.ID, open))
		return r.readBack(ctx, run)
	}
	if run.State != model.RunStarting {
		r.report(ctx, run, false)
		return run, nil
	}
	if r.spawn(ctx, h, d.Task, run) {
		r.report(ctx, run, true)
	}
	return r.readBack(ctx, run)
}

// closeReplaced closes, before run can own its root, every pane an older run of run's task is owed a close: among
// them the idle run StartRun ended in run's own transaction, whose worker is still at its prompt there and must not
// go on working beside run. Which runs are owed is read from the store after that transaction (LeftOpenRuns), never
// from what Start read before it. closeOwed closes each pane and clears its mark. It returns "" when no such pane is
// left open, else why one may be; that run keeps its mark, so Jobs closes its pane again.
func (r *Runner) closeReplaced(ctx context.Context, h Herdr, run model.Run) string {
	marked, err := r.o.Store.LeftOpenRuns(ctx)
	if err != nil {
		r.logErr("T%d run %d: read the runs whose pane is owed a close", run.Task, run.ID, err)
		return "the runs whose pane is owed a close could not be read: " + clip(err.Error())
	}
	var owed []model.Run
	for _, old := range marked {
		if old.Task == run.Task && old.ID < run.ID {
			owed = append(owed, old)
		}
	}
	if len(owed) == 0 {
		return ""
	}
	panes, err := h.Panes(ctx)
	if err != nil {
		r.logErr("T%d run %d: list herdr panes to close the panes of its older runs", run.Task, run.ID, err)
		var open []string
		for _, old := range owed {
			open = append(open, mayBeOpen(old, "herdr did not list its panes: "+clip(err.Error())))
		}
		return strings.Join(open, "; ")
	}
	return strings.Join(r.closeOwed(ctx, h, panes, owed), "; ")
}

// readBack returns the run as the store holds it now: its spawn, or a failure, moved it on from the row StartRun
// inserted. A run that cannot be read back is an error, logged, never the inserted row's state.
func (r *Runner) readBack(ctx context.Context, run model.Run) (model.Run, error) {
	cur, ok, err := r.o.Store.CurrentRun(ctx, run.Task)
	if err == nil && ok && cur.ID == run.ID {
		return cur, nil
	}
	if err == nil {
		err = fmt.Errorf("a newer run owns T%d", run.Task)
	}
	r.logErr("T%d run %d: read the run back", run.Task, run.ID, err)
	return model.Run{}, fmt.Errorf("T%d run %d: its state could not be read back: %w", run.Task, run.ID, err)
}

// mayStart refuses an agent session that is not the recorded coordinator's: a person and the coordinator may
// start runs, a worker and any other agent may not.
func (r *Runner) mayStart(ctx context.Context, a store.Actor) error {
	if a.Session == "" {
		return nil
	}
	c, ok, err := r.o.Store.Coordinator(ctx)
	if err != nil {
		return err
	}
	if ok && c.Session == a.Session {
		return nil
	}
	return &model.Refusal{Code: model.CodeNotAllowed, Msg: "only a person or the desk's coordinator may start a run"}
}

// midnight is the start of the runner's today: runner.max_runs_per_day counts the runs started since.
func (r *Runner) midnight() time.Time { return Midnight(r.o.Now()) }

// Midnight is the start of now's day, local time: the one rule for the day runner.max_runs_per_day counts, which the
// cap and the status's count of today's runs both use.
func Midnight(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
}

// StartWaiting spawns waiting runs, oldest first, while the store finds one whose slot and root are free, and
// reports each it spawned. It starts nothing unless the runner is on.
func (r *Runner) StartWaiting(ctx context.Context) {
	state, h, _ := r.compute()
	if state != StateOn {
		return
	}
	for {
		run, ok, err := r.o.Store.ClaimWaiting(ctx, r.o.Config.Runner.Cap)
		if err != nil {
			r.logErr("claim a waiting run", err)
			return
		}
		if !ok {
			return
		}
		d, err := r.o.Store.GetTask(ctx, run.Task)
		if err != nil {
			r.fail(ctx, run, model.RunStarting, "spawn: could not read the task: "+clip(err.Error()))
			continue
		}
		if r.spawn(ctx, h, d.Task, run) {
			r.report(ctx, run, true)
		}
	}
}

// AfterSet runs after a task's status was written through the API: a write that ended a run frees a slot, so it
// starts what waits. Then it reports the task's run row, which a worker's hand-back or done changes, and the
// coordinator's counts.
func (r *Runner) AfterSet(ctx context.Context, task int) {
	r.StartWaiting(ctx)
	run, ok, err := r.o.Store.CurrentRun(ctx, task)
	if err != nil {
		r.logErr("T%d: read its run", task, err)
	}
	r.report(ctx, run, ok)
}
