package api_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
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

// Wire version 1 answered tasks.steps with a bare Task, which decodes into StepResult as a zero task without an error.
// A client of that version is refused with the version message, never handed a StepResult it would misread, and its
// step is not written.
func TestAVersionOneClientsStepIsRefusedByTheVersionCheck(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	task, err := home.Client().AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "steps"}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	m := testutil.NewMachine(t)
	old := api.NewClient(api.ClientOptions{Paths: m.Paths, Config: config.Default(),
		Transport: &skewTransport{home: home, cfg: homeConfig(t, home), version: 1}})

	_, _, err = old.Step(ctx, store.Actor{}, task.Number, model.StepOp{Op: "add", Text: "x"})
	var re *api.RPCError
	if !errors.As(err, &re) || re.BadRequest || !strings.Contains(re.Message, "install the same herdr-desk on both") {
		t.Fatalf("a version 1 client's Step = %v, want the wire-version refusal", err)
	}
	got, err := home.Client().GetTask(ctx, task.Number)
	if err != nil || len(got.Task.Steps) != 0 {
		t.Fatalf("T%d after the refused step = (%d steps, %v), want none", task.Number, len(got.Task.Steps), err)
	}
}
