package store_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestSetTaskRefusesANotesSaveFromAnOlderReadWithoutWriting(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	task := mustAdd(t, st, "shared notes", model.StatusOpen, "")

	first, original := "theirs", task.Notes
	if _, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Notes: &first, NotesWere: &original}); err != nil {
		t.Fatalf("SetTask(first notes save) error = %v", err)
	}
	before, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("GetTask() before stale save error = %v", err)
	}

	mine := "mine"
	_, err = st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Notes: &mine, NotesWere: &original})
	assertRefusalCode(t, err, model.CodeStale)

	after, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("GetTask() after stale save error = %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("a stale notes save changed the task or history:\n got: %#v\nwant: %#v", after, before)
	}
}

func TestSetTaskRecordsNotesButNeverItsReadPrecondition(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	task := mustAdd(t, st, "record notes", model.StatusOpen, "")

	notes, read := "saved", task.Notes
	if _, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Notes: &notes, NotesWere: &read}); err != nil {
		t.Fatalf("SetTask(notes save) error = %v", err)
	}
	detail, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if len(detail.History) != 2 {
		t.Fatalf("history length = %d, want creation and notes save", len(detail.History))
	}
	var recorded model.Patch
	if err := json.Unmarshal(detail.History[1].Data, &recorded); err != nil {
		t.Fatalf("decode set event: %v", err)
	}
	if recorded.Notes == nil || *recorded.Notes != notes || recorded.NotesWere != nil {
		t.Fatalf("recorded patch = %#v, want notes without NotesWere", recorded)
	}

	unconditional := "replace without a precondition"
	if _, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Notes: &unconditional}); err != nil {
		t.Fatalf("SetTask(notes without NotesWere) error = %v", err)
	}
}
