package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// Start starts a run of the task on route, each field of which wins over the task's own and the defaults (Resolve).
// It refuses, in this order: not-allowed for a session that is not the recorded coordinator's, runner-off,
// runner-paused, no-herdr, and cap-reached once today's runs reach runner.max_runs_per_day. The store then decides
// starting or waiting; a starting run is spawned, and the sidebar rows are reported. A task that already has a
// starting, waiting, or running run gets that run and no error.
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
	today, err := r.today(ctx)
	if err != nil {
		return model.Run{}, err
	}
	if today >= c.Runner.MaxRunsPerDay {
		return refuse(model.CodeCapReached, fmt.Sprintf("%d runs started today, runner.max_runs_per_day is %d", today, c.Runner.MaxRunsPerDay))
	}
	d, err := r.o.Store.GetTask(ctx, task)
	if err != nil {
		return model.Run{}, err
	}
	resolved, err := Resolve(d.Task, route, Roots(c, r.o.Paths), c.Agent.Models)
	if err != nil {
		return model.Run{}, err
	}
	run, err := r.o.Store.StartRun(ctx, task, resolved, c.Runner.Cap)
	if errors.Is(err, store.ErrRunLive) {
		return run, nil
	}
	if err != nil {
		return model.Run{}, err
	}
	how := run.Isolation
	if run.Model != "" {
		how += ", " + run.Model
	}
	r.note(ctx, store.Actor{Run: run.ID}, task, []string{model.TagRunner}, fmt.Sprintf("run %d %s: %s (%s)", run.ID, run.State, run.Root, how))
	if run.State != model.RunStarting {
		r.report(ctx, run, false)
		return run, nil
	}
	if r.spawn(ctx, h, d.Task, run) {
		r.report(ctx, run, true)
	}
	if cur, ok, err := r.o.Store.CurrentRun(ctx, task); err == nil && ok && cur.ID == run.ID {
		run = cur
	}
	return run, nil
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

// today counts the runs started since local midnight.
func (r *Runner) today(ctx context.Context) (int, error) {
	now := r.o.Now()
	y, m, d := now.Date()
	return r.o.Store.RunsSince(ctx, time.Date(y, m, d, 0, 0, 0, 0, now.Location()))
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
