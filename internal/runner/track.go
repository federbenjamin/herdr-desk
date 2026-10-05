package runner

import (
	"context"
	"errors"
	"fmt"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// Track applies the outcome table to the live run on pane, from what herdr says of the pane now: herdr's event is
// only the signal to look. A pane no live run owns is nothing to do, and herdr is not asked.
func (r *Runner) Track(ctx context.Context, pane string) error {
	run, ok, err := r.o.Store.LiveRunOnPane(ctx, pane)
	if err != nil || !ok {
		return err
	}
	h, err := r.findHerdr()
	if err != nil {
		return fmt.Errorf("find herdr: %w", err)
	}
	p, found, err := h.Pane(ctx, pane)
	if err != nil {
		return err
	}
	return r.track(ctx, run, p, found && p.Workspace == run.Workspace)
}

// Reconcile applies the outcome table to every running and idle run with one `herdr pane list`, for the events herdr
// never delivered. A run whose pane herdr no longer lists is gone. With no such run, herdr is not asked. Each error
// is logged as well as returned.
func (r *Runner) Reconcile(ctx context.Context) error {
	live, err := r.o.Store.LiveRuns(ctx)
	if err != nil {
		r.logErr("read live runs", err)
		return err
	}
	var tracked []model.Run
	for _, run := range live {
		if run.State == model.RunRunning || run.State == model.RunIdle {
			tracked = append(tracked, run)
		}
	}
	if len(tracked) == 0 {
		return nil
	}
	h, err := r.findHerdr()
	if err != nil {
		r.logErr("find herdr", err)
		return fmt.Errorf("find herdr: %w", err)
	}
	panes, err := h.Panes(ctx)
	if err != nil {
		r.logErr("list herdr panes", err)
		return err
	}
	var errs []error
	for _, run := range tracked {
		var p herdr.Pane
		found := false
		for _, q := range panes {
			if q.ID == run.Pane && q.Workspace == run.Workspace {
				p, found = q, true
				break
			}
		}
		errs = append(errs, r.track(ctx, run, p, found))
	}
	return errors.Join(errs...)
}

// track writes the outcome of the run's pane through the one hand-back. Whether the worker wrote anything is read
// only for a pane that is gone, the one row that asks.
func (r *Runner) track(ctx context.Context, run model.Run, pane herdr.Pane, found bool) error {
	wrote := false
	if !found {
		var err error
		if wrote, err = r.o.Store.RunWrote(ctx, run.ID); err != nil {
			r.logErr("T%d run %d: read whether its worker wrote", run.Task, run.ID, err)
			return err
		}
	}
	hb, ok := outcome(run, pane, found, wrote)
	if !ok {
		return nil
	}
	_, _, err := r.handBack(ctx, run, hb, nil)
	return err
}

// outcome is the one outcome table: the hand-back that the pane's state now asks of a running or idle run, and
// false when it asks none. A row whose run state is the run's own is no write, so a repeated event changes nothing.
func outcome(run model.Run, pane herdr.Pane, found, wrote bool) (store.HandBack, bool) {
	if run.State != model.RunRunning && run.State != model.RunIdle {
		return store.HandBack{}, false
	}
	own := run.Session != "" && pane.Session == run.Session
	hb := store.HandBack{From: run.State, Tags: []string{model.TagRunner}}
	switch {
	case !found:
		hb.To, hb.Status, hb.IfStatus = model.RunEnded, model.StatusReview, model.StatusStarted
		hb.Note = "the pane closed without a hand-back"
		if !wrote {
			hb.Note += ", before the worker wrote anything"
		}
	case pane.Status == "blocked" && (own || pane.Session == ""):
		hb.To, hb.Status = model.RunIdle, model.StatusBlocked
		hb.Note = "the worker is waiting for an answer in pane " + pane.ID
	case (pane.Status == "idle" || pane.Status == "done") && own && run.State == model.RunRunning:
		hb.To, hb.Status = model.RunIdle, model.StatusReview
		hb.Note = "went idle without handing back"
	case pane.Status == "working" && own && run.State == model.RunIdle:
		hb.To, hb.Status = model.RunRunning, model.StatusStarted
	default:
		return store.HandBack{}, false
	}
	if hb.To == run.State {
		return store.HandBack{}, false
	}
	return hb, true
}
