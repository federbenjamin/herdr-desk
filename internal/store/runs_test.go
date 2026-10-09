package store_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// startRoute is the route the runs of these tests take; cap 100 is one no test reaches.
var startRoute = store.RunRoute{Root: "/repos/desk", Isolation: "worktree", Model: "model-a"}

func TestStartRunCreatesAStartingRunAndMarksTaskStartedWithItsRoute(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "ready", Status: model.StatusReady,
	}})
	if err != nil {
		t.Fatalf("add ready task: %v", err)
	}

	run, err := st.StartRun(ctx, store.Actor{}, task.Number, startRoute, store.RunCaps{Slots: 100, PerDay: 1000})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	if run.Task != task.Number || run.State != model.RunStarting || run.StartedTS.IsZero() ||
		run.Root != startRoute.Root || run.Isolation != startRoute.Isolation || run.Model != startRoute.Model {
		t.Errorf("StartRun() = %#v, want a starting run for T%d on %#v with a start time", run, task.Number, startRoute)
	}
	detail, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if got := detail.Task; got.Status != model.StatusStarted || got.Root != startRoute.Root || got.Isolation != startRoute.Isolation || got.Model != startRoute.Model {
		t.Errorf("task after StartRun = %#v, want started with the route", got)
	}
	if len(detail.History) != 2 {
		t.Fatalf("history length after StartRun = %d, want the creation and one set event", len(detail.History))
	}
	event := detail.History[1]
	if event.Kind != model.KindSet || event.Run != run.ID || event.Session != "" {
		t.Errorf("start event = %#v, want a sessionless set event carrying run %d", event, run.ID)
	}
	var patch model.Patch
	if err := json.Unmarshal(event.Data, &patch); err != nil {
		t.Fatalf("unmarshal start event: %v", err)
	}
	if patch.Status == nil || *patch.Status != model.StatusStarted || patch.Root == nil || *patch.Root != startRoute.Root {
		t.Errorf("start event patch = %#v, want status started and the root", patch)
	}
}

func TestStartRunRefusesDoneArchivedAndUnknownTasks(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	done, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "done", Status: model.StatusDone}})
	if err != nil {
		t.Fatalf("add done task: %v", err)
	}
	archived, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "archived", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add archived task: %v", err)
	}
	yes := true
	if _, err := st.SetTask(ctx, store.Actor{}, archived.Number, model.Patch{Archived: &yes}); err != nil {
		t.Fatalf("archive task: %v", err)
	}

	for _, tc := range []struct {
		name string
		task int
		code string
	}{
		{"done", done.Number, model.CodeNotAllowed},
		{"archived", archived.Number, model.CodeNotAllowed},
		{"unknown", 99, model.CodeUnknownTask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := eventCount(t, st)
			_, err := st.StartRun(ctx, store.Actor{}, tc.task, startRoute, store.RunCaps{Slots: 100, PerDay: 1000})
			if got := refusalCode(t, err); got != tc.code {
				t.Errorf("StartRun(T%d) refusal = %q, want %q", tc.task, got, tc.code)
			}
			if got := eventCount(t, st); got != before {
				t.Errorf("event count after a refused start = %d, want %d", got, before)
			}
		})
	}
}

func TestCurrentRunReportsNoRun(t *testing.T) {
	run, ok, err := openStore(t, store.Options{}).CurrentRun(context.Background(), 1)
	if err != nil {
		t.Fatalf("CurrentRun() error = %v", err)
	}
	if ok || run != (model.Run{}) {
		t.Errorf("CurrentRun() = (%#v, %t), want (zero run, false)", run, ok)
	}
}

func TestCurrentRunReturnsTheTaskRun(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "ready", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add ready task: %v", err)
	}
	want, err := st.StartRun(ctx, store.Actor{}, task.Number, startRoute, store.RunCaps{Slots: 100, PerDay: 1000})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	got, ok, err := st.CurrentRun(ctx, task.Number)
	if err != nil {
		t.Fatalf("CurrentRun() error = %v", err)
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("CurrentRun() = (%#v, %t), want (%#v, true)", got, ok, want)
	}
}

func TestCurrentRunReturnsTheNewestRun(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "ready", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add ready task: %v", err)
	}
	first, err := st.StartRun(ctx, store.Actor{}, task.Number, startRoute, store.RunCaps{Slots: 100, PerDay: 1000})
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if _, err := st.UpdateRun(ctx, first.ID, model.RunStarting, store.RunUpdate{State: model.RunEnded}); err != nil {
		t.Fatalf("end first run: %v", err)
	}
	want, err := st.StartRun(ctx, store.Actor{}, task.Number, startRoute, store.RunCaps{Slots: 100, PerDay: 1000})
	if err != nil {
		t.Fatalf("start second run: %v", err)
	}
	got, ok, err := st.CurrentRun(ctx, task.Number)
	if err != nil {
		t.Fatalf("CurrentRun() error = %v", err)
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("CurrentRun() = (%#v, %t), want newest (%#v, true)", got, ok, want)
	}
}

func TestRunWroteRequiresAnEventWithTheRunAndItsOwnSession(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "ready", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add ready task: %v", err)
	}
	run, err := st.StartRun(ctx, store.Actor{Session: "launcher-session"}, task.Number, startRoute, store.RunCaps{Slots: 100, PerDay: 1000})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	if ok, err := st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{Session: "worker-session"}); err != nil || !ok {
		t.Fatalf("give the run its session = (%t, %v)", ok, err)
	}
	if wrote, err := st.RunWrote(ctx, run.ID); err != nil || wrote {
		t.Fatalf("RunWrote() after only the launcher's start = (%t, %v), want false", wrote, err)
	}
	if _, err := st.Note(ctx, store.Actor{Run: run.ID}, store.NoteInput{Task: task.Number, NoteData: model.NoteData{Text: "runner update"}}); err != nil {
		t.Fatalf("write runner note: %v", err)
	}
	wrote, err := st.RunWrote(ctx, run.ID)
	if err != nil {
		t.Fatalf("RunWrote() after runner note error = %v", err)
	}
	if wrote {
		t.Error("RunWrote() after only sessionless runner events = true, want false")
	}
	if _, err := st.Note(ctx, store.Actor{Session: "worker-session", Run: run.ID}, store.NoteInput{Task: task.Number, NoteData: model.NoteData{Text: "worker update"}}); err != nil {
		t.Fatalf("write worker note: %v", err)
	}
	wrote, err = st.RunWrote(ctx, run.ID)
	if err != nil {
		t.Fatalf("RunWrote() after worker note error = %v", err)
	}
	if !wrote {
		t.Error("RunWrote() after a session event with the run id = false, want true")
	}
}

func TestStartAndHandBackWritesNameWhoMadeThem(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	for _, tc := range []struct {
		name    string
		starter store.Actor
		want    model.Who
	}{
		{"an agent session starts the run", store.Actor{Session: "launcher-session"}, model.WhoAgent},
		{"a person starts the run", store.Actor{}, model.WhoUser},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := mustAdd(t, st, tc.name, model.StatusReady, "")
			run, err := st.StartRun(ctx, tc.starter, task.Number, startRoute, store.RunCaps{Slots: 100, PerDay: 1000})
			if err != nil {
				t.Fatalf("StartRun() error = %v", err)
			}
			if _, claimed, err := st.HandBack(ctx, run, store.HandBack{From: model.RunStarting, To: model.RunEnded,
				Status: model.StatusReview, Tags: []string{model.TagRunner}, Note: "the pane closed"}); err != nil || !claimed {
				t.Fatalf("HandBack() = (%t, %v)", claimed, err)
			}
			d, err := st.GetTask(ctx, task.Number)
			if err != nil {
				t.Fatal(err)
			}
			var got []model.Who
			for _, e := range d.History[1:] {
				got = append(got, e.Who)
			}
			want := []model.Who{tc.want, model.WhoRunner, model.WhoRunner}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("writers after the task's own = %q, want %q (the start, then the hand-back's note and status)", got, want)
			}
			if start := d.History[1]; start.Session != tc.starter.Session || start.Run != run.ID {
				t.Fatalf("start event = session %q run %d, want session %q run %d", start.Session, start.Run, tc.starter.Session, run.ID)
			}
		})
	}
}
