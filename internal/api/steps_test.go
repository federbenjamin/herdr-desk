package api_test

import (
	"context"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestClientStepReturnsTheChangedResultFromTheHome(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := testutil.ClientFor(testutil.NewClientMachine(t, home))
	task, err := client.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "steps"}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}

	withStep, changed, err := client.Step(ctx, store.Actor{}, task.Number, model.StepOp{Op: "add", ShortID: "review", Text: "review output"})
	if err != nil {
		t.Fatalf("Step(add) error: %v", err)
	}
	if !changed || len(withStep.Steps) != 1 || withStep.Steps[0].ShortID != "review" || withStep.Steps[0].Done {
		t.Fatalf("Step(add) = (%#v, %t), want one unfinished review step and changed true", withStep, changed)
	}

	done, changed, err := client.Step(ctx, store.Actor{}, task.Number, model.StepOp{Op: "done", ShortID: "review"})
	if err != nil {
		t.Fatalf("Step(done) error: %v", err)
	}
	if !changed || len(done.Steps) != 1 || !done.Steps[0].Done {
		t.Fatalf("Step(done) = (%#v, %t), want the completed step and changed true", done, changed)
	}

	alreadyDone, changed, err := client.Step(ctx, store.Actor{}, task.Number, model.StepOp{Op: "done", ShortID: "review"})
	if err != nil {
		t.Fatalf("Step(done) when already done error: %v", err)
	}
	if changed || len(alreadyDone.Steps) != 1 || !alreadyDone.Steps[0].Done {
		t.Errorf("Step(done) when already done = (%#v, %t), want the completed step and changed false", alreadyDone, changed)
	}
}
