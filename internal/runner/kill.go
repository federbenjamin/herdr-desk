package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/federbenjamin/desk/internal/herdr"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

// killTries bounds Kill's retries when a tick moves the run on between Kill's read and its write.
const killTries = 3

// Kill stops the task's live run: it kills the pane's processes, closes the pane, sets the run killed and the
// task blocked. It returns the task. No live run is no-run; an actor with a session gets not-allowed.
func (r *Runner) Kill(ctx context.Context, a store.Actor, task int) (model.Task, error) {
	if a.Session != "" {
		return model.Task{}, &model.Refusal{Code: model.CodeNotAllowed, Msg: "an agent may not kill a run; a person does"}
	}
	if _, err := r.o.Store.GetTask(ctx, task); err != nil {
		return model.Task{}, err
	}
	for range killTries {
		run, ok, err := r.o.Store.CurrentRun(ctx, task)
		if err != nil {
			return model.Task{}, err
		}
		if !ok || !model.RunLive(run.State) {
			return model.Task{}, &model.Refusal{Code: model.CodeNoRun, Msg: fmt.Sprintf("T%d has no live run", task)}
		}
		msg := fmt.Sprintf("run %d killed while %s", run.ID, run.State)
		if run.State == model.RunRunning {
			_, h := r.compute()
			if h == nil {
				return model.Task{}, errors.New("no herdr binary on PATH: the pane cannot be reached")
			}
			panes, err := h.Panes(ctx)
			if err != nil {
				return model.Task{}, err
			}
			msg = fmt.Sprintf("run %d killed: %s", run.ID, r.killPane(ctx, h, run, panes))
		}
		claimed, err := r.o.Store.UpdateRun(ctx, run.ID, run.State, store.RunUpdate{State: model.RunKilled})
		if err != nil {
			return model.Task{}, err
		}
		if !claimed {
			continue
		}
		r.note(ctx, store.Actor{Run: run.ID}, task, []string{model.TagRunner}, msg)
		blocked := model.StatusBlocked
		return r.o.Store.SetTask(ctx, a, task, model.Patch{Status: &blocked})
	}
	return model.Task{}, fmt.Errorf("T%d's run kept changing state; try again", task)
}

// stop is the time limit's kill: the pane's processes, the pane, the run killed, the task blocked.
func (r *Runner) stop(ctx context.Context, h Herdr, run model.Run, panes []herdr.Pane, msg string) {
	if what := r.killPane(ctx, h, run, panes); what != "" {
		r.o.Logf("desk runner: T%d run %d: %s", run.Task, run.ID, what)
	}
	r.handBack(ctx, run, model.RunRunning, model.RunKilled, model.StatusBlocked, []string{model.TagRunner}, msg)
}

// killPane signals the processes of the run's pane with TERM, then KILL after the grace, and closes the pane. It
// returns what it did, for a note.
func (r *Runner) killPane(ctx context.Context, h Herdr, run model.Run, panes []herdr.Pane) string {
	pane, _ := findPane(panes, run)
	if pane == nil {
		return "no pane was open"
	}
	procs, err := h.Processes(ctx, pane.ID)
	if err != nil {
		r.logErr("T%d: read the processes of pane %s", run.Task, pane.ID, err)
	}
	targets := signalTargets(procs)
	signalAll(targets, syscall.SIGTERM)
	deadline := time.Now().Add(r.o.KillGrace)
	for len(alive(targets)) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	left := alive(targets)
	signalAll(left, syscall.SIGKILL)
	what := fmt.Sprintf("pane %s closed", pane.ID)
	if err := h.ClosePane(ctx, pane.ID); err != nil {
		r.logErr("T%d: close pane %s", run.Task, pane.ID, err)
		what = fmt.Sprintf("pane %s did not close", pane.ID)
	}
	if len(left) > 0 {
		what += ", killed after the grace"
	}
	return what
}

// signalTargets returns what kill(2) is given for the pane's processes: the foreground group as a negative id,
// then each pid. It never holds 0, 1, this process, or this process's group.
func signalTargets(p herdr.Processes) []int {
	self, group := os.Getpid(), syscall.Getpgrp()
	var out []int
	if p.Group > 1 && p.Group != group && p.Group != self {
		out = append(out, -p.Group)
	}
	for _, pid := range p.PIDs {
		if pid > 1 && pid != self && pid != group && !slices.Contains(out, pid) {
			out = append(out, pid)
		}
	}
	return out
}

func signalAll(targets []int, sig syscall.Signal) {
	for _, t := range targets {
		_ = syscall.Kill(t, sig)
	}
}

// alive returns the targets that still name a process.
func alive(targets []int) []int {
	var out []int
	for _, t := range targets {
		if err := syscall.Kill(t, 0); err == nil || errors.Is(err, syscall.EPERM) {
			out = append(out, t)
		}
	}
	return out
}
