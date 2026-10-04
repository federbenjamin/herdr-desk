package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/federbenjamin/desk/internal/herdr"
	"github.com/federbenjamin/desk/internal/model"
)

// watch looks at the pane of every running run once and hands the task back when its worker stopped, asked, or
// ran past runner.max_run_minutes.
func (r *Runner) watch(ctx context.Context, h Herdr) {
	live, err := r.o.Store.LiveRuns(ctx)
	if err != nil {
		r.logErr("read live runs", err)
		return
	}
	var running []model.Run
	seen := map[int64]bool{}
	for _, run := range live {
		if run.State == model.RunRunning {
			running = append(running, run)
			seen[run.ID] = true
		}
	}
	for id := range r.idle {
		if !seen[id] {
			delete(r.idle, id)
		}
	}
	if len(running) == 0 {
		return
	}
	panes, err := h.Panes(ctx)
	if err != nil {
		r.logErr("list herdr panes", err)
		return
	}
	limit := time.Duration(r.o.Config.Runner.MaxRunMinutes) * time.Minute
	tags := []string{model.TagRunner}
	for _, run := range running {
		if r.o.Now().Sub(run.StartedTS) > limit {
			r.stop(ctx, h, run, panes, fmt.Sprintf("stopped after %d minutes (runner.max_run_minutes)", r.o.Config.Runner.MaxRunMinutes))
			continue
		}
		pane, bySession := findPane(panes, run)
		switch {
		case pane == nil:
			r.handBack(ctx, run, model.RunRunning, model.RunEnded, model.StatusReview, tags, "the pane closed without a hand-back")
		case !bySession:
			// The pane has not started its agent yet; only the time limit stops it.
		case pane.Status == "blocked":
			r.handBack(ctx, run, model.RunRunning, model.RunEnded, model.StatusBlocked, tags,
				fmt.Sprintf("the worker is blocked waiting for an answer in pane %s", pane.ID))
		case pane.Status == "done" || pane.Status == "idle":
			r.idle[run.ID]++
			if r.idle[run.ID] < 2 {
				continue
			}
			msg := "session went idle without handing back"
			wrote, err := r.o.Store.RunWrote(ctx, run.ID)
			if err != nil {
				r.logErr("T%d run %d: read what the worker wrote", run.Task, run.ID, err)
			}
			if err == nil && !wrote {
				msg = "session ended without reporting"
			}
			r.handBack(ctx, run, model.RunRunning, model.RunEnded, model.StatusReview, tags, msg)
		default:
			delete(r.idle, run.ID)
		}
	}
}

// findPane returns the run's pane: the one whose agent session is the run's, else the one whose id and workspace
// are the run's. bySession reports which; nil is no pane.
func findPane(panes []herdr.Pane, run model.Run) (pane *herdr.Pane, bySession bool) {
	if run.Session != "" {
		for i := range panes {
			if panes[i].Session == run.Session {
				return &panes[i], true
			}
		}
	}
	if run.Pane != "" {
		for i := range panes {
			if panes[i].ID == run.Pane && panes[i].Workspace == run.Workspace {
				return &panes[i], false
			}
		}
	}
	return nil, false
}
