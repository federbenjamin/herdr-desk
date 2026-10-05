package board_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/federbenjamin/herdr-desk/internal/board"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestTaskPageAStaleNotesSaveReopensAgainstTheNotesLastLoaded(t *testing.T) {
	task := model.Task{Number: 41, Title: "Shared draft", Notes: "before", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(tea.PasteMsg{Content: " mine"})
	s, save := s.Update(ctrl('s'))
	if len(save) != 1 {
		t.Fatalf("notes save effects = %#v, want one SetTask", save)
	}

	theirs := task
	theirs.Notes = "theirs"
	s, _ = s.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: theirs}})
	s, _ = s.Update(board.FailedOf(save[0], &model.Refusal{Code: model.CodeStale, Msg: "notes changed"}))
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.Contains(text, "before mine") || !strings.HasSuffix(text, "T41's notes changed while you edited: ctrl+s replaces them, esc keeps them") {
		t.Fatalf("stale notes save = %q, want the editor, typed text, and overwrite guidance", text)
	}

	_, retry := s.Update(ctrl('s'))
	wantEffects(t, retry, []board.Effect{board.SetTask{Task: 41, Patch: model.Patch{
		Notes:     w4String("before mine"),
		NotesWere: w4String("theirs"),
	}}})
}
