package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// killTries bounds Kill's retries when another process moves the run on between Kill's read and its write.
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
		hb := store.HandBack{From: run.State, To: model.RunKilled, Status: model.StatusBlocked, Tags: []string{model.TagRunner},
			Note: fmt.Sprintf("run %d killed while %s", run.ID, run.State)}
		var cleanup func(ctx context.Context) string
		var k paneKill
		if run.Pane != "" {
			h, why := r.findHerdr()
			if why != nil {
				return model.Task{}, fmt.Errorf("herdr cannot be reached, so the pane cannot be closed: %w", why)
			}
			panes, err := h.Panes(ctx)
			if err != nil {
				return model.Task{}, err
			}
			cleanup = func(ctx context.Context) string {
				k = r.killPane(ctx, h, run, panes)
				return fmt.Sprintf("run %d killed: %s", run.ID, k)
			}
		}
		t, claimed, err := r.handBack(ctx, run, hb, cleanup)
		if err != nil {
			return t, err
		}
		if !claimed {
			continue
		}
		if k.alive {
			return t, fmt.Errorf("T%d is blocked, but %s", task, strings.Join(k.problems, "; "))
		}
		return t, nil
	}
	return model.Task{}, fmt.Errorf("T%d's run kept changing state; try again", task)
}

// stop is the time limit's kill: the run killed, the pane's processes, the pane, the task blocked.
func (r *Runner) stop(ctx context.Context, h Herdr, run model.Run, panes []herdr.Pane, msg string) {
	r.handBack(ctx, run, store.HandBack{From: model.RunRunning, To: model.RunKilled, Status: model.StatusBlocked,
		Tags: []string{model.TagRunner}}, func(ctx context.Context) string {
		k := r.killPane(ctx, h, run, panes)
		return strings.Join(append([]string{msg}, k.problems...), "; ")
	})
}

// paneKill is what killing a pane did, for a note.
type paneKill struct {
	done     string   // "pane P closed", "no pane was open", or "" when the pane did not close
	problems []string // what could not be done, each a clause
	alive    bool     // the pane is still open, one of its processes survived, or they could not be read to be signalled
}

func (k paneKill) String() string {
	parts := k.problems
	if k.done != "" {
		parts = append([]string{k.done}, parts...)
	}
	return strings.Join(parts, "; ")
}

// killPane kills the run's pane, found by findPane.
func (r *Runner) killPane(ctx context.Context, h Herdr, run model.Run, panes []herdr.Pane) paneKill {
	pane, _ := findPane(panes, run)
	if pane == nil {
		return paneKill{done: "no pane was open"}
	}
	return r.closePane(ctx, h, run, *pane)
}

// closePane signals the pane's processes with TERM, then KILL after the grace, and closes the pane. A pane that
// does not close is recorded on the run (close), and Jobs closes it again until herdr no longer lists it.
func (r *Runner) closePane(ctx context.Context, h Herdr, run model.Run, pane herdr.Pane) paneKill {
	var k paneKill
	procs, err := h.Processes(ctx, pane.ID)
	if err != nil {
		r.logErr("T%d: read the processes of pane %s", run.Task, pane.ID, err)
		k.problems = append(k.problems, "its processes could not be read")
		k.alive = true
	}
	targets := signalTargets(procs)
	signalAll(targets, syscall.SIGTERM)
	left := waitGone(targets, r.o.KillGrace)
	signalAll(left, syscall.SIGKILL)
	if r.close(ctx, h, run, pane) {
		k.done = fmt.Sprintf("pane %s closed", pane.ID)
	} else {
		k.problems = append(k.problems, fmt.Sprintf("pane %s did not close", pane.ID))
		k.alive = true
	}
	survived := waitGone(left, r.o.KillGrace)
	for _, t := range survived {
		if t < 0 {
			k.problems = append(k.problems, fmt.Sprintf("process group %d survived the kill", -t))
		} else {
			k.problems = append(k.problems, fmt.Sprintf("pid %d survived the kill", t))
		}
		k.alive = true
	}
	if len(left) > 0 && len(survived) == 0 && k.done != "" {
		k.done += ", killed after the grace"
	}
	return k
}

// close closes the run's pane and reports whether it is gone. A close that fails on a pane herdr no longer lists
// is gone too: its processes ended and took it. A pane that may still be open is recorded on the run (keepOpen).
func (r *Runner) close(ctx context.Context, h Herdr, run model.Run, pane herdr.Pane) bool {
	err := h.ClosePane(ctx, pane.ID)
	if err != nil {
		if panes, lerr := h.Panes(ctx); lerr == nil {
			if _, ok := paneByID(panes, pane); !ok {
				err = nil
			}
		}
	}
	if err != nil {
		r.logErr("T%d: close pane %s", run.Task, pane.ID, err)
		r.keepOpen(ctx, run, pane)
		return false
	}
	return true
}

// keepOpen records on the run, in the state it is in now, that its pane did not close, so Jobs closes it again.
// A run that is no longer its task's newest is only logged.
func (r *Runner) keepOpen(ctx context.Context, run model.Run, pane herdr.Pane) {
	if run.LeftOpen {
		return
	}
	open := true
	cur, ok, err := r.o.Store.CurrentRun(ctx, run.Task)
	if err == nil && ok && cur.ID == run.ID {
		ok, err = r.o.Store.UpdateRun(ctx, run.ID, cur.State, store.RunUpdate{LeftOpen: &open, Workspace: pane.Workspace, Pane: pane.ID})
	} else if err == nil {
		ok, err = false, errors.New("a newer run owns the task")
	}
	if err == nil && !ok {
		err = errors.New("the run changed state")
	}
	if err != nil {
		r.logErr("T%d run %d: record that pane %s is still open", run.Task, run.ID, pane.ID, err)
	}
}

// paneByID is the one rule for which listed pane is a given one: the same id in the same workspace (herdr may reuse
// an id in another workspace). It returns that pane as herdr lists it; false when herdr does not list it.
func paneByID(panes []herdr.Pane, pane herdr.Pane) (herdr.Pane, bool) {
	for _, p := range panes {
		if p.ID == pane.ID && p.Workspace == pane.Workspace {
			return p, true
		}
	}
	return herdr.Pane{}, false
}

// runPane is the pane the run row names.
func runPane(run model.Run) herdr.Pane { return herdr.Pane{ID: run.Pane, Workspace: run.Workspace} }

// findPane returns the run's pane: the one whose agent session is the run's, else the one paneByID finds for the run
// row's pane. bySession reports which; nil is no pane.
func findPane(panes []herdr.Pane, run model.Run) (pane *herdr.Pane, bySession bool) {
	if run.Session != "" {
		for i := range panes {
			if panes[i].Session == run.Session {
				return &panes[i], true
			}
		}
	}
	if run.Pane != "" {
		if p, ok := paneByID(panes, runPane(run)); ok {
			return &p, false
		}
	}
	return nil, false
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

// waitGone waits up to grace for the targets to exit and returns the ones still there.
func waitGone(targets []int, grace time.Duration) []int {
	deadline := time.Now().Add(grace)
	for len(alive(targets)) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	return alive(targets)
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
