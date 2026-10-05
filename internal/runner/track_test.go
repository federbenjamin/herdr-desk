package runner_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/herdr/herdrtest"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestTrackAppliesPaneOutcomesAndARepeatWritesNothing(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		wantRun  string
		wantTask model.Status
		wantNote string
	}{
		{"a blocked pane becomes idle and blocks its task", "blocked", model.RunIdle, model.StatusBlocked, "waiting for an answer"},
		{"an owned idle pane becomes review", "idle", model.RunIdle, model.StatusReview, "went idle without handing back"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "", "self")
			task, run, r := f.start()
			f.herdr.Set(run.Pane, run.Session, tc.status)

			if err := r.Track(f.ctx, run.Pane); err != nil {
				t.Fatalf("Track() error = %v", err)
			}
			got := f.run(task.Number)
			if got.State != tc.wantRun || f.task(task.Number).Task.Status != tc.wantTask {
				t.Fatalf("after Track, run = %#v; task = %#v, want %s and %s", got, f.task(task.Number).Task, tc.wantRun, tc.wantTask)
			}
			detail := f.task(task.Number)
			if !trackHasNote(detail.History, tc.wantNote) {
				t.Fatalf("history = %#v, want a runner note containing %q", detail.History, tc.wantNote)
			}
			before := len(detail.History)
			if err := r.Track(f.ctx, run.Pane); err != nil {
				t.Fatalf("repeat Track() error = %v", err)
			}
			if again := f.run(task.Number); again.State != tc.wantRun || len(f.task(task.Number).History) != before {
				t.Fatalf("repeat Track changed run to %#v or history from %d events", again, before)
			}
		})
	}
}

func TestTrackLeavesAnOlderRunAloneWhenANewerRunWinsTheTask(t *testing.T) {
	f := newFixture(t, "", "self")
	task, old, _ := f.start()
	race := &trackRaceHerdr{Herdr: f.herdr}
	race.beforePane = func() {
		if changed, err := f.store.UpdateRun(f.ctx, old.ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !changed {
			race.err = fmt.Errorf("end first run = (%t, %v)", changed, err)
			return
		}
		_, race.err = f.store.StartRun(f.ctx, task.Number, store.RunRoute{Root: f.root, Isolation: "self", Model: "model-a"}, store.RunCaps{Slots: f.config.Runner.Cap, PerDay: 1000})
	}
	r := f.runnerWith(race)
	f.herdr.Set(old.Pane, old.Session, "idle")

	if err := r.Track(f.ctx, old.Pane); err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	if race.err != nil {
		t.Fatal(race.err)
	}
	runs := f.runs()
	if len(runs) != 2 || runs[0].State != model.RunEnded || runs[1].State != model.RunStarting {
		t.Fatalf("runs after the race = %#v, want the old run ended and the newer run starting", runs)
	}
	if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
		t.Fatalf("task after the stale event = %s, want started for the newer run", got)
	}
}

func TestJobsReconcilesBeforeItsOtherWorkAndSkipsHerdrWithoutRuns(t *testing.T) {
	t.Run("reconciles a live pane", func(t *testing.T) {
		f := newFixture(t, "", "self")
		task, run, r := f.start()
		f.herdr.Set(run.Pane, run.Session, "idle")

		r.Jobs(f.ctx)
		if got := f.run(task.Number); got.State != model.RunIdle || f.task(task.Number).Task.Status != model.StatusReview {
			t.Fatalf("Jobs() left run = %#v and task = %#v, want idle and review", got, f.task(task.Number).Task)
		}
	})

	t.Run("does not poll without a live or left-open run", func(t *testing.T) {
		f := newFixture(t, "", "self")
		counting := &countingHerdr{Herdr: f.herdr}
		f.runnerWith(counting).Jobs(context.Background())
		if counting.panes != 0 || counting.pane != 0 {
			t.Fatalf("Jobs() made %d pane-list and %d pane-get calls with no runs", counting.panes, counting.pane)
		}
	})
}

func trackHasNote(history []model.Event, want string) bool {
	for _, event := range history {
		if strings.Contains(string(event.Data), want) {
			return true
		}
	}
	return false
}

type trackRaceHerdr struct {
	*herdrtest.Herdr
	beforePane func()
	err        error
}

func (h *trackRaceHerdr) Pane(ctx context.Context, id string) (herdr.Pane, bool, error) {
	if h.beforePane != nil {
		h.beforePane()
		h.beforePane = nil
	}
	return h.Herdr.Pane(ctx, id)
}

type countingHerdr struct {
	*herdrtest.Herdr
	panes int
	pane  int
}

func (h *countingHerdr) Panes(ctx context.Context) ([]herdr.Pane, error) {
	h.panes++
	return h.Herdr.Panes(ctx)
}

func (h *countingHerdr) Pane(ctx context.Context, id string) (herdr.Pane, bool, error) {
	h.pane++
	return h.Herdr.Pane(ctx, id)
}

var _ runner.Herdr = (*trackRaceHerdr)(nil)
var _ runner.Herdr = (*countingHerdr)(nil)
