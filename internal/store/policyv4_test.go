package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestAgentReadyNeedsAutoStartButDoneAndAgentThreadRemainAllowed(t *testing.T) {
	ctx := context.Background()
	agent := store.Actor{Session: "worker-session"}

	t.Run("ready is refused when auto start is off", func(t *testing.T) {
		st := openStore(t, store.Options{})
		task := addOpenTask(t, st)
		before := eventCount(t, st)

		_, err := st.SetTask(ctx, agent, task.Number, model.Patch{Status: statusPtr(model.StatusReady)})
		if got := refusalCode(t, err); got != model.CodeNotAllowed {
			t.Fatalf("agent ready refusal = %q, want %q", got, model.CodeNotAllowed)
		}
		if got := eventCount(t, st); got != before {
			t.Fatalf("events after refused ready = %d, want %d", got, before)
		}
	})

	t.Run("auto start lets an agent ready an agent thread", func(t *testing.T) {
		st := openStore(t, store.Options{AutoStart: true})
		task := addOpenTask(t, st)
		thread := "agent"

		updated, err := st.SetTask(ctx, agent, task.Number, model.Patch{
			Status: statusPtr(model.StatusReady),
			Thread: &thread,
		})
		if err != nil {
			t.Fatalf("agent ready an agent thread: %v", err)
		}
		if updated.Status != model.StatusReady || updated.Thread != thread {
			t.Fatalf("updated task = %#v, want ready task with thread %q", updated, thread)
		}
	})

	t.Run("done does not depend on auto start", func(t *testing.T) {
		st := openStore(t, store.Options{})
		task := addOpenTask(t, st)

		updated, err := st.SetTask(ctx, agent, task.Number, model.Patch{Status: statusPtr(model.StatusDone)})
		if err != nil {
			t.Fatalf("agent set done: %v", err)
		}
		if updated.Status != model.StatusDone {
			t.Fatalf("status = %q, want %q", updated.Status, model.StatusDone)
		}
	})
}

func TestSetCoordinatorRejectsAgentsAndKeepsCursorAcrossReplacement(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	first := model.Coordinator{Session: "coord-one", Workspace: "workspace-one", Pane: "%1"}

	if err := st.SetCoordinator(ctx, store.Actor{Session: "agent-session"}, first); err == nil {
		t.Fatal("agent SetCoordinator() error = nil, want not-allowed")
	} else if got := refusalCode(t, err); got != model.CodeNotAllowed {
		t.Fatalf("agent SetCoordinator() refusal = %q, want %q", got, model.CodeNotAllowed)
	}
	if _, ok, err := st.Coordinator(ctx); err != nil {
		t.Fatalf("Coordinator() after refused write: %v", err)
	} else if ok {
		t.Fatal("Coordinator() found a row after refused agent write")
	}

	if err := st.SetCoordinator(ctx, store.Actor{}, first); err != nil {
		t.Fatalf("record first coordinator: %v", err)
	}
	if _, err := st.Note(ctx, store.Actor{}, store.NoteInput{NoteData: model.NoteData{Text: "first event"}}); err != nil {
		t.Fatalf("add event: %v", err)
	}
	if _, err := st.Changes(ctx, store.Actor{Session: first.Session}, 0); err != nil {
		t.Fatalf("advance first coordinator cursor: %v", err)
	}

	second := model.Coordinator{Session: "coord-two", Workspace: "workspace-two", Pane: "%2"}
	if err := st.SetCoordinator(ctx, store.Actor{}, second); err != nil {
		t.Fatalf("replace coordinator: %v", err)
	}
	got, ok, err := st.Coordinator(ctx)
	if err != nil {
		t.Fatalf("read replacement coordinator: %v", err)
	}
	if !ok {
		t.Fatal("Coordinator() ok = false after person records it")
	}
	want := second
	want.Cursor = 1
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coordinator = %#v, want %#v", got, want)
	}
}

func TestChangesAdvancesCursorOnlyForRecordedCoordinatorSession(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	for _, text := range []string{"one", "two", "three"} {
		if _, err := st.Note(ctx, store.Actor{}, store.NoteInput{NoteData: model.NoteData{Text: text}}); err != nil {
			t.Fatalf("add %q event: %v", text, err)
		}
	}
	coordinator := model.Coordinator{Session: "coordinator-session", Workspace: "workspace", Pane: "%3"}
	if err := st.SetCoordinator(ctx, store.Actor{}, coordinator); err != nil {
		t.Fatalf("record coordinator: %v", err)
	}

	other, err := st.Changes(ctx, store.Actor{Session: "other-session"}, 2)
	if err != nil {
		t.Fatalf("other session changes: %v", err)
	}
	if other.From != 0 || other.To != 3 || other.LeftOut != 1 {
		t.Fatalf("other changes bounds = %#v, want From 0, To 3, LeftOut 1", other)
	}
	if got := eventIDs(other.Events); !reflect.DeepEqual(got, []int64{2, 3}) {
		t.Fatalf("other event ids = %v, want [2 3]", got)
	}
	if got, ok, err := st.Coordinator(ctx); err != nil {
		t.Fatalf("read coordinator after other session: %v", err)
	} else if !ok || got.Cursor != 0 {
		t.Fatalf("cursor after other session = %#v, want recorded cursor 0", got)
	}

	owned, err := st.Changes(ctx, store.Actor{Session: coordinator.Session}, 2)
	if err != nil {
		t.Fatalf("coordinator changes: %v", err)
	}
	if got := eventIDs(owned.Events); !reflect.DeepEqual(got, []int64{2, 3}) {
		t.Fatalf("coordinator event ids = %v, want [2 3]", got)
	}
	if got, ok, err := st.Coordinator(ctx); err != nil {
		t.Fatalf("read advanced coordinator: %v", err)
	} else if !ok || got.Cursor != 3 {
		t.Fatalf("cursor after coordinator read = %#v, want recorded cursor 3", got)
	}

	if _, err := st.Note(ctx, store.Actor{}, store.NoteInput{NoteData: model.NoteData{Text: "four"}}); err != nil {
		t.Fatalf("add fourth event: %v", err)
	}
	next, err := st.Changes(ctx, store.Actor{Session: coordinator.Session}, 0)
	if err != nil {
		t.Fatalf("next coordinator changes: %v", err)
	}
	if next.From != 3 || next.To != 4 || next.LeftOut != 0 || !reflect.DeepEqual(eventIDs(next.Events), []int64{4}) {
		t.Fatalf("next changes = %#v, want only event 4 after cursor 3", next)
	}
}

func eventIDs(events []model.Event) []int64 {
	ids := make([]int64, len(events))
	for i, event := range events {
		ids[i] = event.ID
	}
	return ids
}
