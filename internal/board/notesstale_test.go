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
	wantEffects(t, save, []board.Effect{board.SetTask{Task: 41, Patch: model.Patch{
		Notes:     w4String("before mine"),
		NotesWere: w4String("before"),
	}}})

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

// The other board's save landed after this board's last load: the refusal loads the task, and the second ctrl+s
// names the notes that load brought, so it replaces them with no refresh between.
func TestTaskPageAStaleNotesSaveLoadsTheHomesNotesForTheSecondSave(t *testing.T) {
	task := model.Task{Number: 41, Title: "Shared draft", Notes: "before", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(tea.PasteMsg{Content: " mine"})
	s, save := s.Update(ctrl('s'))

	s, load := s.Update(board.FailedOf(save[0], &model.Refusal{Code: model.CodeStale, Msg: "notes changed"}))
	wantEffects(t, load, []board.Effect{board.LoadTask{Task: 41}})
	theirs := task
	theirs.Notes = "theirs"
	s, _ = s.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: theirs}})
	if text := s.Text(); !strings.Contains(text, "before mine") {
		t.Fatalf("after the load = %q, want the typed text kept", text)
	}

	_, retry := s.Update(ctrl('s'))
	wantEffects(t, retry, []board.Effect{board.SetTask{Task: 41, Patch: model.Patch{
		Notes:     w4String("before mine"),
		NotesWere: w4String("theirs"),
	}}})
}

// A stale refusal that arrives while a prompt has the keys waits for it to close; the load that answers meanwhile
// still gives the waiting text the home's notes.
func TestTaskPageAStaleNotesSaveWaitingForAPromptTakesTheHomesNotes(t *testing.T) {
	task := model.Task{Number: 41, Title: "Shared draft", Notes: "before", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(tea.PasteMsg{Content: " mine"})
	s, save := s.Update(ctrl('s'))
	s, _ = s.Update(press('R'))
	s, _ = s.Update(board.FailedOf(save[0], &model.Refusal{Code: model.CodeStale, Msg: "notes changed"}))
	theirs := task
	theirs.Notes = "theirs"
	s, _ = s.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: theirs}})
	s, _ = s.Update(named(tea.KeyEsc))
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.Contains(text, "before mine") {
		t.Fatalf("after the prompt closed = %q, want the editor back with the typed text", text)
	}

	_, retry := s.Update(ctrl('s'))
	wantEffects(t, retry, []board.Effect{board.SetTask{Task: 41, Patch: model.Patch{
		Notes:     w4String("before mine"),
		NotesWere: w4String("theirs"),
	}}})
}

// A load while the editor is open with no refusal behind it leaves the notes the save says it read: a save over
// notes written since must still be refused stale.
func TestTaskPageALoadWhileEditingKeepsTheNotesTheEditorStartedFrom(t *testing.T) {
	task := model.Task{Number: 41, Title: "Shared draft", Notes: "before", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(tea.PasteMsg{Content: " mine"})
	theirs := task
	theirs.Notes = "theirs"
	s, _ = s.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: theirs}})

	_, save := s.Update(ctrl('s'))
	wantEffects(t, save, []board.Effect{board.SetTask{Task: 41, Patch: model.Patch{
		Notes:     w4String("before mine"),
		NotesWere: w4String("before"),
	}}})
}
