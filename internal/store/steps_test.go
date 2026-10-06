package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestW2StepKeepsCallerIDsOutOfTheGeneratedSequence(t *testing.T) {
	t.Parallel()

	st := w2OpenStore(t)
	ctx := context.Background()
	task := w2AddTask(t, st, "caller ids")

	for _, op := range []model.StepOp{
		{Op: "add", ShortID: "w2-custom-one", Text: "caller supplied"},
		{Op: "add", Text: "first generated"},
		{Op: "add", ShortID: "w2-custom-two", Text: "another caller id"},
		{Op: "add", Text: "second generated"},
	} {
		updated, changed, err := st.Step(ctx, store.Actor{}, task.Number, op)
		if err != nil {
			t.Fatalf("Step(%#v) error = %v", op, err)
		}
		if !changed {
			t.Fatalf("Step(%#v) changed = false, want true", op)
		}
		task = updated
	}

	if got := []string{task.Steps[0].ShortID, task.Steps[1].ShortID, task.Steps[2].ShortID, task.Steps[3].ShortID}; !w2StringsEqual(got, []string{"w2-custom-one", "s1", "w2-custom-two", "s2"}) {
		t.Errorf("step IDs = %v, want caller IDs outside generated sequence", got)
	}
}

func TestW2StepRejectsInvalidOrGeneratedCallerIDsWithoutWriting(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		id   string
	}{
		{name: "generated-looking id", id: "s1"},
		{name: "invalid punctuation", id: "w2/not-an-id"},
		{name: "longer than sixty-four bytes", id: strings.Repeat("a", 65)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := w2OpenStore(t)
			ctx := context.Background()
			task := w2AddTask(t, st, "reject caller id")

			_, changed, err := st.Step(ctx, store.Actor{}, task.Number, model.StepOp{Op: "add", ShortID: tc.id, Text: "must not write"})
			w2AssertRefusal(t, err, model.CodeBadInput)
			if changed {
				t.Errorf("Step(add %q) changed = true, want false", tc.id)
			}
			detail, err := st.GetTask(ctx, task.Number)
			if err != nil {
				t.Fatalf("GetTask() error = %v", err)
			}
			if len(detail.History) != 1 || len(detail.Task.Steps) != 0 {
				t.Errorf("invalid caller id left history=%d steps=%#v, want only the creation event and no steps", len(detail.History), detail.Task.Steps)
			}
		})
	}
}

func TestW2StepSecondDoneIsUnchangedAndWritesNoEvent(t *testing.T) {
	t.Parallel()

	st := w2OpenStore(t)
	ctx := context.Background()
	task := w2AddTask(t, st, "complete once")
	_, changed, err := st.Step(ctx, store.Actor{}, task.Number, model.StepOp{Op: "add", ShortID: "w2-step", Text: "finish this"})
	if err != nil {
		t.Fatalf("Step(add) error = %v", err)
	}
	if !changed {
		t.Fatal("Step(add) changed = false, want true")
	}

	completed, changed, err := st.Step(ctx, store.Actor{}, task.Number, model.StepOp{Op: "done", ShortID: "w2-step"})
	if err != nil {
		t.Fatalf("first Step(done) error = %v", err)
	}
	if !changed || len(completed.Steps) != 1 || !completed.Steps[0].Done {
		t.Errorf("first Step(done) = (%#v, %t), want the one step done and changed", completed.Steps, changed)
	}

	unchanged, changed, err := st.Step(ctx, store.Actor{}, task.Number, model.StepOp{Op: "done", ShortID: "w2-step"})
	if err != nil {
		t.Fatalf("second Step(done) error = %v", err)
	}
	if changed || len(unchanged.Steps) != 1 || !unchanged.Steps[0].Done {
		t.Errorf("second Step(done) = (%#v, %t), want the existing done step and unchanged", unchanged.Steps, changed)
	}

	detail, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if len(detail.History) != 3 {
		t.Errorf("history after repeated done = %d, want creation, add, and one done event", len(detail.History))
	}
}

func TestW2StepRefusesOlderRunButAllowsAPerson(t *testing.T) {
	t.Parallel()

	st := w2OpenStore(t)
	ctx := context.Background()
	task := w2AddTask(t, st, "stale step write")
	first, err := w2StartRun(ctx, st, task.Number)
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if _, err := st.UpdateRun(ctx, first.ID, model.RunStarting, store.RunUpdate{State: model.RunEnded}); err != nil {
		t.Fatalf("end first run: %v", err)
	}
	if _, err := w2StartRun(ctx, st, task.Number); err != nil {
		t.Fatalf("start newer run: %v", err)
	}
	before, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("GetTask() before stale write error = %v", err)
	}

	_, changed, err := st.Step(ctx, store.Actor{Session: "w2-old-run", Run: first.ID}, task.Number, model.StepOp{Op: "add", ShortID: "w2-stale", Text: "must not write"})
	w2AssertRefusal(t, err, model.CodeStaleRun)
	if changed {
		t.Error("Step() from an older run changed = true, want false")
	}
	after, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("GetTask() after stale write error = %v", err)
	}
	if len(after.History) != len(before.History) || len(after.Task.Steps) != 0 {
		t.Errorf("stale Step() left history=%d steps=%#v, want history=%d and no steps", len(after.History), after.Task.Steps, len(before.History))
	}

	updated, changed, err := st.Step(ctx, store.Actor{}, task.Number, model.StepOp{Op: "add", ShortID: "w2-person", Text: "person may write"})
	if err != nil {
		t.Fatalf("Step() from a person error = %v", err)
	}
	if !changed || len(updated.Steps) != 1 || updated.Steps[0].ShortID != "w2-person" {
		t.Errorf("Step() from a person = (%#v, %t), want one written step and changed", updated.Steps, changed)
	}
}

func w2OpenStore(t *testing.T) *store.Store {
	t.Helper()
	machine := testutil.NewMachine(t)
	st, err := store.Open(machine.Paths.DB(), store.Options{Now: func() time.Time {
		return time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)
	}})
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

func w2AddTask(t *testing.T, st *store.Store, title string) model.Task {
	t.Helper()
	task, err := st.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title:  title,
		Status: model.StatusReady,
	}})
	if err != nil {
		t.Fatalf("AddTask(%q) error = %v", title, err)
	}
	return task
}

func w2StartRun(ctx context.Context, st *store.Store, task int) (model.Run, error) {
	return st.StartRun(ctx, task, store.RunRoute{Root: "/work/w2", Isolation: "worktree", Model: "w2-model"}, store.RunCaps{Slots: 100, PerDay: 1000})
}

func w2AssertRefusal(t *testing.T, err error, want string) {
	t.Helper()
	refusal, ok := model.AsRefusal(err)
	if !ok {
		t.Fatalf("error = %v, want refusal %q", err, want)
	}
	if refusal.Code != want {
		t.Errorf("refusal code = %q, want %q", refusal.Code, want)
	}
}

func w2StringsEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
