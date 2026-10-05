package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestOpenMigratesRoutingRunsToStartingWithoutLeavingThemOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desk.db")
	db, err := sqlOpenSQLite(path)
	if err != nil {
		t.Fatalf("open version-1 database: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE runs(
		id INTEGER PRIMARY KEY,
		task INTEGER NOT NULL,
		state TEXT NOT NULL,
		root TEXT NOT NULL DEFAULT '',
		isolation TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		session TEXT NOT NULL DEFAULT '',
		workspace TEXT NOT NULL DEFAULT '',
		pane TEXT NOT NULL DEFAULT '',
		started_ts TEXT NOT NULL,
		ended_ts TEXT NULL,
		exit INTEGER NULL
	);
	INSERT INTO runs(id, task, state, started_ts) VALUES(1, 7, 'routing', '2026-10-04T12:00:00Z');
	PRAGMA user_version = 1;`); err != nil {
		db.Close()
		t.Fatalf("seed version-1 routing run: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close version-1 database: %v", err)
	}

	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("Open() migrates version 1: %v", err)
	}
	runs, err := st.ListRuns(context.Background())
	if err != nil {
		st.Close()
		t.Fatalf("list migrated runs: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}
	if len(runs) != 1 || runs[0].State != model.RunStarting {
		t.Fatalf("migrated runs = %#v, want one starting run", runs)
	}

	db, err = sqlOpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read migrated user_version: %v", err)
	}
	if version != 2 {
		t.Errorf("user_version = %d, want 2", version)
	}
	var leftOpen int
	if err := db.QueryRow("SELECT left_open FROM runs WHERE id = 1").Scan(&leftOpen); err != nil {
		t.Fatalf("read migrated left_open: %v", err)
	}
	if leftOpen != 0 {
		t.Errorf("runs.left_open = %d, want 0", leftOpen)
	}
}

func TestStartRunEndsAnIdleRunAndWaitsWhenTheCapIsFull(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	first, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "first", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add first task: %v", err)
	}
	idle, err := st.StartRun(ctx, first.Number, policyRoute, 1)
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if changed, err := st.UpdateRun(ctx, idle.ID, model.RunStarting, store.RunUpdate{State: model.RunIdle}); err != nil || !changed {
		t.Fatalf("make first run idle = (%t, %v)", changed, err)
	}

	now = now.Add(time.Minute)
	replacement, err := st.StartRun(ctx, first.Number, policyRoute, 1)
	if err != nil {
		t.Fatalf("start replacement run: %v", err)
	}
	if replacement.State != model.RunStarting {
		t.Errorf("replacement state = %q, want starting because idle takes no cap slot", replacement.State)
	}
	gotIdle, ok, err := st.CurrentRun(ctx, first.Number)
	if err != nil || !ok {
		t.Fatalf("read replacement run = (%t, %v)", ok, err)
	}
	if gotIdle.ID != replacement.ID {
		t.Fatalf("current run = %d, want replacement %d", gotIdle.ID, replacement.ID)
	}

	second, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "second", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add second task: %v", err)
	}
	waiting, err := st.StartRun(ctx, second.Number, policyRoute, 1)
	if err != nil {
		t.Fatalf("start capped run: %v", err)
	}
	if waiting.State != model.RunWaiting {
		t.Errorf("capped run state = %q, want waiting", waiting.State)
	}

	runs, err := st.ListRuns(ctx)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if runs[0].State != model.RunEnded || !runs[0].EndedTS.Equal(now) {
		t.Errorf("idle run after replacement = %#v, want ended at %v", runs[0], now)
	}
}

func TestClaimWaitingStartsTheOldestRunOnceASlotOpens(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	var waiting []model.Run
	for _, title := range []string{"first", "second"} {
		task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: title, Status: model.StatusReady}})
		if err != nil {
			t.Fatalf("add %s task: %v", title, err)
		}
		run, err := st.StartRun(ctx, task.Number, policyRoute, 0)
		if err != nil {
			t.Fatalf("start %s run: %v", title, err)
		}
		if run.State != model.RunWaiting {
			t.Fatalf("%s run state = %q, want waiting", title, run.State)
		}
		waiting = append(waiting, run)
	}

	now = now.Add(time.Minute)
	claimed, ok, err := st.ClaimWaiting(ctx, 1)
	if err != nil {
		t.Fatalf("ClaimWaiting() error = %v", err)
	}
	if !ok || claimed.ID != waiting[0].ID || claimed.State != model.RunStarting || !claimed.StartedTS.Equal(now) {
		t.Errorf("ClaimWaiting() = (%#v, %t), want oldest run %d starting at %v", claimed, ok, waiting[0].ID, now)
	}
	persisted, ok, err := st.CurrentRun(ctx, waiting[0].Task)
	if err != nil || !ok {
		t.Fatalf("read claimed run = (%t, %v)", ok, err)
	}
	if persisted.State != model.RunStarting || !persisted.StartedTS.Equal(now) {
		t.Errorf("claimed run in store = %#v, want starting at %v", persisted, now)
	}
	second, ok, err := st.CurrentRun(ctx, waiting[1].Task)
	if err != nil || !ok {
		t.Fatalf("read second waiting run = (%t, %v)", ok, err)
	}
	if second.State != model.RunWaiting {
		t.Errorf("second waiting run state = %q, want waiting", second.State)
	}
}

func TestUpdateRunFromIdleKeepsTheOriginalStartTime(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "idle run", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	run, err := st.StartRun(ctx, task.Number, policyRoute, 1)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	now = now.Add(time.Hour)
	if changed, err := st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{State: model.RunIdle}); err != nil || !changed {
		t.Fatalf("set run idle = (%t, %v)", changed, err)
	}
	now = now.Add(time.Hour)
	if changed, err := st.UpdateRun(ctx, run.ID, model.RunIdle, store.RunUpdate{State: model.RunRunning}); err != nil || !changed {
		t.Fatalf("resume idle run = (%t, %v)", changed, err)
	}
	got, ok, err := st.CurrentRun(ctx, task.Number)
	if err != nil || !ok {
		t.Fatalf("read resumed run = (%t, %v)", ok, err)
	}
	if !got.StartedTS.Equal(run.StartedTS) {
		t.Errorf("resumed run started at %v, want original start %v", got.StartedTS, run.StartedTS)
	}
}

func TestHandBackWithALostClaimWritesNothing(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "hand back", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	run, err := st.StartRun(ctx, task.Number, policyRoute, 1)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	before, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("read task before lost claim: %v", err)
	}

	updated, claimed, err := st.HandBack(ctx, run, store.HandBack{
		From:   model.RunWaiting,
		To:     model.RunEnded,
		Status: model.StatusReview,
		Note:   "this note must not appear",
	})
	if err != nil {
		t.Fatalf("HandBack() lost claim error = %v", err)
	}
	if claimed || updated.Number != 0 {
		t.Errorf("HandBack() lost claim = (%#v, %t), want (zero task, false)", updated, claimed)
	}
	after, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("read task after lost claim: %v", err)
	}
	if after.Task.Status != before.Task.Status || len(after.History) != len(before.History) {
		t.Errorf("task after lost claim = %#v with %d events, want status %q and %d events", after.Task, len(after.History), before.Task.Status, len(before.History))
	}
	current, ok, err := st.CurrentRun(ctx, task.Number)
	if err != nil || !ok {
		t.Fatalf("read run after lost claim = (%t, %v)", ok, err)
	}
	if current.State != model.RunStarting {
		t.Errorf("run state after lost claim = %q, want starting", current.State)
	}
}
