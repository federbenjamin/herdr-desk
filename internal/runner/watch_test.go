package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/herdr/herdrtest"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/runner"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
)

type watchFixture struct {
	t     *testing.T
	ctx   context.Context
	paths config.Paths
	store *store.Store
	cfg   config.Config
	herdr *herdrtest.Herdr
	now   time.Time
	logs  []string
}

func newWatchFixture(t *testing.T) *watchFixture {
	t.Helper()
	machine := testutil.NewMachine(t)
	now := time.Date(2026, time.October, 4, 15, 0, 0, 0, time.Local)
	st, err := store.Open(machine.Paths.DB(), store.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	f := &watchFixture{
		t: t, ctx: context.Background(), paths: machine.Paths, store: st, herdr: herdrtest.NewHerdr(), now: now,
	}
	t.Cleanup(func() { _ = st.Close() })
	f.cfg = config.Default()
	f.cfg.Runner.Enabled = true
	f.cfg.Runner.Cap = 3
	f.cfg.Runner.MaxRunsPerDay = 20
	f.cfg.Runner.MaxRunMinutes = 10
	f.cfg.Roots = []config.Root{{Path: t.TempDir(), About: "watch root", Isolation: "in-place"}}
	f.cfg.Agent.Models = []string{"model-a"}
	f.cfg.Agent.Router = []string{f.routerScript()}
	return f
}

func (f *watchFixture) routerScript() string {
	f.t.Helper()
	path := filepath.Join(f.t.TempDir(), "router")
	text := fmt.Sprintf("#!/bin/sh\nprintf '%s\\n' '%s'\n", "%s", `{"root":"`+f.cfg.Roots[0].Path+`","isolation":"in-place","model":"model-a","reason":"test"}`)
	if err := os.WriteFile(path, []byte(text), 0o700); err != nil {
		f.t.Fatalf("write router: %v", err)
	}
	return path
}

func (f *watchFixture) runner() *runner.Runner {
	return f.runnerWithHerdr(f.herdr)
}

func (f *watchFixture) runnerWithHerdr(h runner.Herdr) *runner.Runner {
	f.t.Helper()
	return runner.New(runner.Options{
		Store: f.store, Config: f.cfg, Paths: f.paths, Herdr: h, Exe: "/opt/desk/bin/desk",
		Now: func() time.Time { return f.now }, RouterTimeout: 100 * time.Millisecond, KillGrace: 10 * time.Millisecond,
		Logf: func(format string, args ...any) { f.logs = append(f.logs, fmt.Sprintf(format, args...)) },
	})
}

func (f *watchFixture) start() (model.Task, model.Run, *runner.Runner) {
	f.t.Helper()
	task, err := f.store.AddTask(f.ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "watch me", Thread: "agent"}})
	if err != nil {
		f.t.Fatalf("add task: %v", err)
	}
	ready := model.StatusReady
	root, isolation, modelName := f.cfg.Roots[0].Path, "in-place", "model-a"
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &ready, Root: &root, Isolation: &isolation, Model: &modelName}); err != nil {
		f.t.Fatalf("arm task: %v", err)
	}
	r := f.runner()
	r.Tick(f.ctx)
	run := f.run(task.Number)
	if run.State != model.RunRunning || run.Session == "" || run.Pane == "" {
		f.t.Fatalf("started run = %#v, want running run with session and pane", run)
	}
	return task, run, r
}

func (f *watchFixture) run(task int) model.Run {
	f.t.Helper()
	runs, err := f.store.ListRuns(f.ctx)
	if err != nil {
		f.t.Fatalf("list runs: %v", err)
	}
	for _, run := range runs {
		if run.Task == task {
			return run
		}
	}
	f.t.Fatalf("no run for T%d in %#v", task, runs)
	return model.Run{}
}

func (f *watchFixture) task(number int) store.TaskDetail {
	f.t.Helper()
	detail, err := f.store.GetTask(f.ctx, number)
	if err != nil {
		f.t.Fatalf("get T%d: %v", number, err)
	}
	return detail
}

func TestTickBlocksAReportedBlockedSession(t *testing.T) {
	f := newWatchFixture(t)
	task, run, r := f.start()
	f.herdr.Set(run.Pane, run.Session, "blocked")

	r.Tick(f.ctx)
	detail := f.task(task.Number)
	if detail.Task.Status != model.StatusBlocked || f.run(task.Number).State != model.RunEnded {
		t.Fatalf("task = %#v; run = %#v, want blocked task and ended run", detail.Task, f.run(task.Number))
	}
	watchAssertRunnerNote(t, detail.History, run.ID, "")
	watchAssertRunnerStatus(t, detail.History, run.ID, model.StatusBlocked)
}

func TestTickReviewsDoneOrIdleSessionsOnlyAfterTwoConsecutiveTicks(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      string
		workerWrote bool
		wantNote    string
	}{
		{name: "done without hand-back", status: "done", wantNote: "session ended without reporting"},
		{name: "idle after worker wrote", status: "idle", workerWrote: true, wantNote: "session went idle without handing back"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWatchFixture(t)
			task, run, r := f.start()
			if tc.workerWrote {
				if _, err := f.store.Note(f.ctx, store.Actor{Session: run.Session, Run: run.ID}, store.NoteInput{Task: task.Number, NoteData: model.NoteData{Text: "worker progress"}}); err != nil {
					t.Fatalf("write worker event: %v", err)
				}
			}
			f.herdr.Set(run.Pane, run.Session, tc.status)
			r.Tick(f.ctx)
			if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
				t.Fatalf("task after first %s tick = %q, want started", tc.status, got)
			}
			r.Tick(f.ctx)
			detail := f.task(task.Number)
			if detail.Task.Status != model.StatusReview || f.run(task.Number).State != model.RunEnded {
				t.Fatalf("task = %#v; run = %#v, want review task and ended run", detail.Task, f.run(task.Number))
			}
			watchAssertRunnerNote(t, detail.History, run.ID, tc.wantNote)
			watchAssertRunnerStatus(t, detail.History, run.ID, model.StatusReview)
		})
	}
}

func TestTickResetsTheIdleCountWhenTheSessionWorksOrIsUnknown(t *testing.T) {
	for _, reset := range []string{"working", "unknown"} {
		t.Run(reset, func(t *testing.T) {
			f := newWatchFixture(t)
			task, run, r := f.start()
			f.herdr.Set(run.Pane, run.Session, "idle")
			r.Tick(f.ctx)
			f.herdr.Set(run.Pane, run.Session, reset)
			r.Tick(f.ctx)
			f.herdr.Set(run.Pane, run.Session, "idle")
			r.Tick(f.ctx)
			if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
				t.Fatalf("task after idle, %s, idle = %q, want started", reset, got)
			}
			r.Tick(f.ctx)
			if got := f.task(task.Number).Task.Status; got != model.StatusReview {
				t.Fatalf("task after second post-reset idle = %q, want review", got)
			}
		})
	}
}

func TestTickDoesNotCountIdleForAPaneWithoutTheRunSession(t *testing.T) {
	f := newWatchFixture(t)
	task, run, r := f.start()
	f.herdr.Set(run.Pane, "", "idle")
	r.Tick(f.ctx)
	r.Tick(f.ctx)
	if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
		t.Fatalf("task found only by pane id = %q, want started", got)
	}
}

func TestTickReviewsWhenThePaneClosesWithoutAHandBack(t *testing.T) {
	f := newWatchFixture(t)
	task, run, r := f.start()
	f.herdr.Remove(run.Pane)
	r.Tick(f.ctx)
	detail := f.task(task.Number)
	if detail.Task.Status != model.StatusReview || f.run(task.Number).State != model.RunEnded {
		t.Fatalf("task = %#v; run = %#v, want review task and ended run", detail.Task, f.run(task.Number))
	}
	watchAssertRunnerNote(t, detail.History, run.ID, "the pane closed without a hand-back")
	watchAssertRunnerStatus(t, detail.History, run.ID, model.StatusReview)
}

func TestTickLeavesRunsAloneAfterPanesFailureAndRecoversOnTheNextTick(t *testing.T) {
	f := newWatchFixture(t)
	task, run, r := f.start()
	f.herdr.Set(run.Pane, run.Session, "blocked")
	f.herdr.Fail("Panes", errors.New("list failed"))
	before := f.task(task.Number)
	beforeRun := f.run(task.Number)
	r.Tick(f.ctx)
	if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
		t.Fatalf("task after Panes failure = %q, want started", got)
	}
	if got := f.run(task.Number); !reflect.DeepEqual(got, beforeRun) {
		t.Fatalf("run after Panes failure = %#v, want unchanged %#v", got, beforeRun)
	}
	if got := f.task(task.Number).History; !reflect.DeepEqual(got, before.History) {
		t.Fatalf("history after Panes failure = %#v, want unchanged %#v", got, before.History)
	}
	if got := strings.Join(f.logs, "\n"); !strings.Contains(got, "list failed") {
		t.Fatalf("watch logs = %q, want Panes failure", got)
	}
	f.herdr.Fail("Panes", nil)
	r.Tick(f.ctx)
	if got := f.task(task.Number).Task.Status; got != model.StatusBlocked {
		t.Fatalf("task after recovered Panes call = %q, want blocked", got)
	}
}

func TestTickDoesNotListPanesWithoutALiveRunAndStillStartsAnArmedTask(t *testing.T) {
	f := newWatchFixture(t)
	f.herdr.Fail("Panes", errors.New("must not be called"))
	task, err := f.store.AddTask(f.ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "start without watch", Thread: "agent"}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	ready := model.StatusReady
	root, isolation, modelName := f.cfg.Roots[0].Path, "in-place", "model-a"
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &ready, Root: &root, Isolation: &isolation, Model: &modelName}); err != nil {
		t.Fatalf("arm task: %v", err)
	}
	f.runner().Tick(f.ctx)
	if got := f.run(task.Number).State; got != model.RunRunning {
		t.Fatalf("run after tick with no prior live runs = %q, want running", got)
	}
	if got := strings.Join(f.logs, "\n"); strings.Contains(got, "must not be called") {
		t.Fatalf("watch called Panes without a live run: %q", got)
	}
}

func TestTickWatchesLiveRunsWhenTheRunnerIsDisabled(t *testing.T) {
	f := newWatchFixture(t)
	task, run, _ := f.start()
	f.herdr.Set(run.Pane, run.Session, "done")
	f.cfg.Runner.Enabled = false
	off := f.runner()
	off.Tick(f.ctx)
	off.Tick(f.ctx)
	if got := f.task(task.Number).Task.Status; got != model.StatusReview {
		t.Fatalf("task watched while disabled = %q, want review", got)
	}
}

func TestTickDoesNothingAfterTheWorkerHandsTheTaskBack(t *testing.T) {
	f := newWatchFixture(t)
	task, run, r := f.start()
	review := model.StatusReview
	if _, err := f.store.SetTask(f.ctx, store.Actor{Session: run.Session, Run: run.ID}, task.Number, model.Patch{Status: &review}); err != nil {
		t.Fatalf("worker hand-back: %v", err)
	}
	before := f.task(task.Number)
	r.Tick(f.ctx)
	after := f.task(task.Number)
	if f.run(task.Number).State != model.RunEnded || !reflect.DeepEqual(after.History, before.History) || after.Task.Status != model.StatusReview {
		t.Fatalf("task after hand-back and tick = %#v; run = %#v, want unchanged review task and ended run", after, f.run(task.Number))
	}
}

func TestTickStopsRunsPastTheConfiguredTimeLimit(t *testing.T) {
	f := newWatchFixture(t)
	f.cfg.Runner.MaxRunMinutes = 1
	task, run, r := f.start()
	f.herdr.Set(run.Pane, run.Session, "working")
	f.now = f.now.Add(2 * time.Minute)
	r.Tick(f.ctx)
	detail := f.task(task.Number)
	if detail.Task.Status != model.StatusBlocked || f.run(task.Number).State != model.RunKilled || !reflect.DeepEqual(f.herdr.Closed(), []string{run.Pane}) {
		t.Fatalf("task = %#v; run = %#v; closed = %#v, want blocked killed run and closed pane", detail.Task, f.run(task.Number), f.herdr.Closed())
	}
	watchAssertRunnerNote(t, detail.History, run.ID, "stopped after 1 minutes (runner.max_run_minutes)")
	watchAssertRunnerStatus(t, detail.History, run.ID, model.StatusBlocked)
}

func watchAssertRunnerNote(t *testing.T, history []model.Event, run int64, want string) {
	t.Helper()
	for _, event := range history {
		var note model.NoteData
		if event.Kind == model.KindNote && event.Run == run && event.Session == "" && reflect.DeepEqual(event.Tags, []string{model.TagRunner}) && json.Unmarshal(event.Data, &note) == nil && (want == "" || note.Text == want) {
			return
		}
	}
	if want == "" {
		t.Fatalf("history = %#v, want a runner note carrying run %d and no session", history, run)
	}
	t.Fatalf("history = %#v, want runner note %q carrying run %d and no session", history, want, run)
}

func watchAssertRunnerStatus(t *testing.T, history []model.Event, run int64, want model.Status) {
	t.Helper()
	for _, event := range history {
		var patch model.Patch
		if event.Kind == model.KindSet && event.Run == run && event.Session == "" && json.Unmarshal(event.Data, &patch) == nil && patch.Status != nil && *patch.Status == want {
			return
		}
	}
	t.Fatalf("history = %#v, want %s status write carrying run %d and no session", history, want, run)
}
