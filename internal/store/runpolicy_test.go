package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
)

func TestStartRunCreatesARoutingRunAndStartsTheArmedTask(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "armed task", Status: model.StatusReady, Thread: "agent",
	}})
	if err != nil {
		t.Fatalf("add armed task: %v", err)
	}

	run, err := st.StartRun(ctx, task.Number)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if run.Task != task.Number || run.State != model.RunRouting {
		t.Errorf("started run = %#v, want task %d in routing", run, task.Number)
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

func TestStartRunRefusesATaskThatIsNotArmed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "open task"}})
	if err != nil {
		t.Fatalf("add open task: %v", err)
	}

	_, err = st.StartRun(ctx, task.Number)
	if !errors.Is(err, store.ErrNotArmed) {
		t.Fatalf("StartRun() error = %v, want ErrNotArmed", err)
	}
}

func TestUpdateRunChangesOnlyItsExpectedStateAndKeepsZeroFields(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	task, run := startRunPolicy(t, st, "update run")

	now = now.Add(time.Minute)
	changed, err := st.UpdateRun(ctx, run.ID, model.RunRouting, store.RunUpdate{
		State:     model.RunRunning,
		Root:      "/work/desk",
		Isolation: "worktree",
		Model:     "model-a",
		Reason:    "matched the repository",
		Session:   "session-a",
		Workspace: "/work/desk/.worktrees/task",
		Pane:      "pane-a",
	})
	if err != nil {
		t.Fatalf("update routing run: %v", err)
	}
	if !changed {
		t.Fatal("update routing run changed = false, want true")
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
	changed, err = st.UpdateRun(ctx, run.ID, model.RunRouting, store.RunUpdate{State: model.RunFailed})
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
	if got.State != model.RunWaiting || got.Root != "/work/desk" || got.Isolation != "worktree" || got.Model != "model-a" || got.Reason != "matched the repository" || got.Session != "session-a" || got.Workspace != "/work/desk/.worktrees/task" || got.Pane != "pane-a" {
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
			changed, err := st.UpdateRun(ctx, run.ID, model.RunRouting, store.RunUpdate{State: terminal})
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

func TestLiveRunsReturnsOnlyLiveStatesInIDOrder(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)

	_, routing := startRunPolicy(t, st, "routing")
	_, waiting := startRunPolicy(t, st, "waiting")
	_, running := startRunPolicy(t, st, "running")
	_, ended := startRunPolicy(t, st, "ended")
	_, failed := startRunPolicy(t, st, "failed")
	_, killed := startRunPolicy(t, st, "killed")
	for _, update := range []struct {
		run   model.Run
		state string
	}{
		{waiting, model.RunWaiting},
		{running, model.RunRunning},
		{ended, model.RunEnded},
		{failed, model.RunFailed},
		{killed, model.RunKilled},
	} {
		changed, err := st.UpdateRun(ctx, update.run.ID, model.RunRouting, store.RunUpdate{State: update.state})
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
	if len(runs) != 3 {
		t.Fatalf("live run count = %d, want 3: %#v", len(runs), runs)
	}
	for i, want := range []model.Run{routing, waiting, running} {
		if runs[i].ID != want.ID {
			t.Errorf("live run %d id = %d, want %d", i, runs[i].ID, want.ID)
		}
	}
	if runs[0].State != model.RunRouting || runs[1].State != model.RunWaiting || runs[2].State != model.RunRunning {
		t.Errorf("live run states = %q, %q, %q; want routing, waiting, running", runs[0].State, runs[1].State, runs[2].State)
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
	ready := model.StatusReady
	if _, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Status: &ready}); err != nil {
		t.Fatalf("rearm task: %v", err)
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
	if _, err := st.UpdateRun(ctx, newestRun.ID, model.RunRouting, store.RunUpdate{State: model.RunEnded}); err != nil {
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
	ready := model.StatusReady
	if _, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Status: &ready}); err != nil {
		t.Fatalf("rearm task: %v", err)
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

func TestSetTaskStatusLeavingStartedEndsTheLiveRunForEveryActor(t *testing.T) {
	for _, tc := range []struct {
		name  string
		actor func(model.Run) store.Actor
	}{
		{"user", func(model.Run) store.Actor { return store.Actor{} }},
		{"agent", func(model.Run) store.Actor { return store.Actor{Session: "agent-session"} }},
		{"run itself", func(run model.Run) store.Actor { return store.Actor{Run: run.ID} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
			st := openRunPolicyStore(t, &now, false)
			task, run := startRunPolicy(t, st, tc.name)
			now = now.Add(time.Minute)
			blocked := model.StatusBlocked
			if _, err := st.SetTask(ctx, tc.actor(run), task.Number, model.Patch{Status: &blocked}); err != nil {
				t.Fatalf("set blocked: %v", err)
			}
			got, ok, err := st.CurrentRun(ctx, task.Number)
			if err != nil {
				t.Fatalf("read current run: %v", err)
			}
			if !ok {
				t.Fatal("CurrentRun() found no run")
			}
			if got.State != model.RunEnded || !got.EndedTS.Equal(now) {
				t.Errorf("run after status change = state %q ended_ts %v, want ended at %v", got.State, got.EndedTS, now)
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
		if !ok || got.State != model.RunRouting || !got.EndedTS.IsZero() {
			t.Errorf("run after unchanged started = %#v, found %t; want live routing run", got, ok)
		}
	})
}

func TestSetTaskOnlyLetsAgentsArmReadyTasksWhenConfigured(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      model.Status
		thread      string
		agentMayArm bool
		actor       store.Actor
		wantRefusal string
		wantThread  string
	}{
		{"agent cannot arm an already-agent ready task", model.StatusReady, "agent", false, store.Actor{Session: "agent"}, model.CodeNotAllowed, ""},
		{"agent may set open task thread", model.StatusOpen, "", false, store.Actor{Session: "agent"}, "", "agent"},
		{"configured agent may arm ready task", model.StatusReady, "", true, store.Actor{Session: "agent"}, "", "agent"},
		{"user may arm ready task", model.StatusReady, "", false, store.Actor{}, "", "agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
			st := openRunPolicyStore(t, &now, tc.agentMayArm)
			task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: tc.name, Status: tc.status, Thread: tc.thread}})
			if err != nil {
				t.Fatalf("add task: %v", err)
			}
			before, err := st.GetTask(ctx, task.Number)
			if err != nil {
				t.Fatalf("read task before thread patch: %v", err)
			}
			thread := "agent"
			updated, err := st.SetTask(ctx, tc.actor, task.Number, model.Patch{Thread: &thread})
			if tc.wantRefusal != "" {
				assertRunPolicyRefusal(t, err, tc.wantRefusal)
				after, getErr := st.GetTask(ctx, task.Number)
				if getErr != nil {
					t.Fatalf("read task after refused thread patch: %v", getErr)
				}
				if len(after.History) != len(before.History) {
					t.Errorf("history length after refused thread patch = %d, want %d", len(after.History), len(before.History))
				}
				return
			}
			if err != nil {
				t.Fatalf("set thread: %v", err)
			}
			if updated.Thread != tc.wantThread {
				t.Errorf("thread = %q, want %q", updated.Thread, tc.wantThread)
			}
		})
	}
}

func openRunPolicyStore(t *testing.T, now *time.Time, agentsMayArm bool) *store.Store {
	t.Helper()
	machine := testutil.NewMachine(t)
	st, err := store.Open(machine.Paths.DB(), store.Options{
		Now:          func() time.Time { return *now },
		AgentsMayArm: agentsMayArm,
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
		Title: title, Status: model.StatusReady, Thread: "agent",
	}})
	if err != nil {
		t.Fatalf("add armed task: %v", err)
	}
	run, err := startRunPolicyOnTask(t, st, task.Number)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	return task, run
}

func startRunPolicyOnTask(t *testing.T, st *store.Store, task int) (model.Run, error) {
	t.Helper()
	return st.StartRun(context.Background(), task)
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
