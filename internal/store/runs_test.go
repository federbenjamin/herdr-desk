package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
)

func TestArmedReturnsOnlyUserArmedReadyAgentTasks(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})

	armed, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "user armed", Status: model.StatusReady, Thread: "agent",
	}})
	if err != nil {
		t.Fatalf("add armed task: %v", err)
	}
	if _, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "wrong thread", Status: model.StatusReady, Thread: "user",
	}}); err != nil {
		t.Fatalf("add wrong-thread task: %v", err)
	}
	archived, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "archived", Status: model.StatusReady, Thread: "agent",
	}})
	if err != nil {
		t.Fatalf("add archived task: %v", err)
	}
	archivedValue := true
	if _, err := st.SetTask(ctx, store.Actor{}, archived.Number, model.Patch{Archived: &archivedValue}); err != nil {
		t.Fatalf("archive task: %v", err)
	}
	if _, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "not ready", Status: model.StatusOpen, Thread: "agent",
	}}); err != nil {
		t.Fatalf("add open task: %v", err)
	}

	got, err := st.Armed(ctx)
	if err != nil {
		t.Fatalf("Armed() error = %v", err)
	}
	if want := []model.Task{armed}; !reflect.DeepEqual(got, want) {
		t.Errorf("Armed() = %#v, want %#v", got, want)
	}
}

func TestArmedOrdersTasksByTheirArmingEvent(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	first, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "first", Thread: "agent"}})
	if err != nil {
		t.Fatalf("add first task: %v", err)
	}
	second, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "second", Thread: "agent"}})
	if err != nil {
		t.Fatalf("add second task: %v", err)
	}
	ready := model.StatusReady
	if _, err := st.SetTask(ctx, store.Actor{}, second.Number, model.Patch{Status: &ready}); err != nil {
		t.Fatalf("arm second task: %v", err)
	}
	if _, err := st.SetTask(ctx, store.Actor{}, first.Number, model.Patch{Status: &ready}); err != nil {
		t.Fatalf("arm first task: %v", err)
	}

	got, err := st.Armed(ctx)
	if err != nil {
		t.Fatalf("Armed() error = %v", err)
	}
	if len(got) != 2 || got[0].Number != second.Number || got[1].Number != first.Number {
		t.Errorf("Armed() task order = %#v, want T%d then T%d", got, second.Number, first.Number)
	}
}

func TestArmedHonorsAgentsMayArmOnlyForThatStore(t *testing.T) {
	ctx := context.Background()
	machine := testutil.NewMachine(t)
	st, err := store.Open(machine.Paths.DB(), store.Options{AgentsMayArm: true})
	if err != nil {
		t.Fatalf("open agent-arming store: %v", err)
	}
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "agent armed", Thread: "agent"}})
	if err != nil {
		st.Close()
		t.Fatalf("add task: %v", err)
	}
	ready := model.StatusReady
	if _, err := st.SetTask(ctx, store.Actor{Session: "agent-session"}, task.Number, model.Patch{Status: &ready}); err != nil {
		st.Close()
		t.Fatalf("agent arms task: %v", err)
	}
	got, err := st.Armed(ctx)
	if err != nil {
		st.Close()
		t.Fatalf("Armed() with AgentsMayArm: %v", err)
	}
	if len(got) != 1 || got[0].Number != task.Number || got[0].Status != model.StatusReady {
		st.Close()
		t.Fatalf("Armed() with AgentsMayArm = %#v, want ready T%d", got, task.Number)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close agent-arming store: %v", err)
	}

	st, err = store.Open(machine.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("reopen default store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	got, err = st.Armed(ctx)
	if err != nil {
		t.Fatalf("Armed() after reopen: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Armed() after reopen = %#v, want no agent-armed tasks", got)
	}
}

func TestStartRunCreatesRoutingRunAndMarksTaskStarted(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "ready", Status: model.StatusReady, Thread: "agent",
	}})
	if err != nil {
		t.Fatalf("add ready task: %v", err)
	}

	run, err := st.StartRun(ctx, task.Number)
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	if run.Task != task.Number || run.State != model.RunRouting || run.StartedTS.IsZero() {
		t.Errorf("StartRun() = %#v, want routing run for T%d with a start time", run, task.Number)
	}
	detail, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if detail.Task.Status != model.StatusStarted {
		t.Errorf("task status after StartRun = %q, want %q", detail.Task.Status, model.StatusStarted)
	}
	if len(detail.History) < 2 {
		t.Fatalf("history length after StartRun = %d, want creation and set events", len(detail.History))
	}
	event := detail.History[len(detail.History)-1]
	if event.Kind != model.KindSet || event.Run != run.ID || event.Session != "" {
		t.Errorf("start event = %#v, want a sessionless set event carrying run %d", event, run.ID)
	}
	var patch model.Patch
	if err := json.Unmarshal(event.Data, &patch); err != nil {
		t.Fatalf("unmarshal start event: %v", err)
	}
	if patch.Status == nil || *patch.Status != model.StatusStarted {
		t.Errorf("start event patch = %#v, want status started", patch)
	}
}

func TestStartRunRefusesTasksThatAreNotArmed(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	open, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "open", Thread: "agent"}})
	if err != nil {
		t.Fatalf("add open task: %v", err)
	}
	otherThread, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "other thread", Status: model.StatusReady, Thread: "user"}})
	if err != nil {
		t.Fatalf("add other-thread task: %v", err)
	}
	alreadyStarted, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "started", Status: model.StatusReady, Thread: "agent"}})
	if err != nil {
		t.Fatalf("add started task: %v", err)
	}
	if _, err := st.StartRun(ctx, alreadyStarted.Number); err != nil {
		t.Fatalf("start setup task: %v", err)
	}

	for _, tc := range []struct {
		name string
		task int
	}{
		{name: "open", task: open.Number},
		{name: "other thread", task: otherThread.Number},
		{name: "already started", task: alreadyStarted.Number},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.StartRun(ctx, tc.task)
			if !errors.Is(err, store.ErrNotArmed) {
				t.Errorf("StartRun(T%d) error = %v, want errors.Is(err, ErrNotArmed)", tc.task, err)
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
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "ready", Status: model.StatusReady, Thread: "agent"}})
	if err != nil {
		t.Fatalf("add ready task: %v", err)
	}
	want, err := st.StartRun(ctx, task.Number)
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
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "ready", Status: model.StatusReady, Thread: "agent"}})
	if err != nil {
		t.Fatalf("add ready task: %v", err)
	}
	if _, err := st.StartRun(ctx, task.Number); err != nil {
		t.Fatalf("start first run: %v", err)
	}
	ready := model.StatusReady
	if _, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Status: &ready}); err != nil {
		t.Fatalf("re-arm task: %v", err)
	}
	want, err := st.StartRun(ctx, task.Number)
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

func TestRunWroteRequiresAnEventWithBothRunAndSession(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "ready", Status: model.StatusReady, Thread: "agent"}})
	if err != nil {
		t.Fatalf("add ready task: %v", err)
	}
	run, err := st.StartRun(ctx, task.Number)
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
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
