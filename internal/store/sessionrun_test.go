package store_test

import (
	"context"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestLiveRunOfSessionReturnsTheNewestLiveRunAndFallsBackWhenItEnds(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})

	firstTask, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "first", Status: model.StatusReady,
	}})
	if err != nil {
		t.Fatalf("add first task: %v", err)
	}
	first, err := st.StartRun(ctx, firstTask.Number, startRoute, store.RunCaps{Slots: 100, PerDay: 1000})
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if _, err := st.UpdateRun(ctx, first.ID, model.RunStarting, store.RunUpdate{State: model.RunRunning, Session: "w3-session"}); err != nil {
		t.Fatalf("record first run session: %v", err)
	}

	newerTask, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "newer", Status: model.StatusReady,
	}})
	if err != nil {
		t.Fatalf("add newer task: %v", err)
	}
	newer, err := st.StartRun(ctx, newerTask.Number, startRoute, store.RunCaps{Slots: 100, PerDay: 1000})
	if err != nil {
		t.Fatalf("start newer run: %v", err)
	}
	if _, err := st.UpdateRun(ctx, newer.ID, model.RunStarting, store.RunUpdate{State: model.RunRunning, Session: "w3-session"}); err != nil {
		t.Fatalf("record newer run session: %v", err)
	}

	got, ok, err := st.LiveRunOfSession(ctx, "w3-session")
	if err != nil {
		t.Fatalf("LiveRunOfSession() with two live runs: %v", err)
	}
	if !ok || got.ID != newer.ID {
		t.Fatalf("LiveRunOfSession() = (%#v, %t), want newest live run %d", got, ok, newer.ID)
	}

	if _, err := st.UpdateRun(ctx, newer.ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil {
		t.Fatalf("end newer run: %v", err)
	}
	got, ok, err = st.LiveRunOfSession(ctx, "w3-session")
	if err != nil {
		t.Fatalf("LiveRunOfSession() after ending newest run: %v", err)
	}
	if !ok || got.ID != first.ID {
		t.Fatalf("LiveRunOfSession() after ending newest run = (%#v, %t), want live run %d", got, ok, first.ID)
	}
}

func TestLiveRunOfSessionDoesNotMatchAnEmptySession(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "ready", Status: model.StatusReady,
	}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	if _, err := st.StartRun(ctx, task.Number, startRoute, store.RunCaps{Slots: 100, PerDay: 1000}); err != nil {
		t.Fatalf("start run: %v", err)
	}

	run, ok, err := st.LiveRunOfSession(ctx, "")
	if err != nil {
		t.Fatalf("LiveRunOfSession(\"\"): %v", err)
	}
	if ok || run != (model.Run{}) {
		t.Errorf("LiveRunOfSession(\"\") = (%#v, %t), want (zero run, false)", run, ok)
	}
}
