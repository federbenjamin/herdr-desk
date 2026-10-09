package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestStartRunCreatesAStartingRunAndStartsTheTask(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "ready task", Status: model.StatusReady,
	}})
	if err != nil {
		t.Fatalf("add ready task: %v", err)
	}

	run, err := startRunPolicyOnTask(t, st, task.Number)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if run.Task != task.Number || run.State != model.RunStarting || run.Root != policyRoute.Root {
		t.Errorf("started run = %#v, want task %d starting on %s", run, task.Number, policyRoute.Root)
	}
	detail, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("read started task: %v", err)
	}
	if detail.Task.Status != model.StatusStarted {
		t.Errorf("task status = %q, want started", detail.Task.Status)
	}
	if len(detail.History) != 2 {
		t.Fatalf("history length = %d, want task creation and start", len(detail.History))
	}
	start := detail.History[1]
	if start.Kind != model.KindSet || start.Run != run.ID || start.Session != "" {
		t.Errorf("start event = %#v, want set event with run %d and no session", start, run.ID)
	}
}

func TestStartRunAsksNoArmingAndAnswersALiveRunWithIt(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "open task"}})
	if err != nil {
		t.Fatalf("add open task: %v", err)
	}

	first, err := startRunPolicyOnTask(t, st, task.Number)
	if err != nil {
		t.Fatalf("StartRun(open task) error = %v, want a run", err)
	}
	again, err := startRunPolicyOnTask(t, st, task.Number)
	if !errors.Is(err, store.ErrRunLive) || again.ID != first.ID {
		t.Fatalf("second StartRun() = run %d, %v; want run %d and ErrRunLive", again.ID, err, first.ID)
	}
}

func TestUpdateRunChangesOnlyItsExpectedStateAndKeepsZeroFields(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	task, run := startRunPolicy(t, st, "update run")

	now = now.Add(time.Minute)
	changed, err := st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{
		State:     model.RunRunning,
		Reason:    "matched the repository",
		Session:   "session-a",
		Workspace: "/work/desk/.worktrees/task",
		Pane:      "pane-a",
	})
	if err != nil {
		t.Fatalf("update starting run: %v", err)
	}
	if !changed {
		t.Fatal("update starting run changed = false, want true")
	}

	now = now.Add(time.Minute)
	changed, err = st.UpdateRun(ctx, run.ID, model.RunRunning, store.RunUpdate{State: model.RunWaiting})
	if err != nil {
		t.Fatalf("update running run with zero fields: %v", err)
	}
	if !changed {
		t.Fatal("update running run with zero fields changed = false, want true")
	}

	now = now.Add(time.Minute)
	changed, err = st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{State: model.RunFailed})
	if err != nil {
		t.Fatalf("update already-running run: %v", err)
	}
	if changed {
		t.Fatal("update from an old state changed = true, want false")
	}
	got, ok, err := st.CurrentRun(ctx, task.Number)
	if err != nil {
		t.Fatalf("read current run: %v", err)
	}
	if !ok {
		t.Fatal("CurrentRun() found no run")
	}
	if got.State != model.RunWaiting || got.Root != run.Root || got.Isolation != run.Isolation || got.Model != run.Model || got.Reason != "matched the repository" || got.Session != "session-a" || got.Workspace != "/work/desk/.worktrees/task" || got.Pane != "pane-a" {
		t.Errorf("run after zero-field and old-state updates = %#v, want waiting with original fields", got)
	}
	if !got.EndedTS.IsZero() {
		t.Errorf("run ended at %v after old-state update, want zero", got.EndedTS)
	}
}

func TestUpdateRunTerminalStatesRecordTheStoreClock(t *testing.T) {
	for _, terminal := range []string{model.RunEnded, model.RunFailed, model.RunKilled} {
		t.Run(terminal, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
			st := openRunPolicyStore(t, &now, false)
			task, run := startRunPolicy(t, st, terminal+" run")

			now = now.Add(7 * time.Minute)
			changed, err := st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{State: terminal})
			if err != nil {
				t.Fatalf("set state %q: %v", terminal, err)
			}
			if !changed {
				t.Fatalf("set state %q changed = false, want true", terminal)
			}
			got, ok, err := st.CurrentRun(ctx, task.Number)
			if err != nil {
				t.Fatalf("read terminal run: %v", err)
			}
			if !ok {
				t.Fatal("CurrentRun() found no run")
			}
			if got.State != terminal {
				t.Errorf("state = %q, want %q", got.State, terminal)
			}
			if !got.EndedTS.Equal(now) {
				t.Errorf("ended_ts = %v, want exact store clock %v", got.EndedTS, now)
			}
		})
	}
}

func TestUpdateRunToRunningRestartsTheRunClock(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	task, run := startRunPolicy(t, st, "waits, then runs")

	now = now.Add(3 * time.Hour)
	if changed, err := st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{State: model.RunWaiting}); err != nil || !changed {
		t.Fatalf("set waiting = (%t, %v)", changed, err)
	}
	if got, _, _ := st.CurrentRun(ctx, task.Number); !got.StartedTS.Equal(run.StartedTS) {
		t.Fatalf("started_ts after waiting = %v, want the creation time %v", got.StartedTS, run.StartedTS)
	}
	now = now.Add(time.Hour)
	if changed, err := st.UpdateRun(ctx, run.ID, model.RunWaiting, store.RunUpdate{State: model.RunRunning}); err != nil || !changed {
		t.Fatalf("set running = (%t, %v)", changed, err)
	}
	spawned := now
	now = now.Add(time.Minute)
	if changed, err := st.UpdateRun(ctx, run.ID, model.RunRunning, store.RunUpdate{Pane: "pane-a"}); err != nil || !changed {
		t.Fatalf("record a pane = (%t, %v)", changed, err)
	}
	if got, _, _ := st.CurrentRun(ctx, task.Number); !got.StartedTS.Equal(spawned) {
		t.Fatalf("started_ts of a running run = %v, want the spawn time %v", got.StartedTS, spawned)
	}
}

func TestRunMethodsReturnTheStoreErrorOnceClosed(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(testutil.NewMachine(t).Paths.DB(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"StartRun": func() error {
			_, err := st.StartRun(ctx, store.Actor{}, 1, policyRoute, store.RunCaps{Slots: 1, PerDay: 1000})
			return err
		},
		"UpdateRun":  func() error { _, err := st.UpdateRun(ctx, 1, model.RunStarting, store.RunUpdate{}); return err },
		"ListRuns":   func() error { _, err := st.ListRuns(ctx); return err },
		"CurrentRun": func() error { _, _, err := st.CurrentRun(ctx, 1); return err },
		"RunsSince":  func() error { _, err := st.RunsSince(ctx, time.Time{}); return err },
		"RunWrote":   func() error { _, err := st.RunWrote(ctx, 1); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s on a closed store: error = nil, want the store's error", name)
		}
	}
}

func TestLiveRunsReturnsOnlyLiveStatesInIDOrder(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)

	_, starting := startRunPolicy(t, st, "starting")
	_, waiting := startRunPolicy(t, st, "waiting")
	_, running := startRunPolicy(t, st, "running")
	_, idle := startRunPolicy(t, st, "idle")
	_, ended := startRunPolicy(t, st, "ended")
	_, failed := startRunPolicy(t, st, "failed")
	_, killed := startRunPolicy(t, st, "killed")
	for _, update := range []struct {
		run   model.Run
		state string
	}{
		{waiting, model.RunWaiting},
		{running, model.RunRunning},
		{idle, model.RunIdle},
		{ended, model.RunEnded},
		{failed, model.RunFailed},
		{killed, model.RunKilled},
	} {
		changed, err := st.UpdateRun(ctx, update.run.ID, model.RunStarting, store.RunUpdate{State: update.state})
		if err != nil {
			t.Fatalf("set run %d %q: %v", update.run.ID, update.state, err)
		}
		if !changed {
			t.Fatalf("set run %d %q changed = false", update.run.ID, update.state)
		}
	}

	runs, err := st.LiveRuns(ctx)
	if err != nil {
		t.Fatalf("list live runs: %v", err)
	}
	if len(runs) != 4 {
		t.Fatalf("live run count = %d, want 4: %#v", len(runs), runs)
	}
	for i, want := range []struct {
		run   model.Run
		state string
	}{{starting, model.RunStarting}, {waiting, model.RunWaiting}, {running, model.RunRunning}, {idle, model.RunIdle}} {
		if runs[i].ID != want.run.ID || runs[i].State != want.state {
			t.Errorf("live run %d = run %d %s, want run %d %s", i, runs[i].ID, runs[i].State, want.run.ID, want.state)
		}
	}
}

func TestRunsSinceCountsStartsAtOrAfterTheBoundary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	startRunPolicy(t, st, "before boundary")

	boundary := now.Add(time.Hour)
	now = boundary
	startRunPolicy(t, st, "at boundary")
	now = now.Add(time.Minute)
	startRunPolicy(t, st, "after boundary")

	got, err := st.RunsSince(ctx, boundary)
	if err != nil {
		t.Fatalf("count runs since boundary: %v", err)
	}
	if got != 2 {
		t.Errorf("runs since %v = %d, want 2", boundary, got)
	}
}

func TestSetTaskRejectsAStatusFromAnOlderRunWithoutWritingAnEvent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	task, oldRun := startRunPolicy(t, st, "stale run")
	if _, err := st.UpdateRun(ctx, oldRun.ID, model.RunStarting, store.RunUpdate{State: model.RunEnded}); err != nil {
		t.Fatalf("end the old run: %v", err)
	}
	newestRun, err := startRunPolicyOnTask(t, st, task.Number)
	if err != nil {
		t.Fatalf("start newest run: %v", err)
	}
	before, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("read task before stale update: %v", err)
	}

	blocked := model.StatusBlocked
	_, err = st.SetTask(ctx, store.Actor{Run: oldRun.ID}, task.Number, model.Patch{Status: &blocked})
	assertRunPolicyRefusal(t, err, model.CodeStaleRun)
	after, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("read task after stale update: %v", err)
	}
	if len(after.History) != len(before.History) {
		t.Errorf("history length after stale update = %d, want unchanged %d", len(after.History), len(before.History))
	}
	if after.Task.Status != model.StatusStarted {
		t.Errorf("status after stale update = %q, want started", after.Task.Status)
	}

	title := "old run may still add context"
	oldRunPatch, err := st.SetTask(ctx, store.Actor{Run: oldRun.ID}, task.Number, model.Patch{Title: &title})
	if err != nil {
		t.Fatalf("old run title patch: %v", err)
	}
	if oldRunPatch.Title != title {
		t.Errorf("old run title patch title = %q, want %q", oldRunPatch.Title, title)
	}
	if _, err := st.UpdateRun(ctx, newestRun.ID, model.RunStarting, store.RunUpdate{State: model.RunEnded}); err != nil {
		t.Fatalf("end newest run: %v", err)
	}
	updated, err := st.SetTask(ctx, store.Actor{Run: newestRun.ID}, task.Number, model.Patch{Status: &blocked})
	if err != nil {
		t.Fatalf("newest ended run status patch: %v", err)
	}
	if updated.Status != model.StatusBlocked {
		t.Errorf("newest ended run status = %q, want blocked", updated.Status)
	}
}

func TestSetTaskTreatsAZeroRunAsNeverStale(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	task, first := startRunPolicy(t, st, "zero run")
	if _, err := st.UpdateRun(ctx, first.ID, model.RunStarting, store.RunUpdate{State: model.RunEnded}); err != nil {
		t.Fatalf("end the first run: %v", err)
	}
	if _, err := startRunPolicyOnTask(t, st, task.Number); err != nil {
		t.Fatalf("start newer run: %v", err)
	}

	blocked := model.StatusBlocked
	updated, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Status: &blocked})
	if err != nil {
		t.Fatalf("zero run status patch with newer run than %d: %v", first.ID, err)
	}
	if updated.Status != model.StatusBlocked {
		t.Errorf("zero run status = %q, want blocked", updated.Status)
	}
}

func TestSetTaskEndsALiveRunOnlyByTheRunEndingRule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status model.Status
		actor  func(model.Run) store.Actor
		ends   bool
	}{
		{"user sets done", model.StatusDone, func(model.Run) store.Actor { return store.Actor{} }, true},
		{"agent sets done", model.StatusDone, func(model.Run) store.Actor { return store.Actor{Session: "agent-session"} }, true},
		{"run itself sets blocked", model.StatusBlocked, func(run model.Run) store.Actor { return store.Actor{Run: run.ID} }, true},
		{"user sets blocked", model.StatusBlocked, func(model.Run) store.Actor { return store.Actor{} }, false},
		{"another agent sets review", model.StatusReview, func(model.Run) store.Actor { return store.Actor{Session: "agent-session"} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
			st := openRunPolicyStore(t, &now, false)
			task, run := startRunPolicy(t, st, tc.name)
			now = now.Add(time.Minute)
			if _, err := st.SetTask(ctx, tc.actor(run), task.Number, model.Patch{Status: &tc.status}); err != nil {
				t.Fatalf("set %s: %v", tc.status, err)
			}
			got, ok, err := st.CurrentRun(ctx, task.Number)
			if err != nil || !ok {
				t.Fatalf("CurrentRun() = %t, %v", ok, err)
			}
			switch {
			case tc.ends && (got.State != model.RunEnded || !got.EndedTS.Equal(now)):
				t.Errorf("run after %s = state %q ended_ts %v, want ended at %v", tc.name, got.State, got.EndedTS, now)
			case !tc.ends && (got.State != model.RunStarting || !got.EndedTS.IsZero()):
				t.Errorf("run after %s = state %q ended_ts %v, want it left starting", tc.name, got.State, got.EndedTS)
			}
		})
	}

	t.Run("unchanged started status leaves the live run alone", func(t *testing.T) {
		ctx := context.Background()
		now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
		st := openRunPolicyStore(t, &now, false)
		task, _ := startRunPolicy(t, st, "unchanged started")
		started := model.StatusStarted
		title := "still started"
		if _, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Status: &started, Title: &title}); err != nil {
			t.Fatalf("set unchanged started: %v", err)
		}
		got, ok, err := st.CurrentRun(ctx, task.Number)
		if err != nil {
			t.Fatalf("read current run: %v", err)
		}
		if !ok || got.State != model.RunStarting || !got.EndedTS.IsZero() {
			t.Errorf("run after unchanged started = %#v, found %t; want live starting run", got, ok)
		}
	})
}

func TestSetTaskLetsAnyoneSetTheAgentThread(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status model.Status
		actor  store.Actor
	}{
		{"agent on a ready task", model.StatusReady, store.Actor{Session: "agent"}},
		{"agent on an open task", model.StatusOpen, store.Actor{Session: "agent"}},
		{"user on a ready task", model.StatusReady, store.Actor{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
			st := openRunPolicyStore(t, &now, false)
			task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: tc.name, Status: tc.status}})
			if err != nil {
				t.Fatalf("add task: %v", err)
			}
			thread := "agent"
			updated, err := st.SetTask(ctx, tc.actor, task.Number, model.Patch{Thread: &thread})
			if err != nil || updated.Thread != "agent" {
				t.Fatalf("set thread = %q, %v; want agent, nil", updated.Thread, err)
			}
		})
	}
}

// policyRoute is the route every run of these tests takes.
var policyRoute = store.RunRoute{Root: "/work/desk", Isolation: "worktree", Model: "model-a"}

func openRunPolicyStore(t *testing.T, now *time.Time, autoStart bool) *store.Store {
	t.Helper()
	machine := testutil.NewMachine(t)
	st, err := store.Open(machine.Paths.DB(), store.Options{
		Now:       func() time.Time { return *now },
		AutoStart: autoStart,
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return st
}

func startRunPolicy(t *testing.T, st *store.Store, title string) (model.Task, model.Run) {
	t.Helper()
	ctx := context.Background()
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: title, Status: model.StatusReady,
	}})
	if err != nil {
		t.Fatalf("add ready task: %v", err)
	}
	run, err := startRunPolicyOnTask(t, st, task.Number)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	return task, run
}

func startRunPolicyOnTask(t *testing.T, st *store.Store, task int) (model.Run, error) {
	t.Helper()
	// A cap no test reaches, so every run starts.
	return st.StartRun(context.Background(), store.Actor{}, task, policyRoute, store.RunCaps{Slots: 100, PerDay: 1000})
}

func assertRunPolicyRefusal(t *testing.T, err error, want string) {
	t.Helper()
	refusal, ok := model.AsRefusal(err)
	if !ok {
		t.Fatalf("error = %v, want refusal %q", err, want)
	}
	if refusal.Code != want {
		t.Errorf("refusal code = %q, want %q", refusal.Code, want)
	}
}
