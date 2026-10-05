package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
)

// watch looks at the pane of every running run once and hands the task back when its worker stopped, asked, or
// ran past runner.max_run_minutes. It also closes again each pane the runner failed to close.
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
	left := r.leftOpen()
	if len(running) == 0 && len(left) == 0 {
		return
	}
	panes, err := h.Panes(ctx)
	if err != nil {
		r.logErr("list herdr panes", err)
		return
	}
	r.closeAgain(ctx, h, panes, left)
	limit := time.Duration(r.o.Config.Runner.MaxRunMinutes) * time.Minute
	tags := []string{model.TagRunner}
	toReview := flip{from: model.RunRunning, to: model.RunEnded, status: model.StatusReview, tags: tags}
	for _, run := range running {
		if r.o.Now().Sub(run.StartedTS) > limit {
			r.stop(ctx, h, run, panes, fmt.Sprintf("stopped after %d minutes (runner.max_run_minutes)", r.o.Config.Runner.MaxRunMinutes))
			continue
		}
		pane, bySession := findPane(panes, run)
		switch {
		case pane == nil:
			toReview.note = "the pane closed without a hand-back"
			if !r.wrote(ctx, run) {
				toReview.note += ", before the worker wrote anything"
			}
			r.handBack(ctx, run, toReview)
		case pane.Status == "blocked":
			// An agent can stop at a question before it has a session (claude's trust question), so blocked counts
			// on a pane found only by id too.
			r.handBack(ctx, run, flip{from: model.RunRunning, to: model.RunEnded, status: model.StatusBlocked, tags: tags,
				note: fmt.Sprintf("the worker is blocked waiting for an answer in pane %s", pane.ID)})
		case !bySession:
			// The pane has not started its agent yet; only the time limit stops it.
		case pane.Status == "done" || pane.Status == "idle":
			r.idle[run.ID]++
			if r.idle[run.ID] < 2 {
				continue
			}
			toReview.note = "session went idle without handing back"
			if !r.wrote(ctx, run) {
				toReview.note = "session ended without reporting"
			}
			r.handBack(ctx, run, toReview)
		default:
			delete(r.idle, run.ID)
		}
	}
}

// wrote reports whether the run's worker wrote anything; a store error reads as true, the note that claims less.
func (r *Runner) wrote(ctx context.Context, run model.Run) bool {
	wrote, err := r.o.Store.RunWrote(ctx, run.ID)
	if err != nil {
		r.logErr("T%d run %d: read what the worker wrote", run.Task, run.ID, err)
		return true
	}
	return wrote
}

// closeAgain closes each pane left open that herdr still lists, and forgets the ones it no longer lists.
func (r *Runner) closeAgain(ctx context.Context, h Herdr, panes []herdr.Pane, left []openPane) {
	for _, o := range left {
		if !listed(panes, o.pane) {
			r.forgetOpen(o.pane.ID)
			continue
		}
		k := r.closePane(ctx, h, o.task, o.pane)
		r.o.Logf("herdr-desk runner: T%d: closing pane %s again: %s", o.task, o.pane.ID, k)
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
