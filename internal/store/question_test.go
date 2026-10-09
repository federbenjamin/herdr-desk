package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// runningWithPane starts a run of a new task and moves it to running on pane.
func runningWithPane(t *testing.T, st *store.Store, pane string) (model.Task, model.Run) {
	t.Helper()
	ctx := context.Background()
	task := mustAdd(t, st, "asks in text", model.StatusReady, "")
	run, err := st.StartRun(ctx, task.Number, policyRoute, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if ok, err := st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{State: model.RunRunning, Pane: pane}); err != nil || !ok {
		t.Fatalf("run it = (%t, %v)", ok, err)
	}
	run.State, run.Pane = model.RunRunning, pane
	return task, run
}

// notesOf returns the text of every note in the task's history, oldest first.
func notesOf(t *testing.T, st *store.Store, task int) []string {
	t.Helper()
	d, err := st.GetTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range d.History {
		if e.Kind != model.KindNote {
			continue
		}
		var n model.NoteData
		if err := json.Unmarshal(e.Data, &n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n.Text)
	}
	return out
}

func TestSetTaskBlockedWithAQuestionKeepsTheRunAndNamesItsPane(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, run := runningWithPane(t, st, "w2Z:p1")
	worker := store.Actor{Session: "worker", Run: run.ID}
	blocked := model.StatusBlocked

	got, err := st.SetTask(ctx, worker, task.Number, model.Patch{Status: &blocked, Question: "pick the support address"})
	if err != nil || got.Status != model.StatusBlocked {
		t.Fatalf("set blocked with a question = (%q, %v), want blocked", got.Status, err)
	}
	cur, ok, err := st.CurrentRun(ctx, task.Number)
	if err != nil || !ok || cur.ID != run.ID || cur.State != model.RunRunning {
		t.Fatalf("run after the question = (%#v, %t, %v), want run %d still running", cur, ok, err, run.ID)
	}
	want := model.WaitingNote("w2Z:p1") + ": pick the support address"
	if notes := notesOf(t, st, task.Number); len(notes) == 0 || notes[len(notes)-1] != want {
		t.Fatalf("notes = %q, want the last to be %q", notes, want)
	}

	before := eventCount(t, st)
	if _, err := st.SetTask(ctx, worker, task.Number, model.Patch{Status: &blocked, Question: "and the sender name"}); err != nil {
		t.Fatalf("a second question: %v", err)
	}
	if after := eventCount(t, st); after != before+1 {
		t.Fatalf("events after a second question on a blocked task = %d, want the note only (%d)", after, before+1)
	}
	if cur, _, _ := st.CurrentRun(ctx, task.Number); cur.State != model.RunRunning {
		t.Fatalf("run after a second question = %q, want running", cur.State)
	}

	if _, err := st.SetTask(ctx, worker, task.Number, model.Patch{Status: &blocked}); err != nil {
		t.Fatalf("worker hands back blocked: %v", err)
	}
	if cur, _, _ := st.CurrentRun(ctx, task.Number); cur.State != model.RunEnded {
		t.Fatalf("run after a blocked hand-back with no question = %q, want ended", cur.State)
	}
}

func TestSetTaskQuestionWithoutARunNamesNoPane(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task := mustAdd(t, st, "no run", model.StatusOpen, "")
	blocked := model.StatusBlocked
	if _, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Status: &blocked, Question: "which repo?"}); err != nil {
		t.Fatalf("set blocked with a question: %v", err)
	}
	if notes := notesOf(t, st, task.Number); len(notes) != 1 || notes[0] != "waiting for an answer: which repo?" {
		t.Fatalf("notes = %q, want the question with no pane", notes)
	}
}

func TestSetTaskRefusesAQuestionWithoutBlockedOrText(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task := mustAdd(t, st, "bad question", model.StatusOpen, "")
	review, blocked := model.StatusReview, model.StatusBlocked
	before := eventCount(t, st)
	for _, tc := range []struct {
		name string
		p    model.Patch
		code string
	}{
		{"no status", model.Patch{Question: "why?"}, model.CodeBadInput},
		{"review", model.Patch{Status: &review, Question: "why?"}, model.CodeBadInput},
		{"blank", model.Patch{Status: &blocked, Question: "  "}, model.CodeEmptyText},
	} {
		_, err := st.SetTask(ctx, store.Actor{}, task.Number, tc.p)
		if got := refusalCode(t, err); got != tc.code {
			t.Fatalf("%s: refusal = %q, want %q", tc.name, got, tc.code)
		}
	}
	if after := eventCount(t, st); after != before {
		t.Fatalf("events after refused questions = %d, want %d", after, before)
	}
}

func TestHandBackThatLeavesARunLiveWritesNoNoteOutOfIfStatus(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, run := runningWithPane(t, st, "w2Z:p1")
	blocked := model.StatusBlocked
	if _, err := st.SetTask(ctx, store.Actor{Session: "worker", Run: run.ID}, task.Number,
		model.Patch{Status: &blocked, Question: "pick the support address"}); err != nil {
		t.Fatalf("set blocked with a question: %v", err)
	}
	before := len(notesOf(t, st, task.Number))

	got, claimed, err := st.HandBack(ctx, run, store.HandBack{From: model.RunRunning, To: model.RunIdle, Status: model.StatusReview,
		IfStatus: model.StatusStarted, Tags: []string{model.TagRunner}, Note: "went idle without handing back"})
	if err != nil || !claimed || got.Status != model.StatusBlocked {
		t.Fatalf("idle hand-back = (%q, %t, %v), want claimed with the task still blocked", got.Status, claimed, err)
	}
	if cur, _, _ := st.CurrentRun(ctx, task.Number); cur.State != model.RunIdle {
		t.Fatalf("run after the idle hand-back = %q, want idle", cur.State)
	}
	if notes := notesOf(t, st, task.Number); len(notes) != before {
		t.Fatalf("notes after the idle hand-back = %q, want none added", notes)
	}
}
