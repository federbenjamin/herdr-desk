package store_test

import (
	"context"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestSetTaskDoneEndsRunsAndOtherPersonStatusesLeaveThemLive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actor   store.Actor
		status  model.Status
		wantEnd bool
	}{
		{"person sets done", store.Actor{}, model.StatusDone, true},
		{"person sets review", store.Actor{}, model.StatusReview, false},
		{"person sets blocked", store.Actor{}, model.StatusBlocked, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t, store.Options{})
			task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: tc.name, Status: model.StatusReady}})
			if err != nil {
				t.Fatalf("add task: %v", err)
			}
			run, err := st.StartRun(ctx, task.Number, startRoute, store.RunCaps{Slots: 1, PerDay: 1000})
			if err != nil {
				t.Fatalf("start run: %v", err)
			}

			if _, err := st.SetTask(ctx, tc.actor, task.Number, model.Patch{Status: &tc.status}); err != nil {
				t.Fatalf("set %s: %v", tc.status, err)
			}
			got, ok, err := st.CurrentRun(ctx, task.Number)
			if err != nil || !ok {
				t.Fatalf("current run = (%#v, %t, %v)", got, ok, err)
			}
			if ended := got.State == model.RunEnded; ended != tc.wantEnd {
				t.Fatalf("run after person set %s = %#v, ended = %t; want ended = %t", tc.status, got, ended, tc.wantEnd)
			}
			if got.ID != run.ID {
				t.Fatalf("current run id = %d, want %d", got.ID, run.ID)
			}
		})
	}
}

func TestSetTaskWorkerRepeatedBlockedEndsIdleRunAndReleasesInPlaceRoot(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "idle worker", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	route := store.RunRoute{Root: "/repos/desk", Isolation: "in-place", Model: "model-a"}
	run, err := st.StartRun(ctx, task.Number, route, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if changed, err := st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{State: model.RunIdle}); err != nil || !changed {
		t.Fatalf("set idle = (%t, %v), want (true, nil)", changed, err)
	}
	live, err := st.LiveRuns(ctx)
	if err != nil || len(live) != 1 || live[0].ID != run.ID || live[0].State != model.RunIdle {
		t.Fatalf("live runs after idle = (%#v, %v), want the idle run", live, err)
	}

	blocked := model.StatusBlocked
	if _, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Status: &blocked}); err != nil {
		t.Fatalf("person sets blocked: %v", err)
	}
	before := eventCount(t, st)
	if _, err := st.SetTask(ctx, store.Actor{Session: "worker", Run: run.ID}, task.Number, model.Patch{Status: &blocked}); err != nil {
		t.Fatalf("worker repeats blocked: %v", err)
	}
	if after := eventCount(t, st); after != before {
		t.Fatalf("events after repeated worker status = %d, want %d", after, before)
	}
	got, ok, err := st.CurrentRun(ctx, task.Number)
	if err != nil || !ok || got.State != model.RunEnded || got.EndedTS.IsZero() {
		t.Fatalf("run after worker hand-back = (%#v, %t, %v), want ended with a timestamp", got, ok, err)
	}

	next, err := st.StartRun(ctx, task.Number, route, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil || next.State != model.RunStarting || next.ID == run.ID {
		t.Fatalf("start after idle hand-back = (%#v, %v), want a new starting run", next, err)
	}
}

func TestHandBackGatesStatusAndRefusesAStaleRunWithoutWrites(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "hand back", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	run, err := st.StartRun(ctx, task.Number, startRoute, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	before := eventCount(t, st)
	gotTask, claimed, err := st.HandBack(ctx, run, store.HandBack{
		From: model.RunStarting, To: model.RunEnded, Status: model.StatusBlocked, IfStatus: model.StatusReady, Note: "worker stopped",
	})
	if err != nil || !claimed {
		t.Fatalf("conditional HandBack = (%#v, %t, %v), want claimed without error", gotTask, claimed, err)
	}
	if gotTask.Status != model.StatusStarted {
		t.Fatalf("task after gated hand-back status = %q, want started", gotTask.Status)
	}
	if after := eventCount(t, st); after != before+1 {
		t.Fatalf("events after gated hand-back = %d, want note only (%d)", after, before+1)
	}
	first, ok, err := st.CurrentRun(ctx, task.Number)
	if err != nil || !ok || first.State != model.RunEnded {
		t.Fatalf("first run after HandBack = (%#v, %t, %v), want ended", first, ok, err)
	}
	second, err := st.StartRun(ctx, task.Number, startRoute, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start second run: %v", err)
	}
	before = eventCount(t, st)
	_, claimed, err = st.HandBack(ctx, first, store.HandBack{From: model.RunEnded, To: model.RunEnded, Note: "must not write"})
	if got := refusalCode(t, err); got != model.CodeStaleRun {
		t.Fatalf("stale HandBack refusal = %q, want %q", got, model.CodeStaleRun)
	}
	if claimed {
		t.Fatal("stale HandBack claimed = true, want false")
	}
	if after := eventCount(t, st); after != before {
		t.Fatalf("events after stale HandBack = %d, want %d", after, before)
	}
	current, ok, err := st.CurrentRun(ctx, task.Number)
	if err != nil || !ok || current.ID != second.ID || current.State != model.RunStarting {
		t.Fatalf("current run after stale HandBack = (%#v, %t, %v), want unchanged second run %#v", current, ok, err, second)
	}
}
