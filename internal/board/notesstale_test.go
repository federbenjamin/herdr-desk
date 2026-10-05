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

// staleHome refuses every notes save stale and holds the notes another board wrote.
func staleHome(task model.Task, notes string) *fakeHome {
	theirs := task
	theirs.Notes = notes
	return &fakeHome{set: &model.Refusal{Code: model.CodeStale, Msg: "notes changed"}, detail: store.TaskDetail{Task: theirs}}
}

// The other board's save landed after this board's last load: the executor reads the home's notes as the stale
// refusal comes back, so a ctrl+s pressed at once names them and replaces them, with no refresh between.
func TestTaskPageAStaleNotesSaveReopensOnTheHomesNotesForAnImmediateSecondSave(t *testing.T) {
	task := model.Task{Number: 41, Title: "Shared draft", Notes: "before", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(tea.PasteMsg{Content: " mine"})
	s, save := s.Update(ctrl('s'))

	s, more := board.Feed(s, board.Answer(staleHome(task, "theirs"), save[0]))
	if len(more) != 0 {
		t.Fatalf("effects after the refusal = %#v, want none: the second save waits on no load", more)
	}
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.Contains(text, "before mine") {
		t.Fatalf("after the refusal = %q, want the editor back with the typed text", text)
	}

	_, retry := s.Update(ctrl('s'))
	wantEffects(t, retry, []board.Effect{board.SetTask{Task: 41, Patch: model.Patch{
		Notes:     w4String("before mine"),
		NotesWere: w4String("theirs"),
	}}})
}

// A stale refusal that arrives while a prompt has the keys waits for it to close, still holding the home's notes.
func TestTaskPageAStaleNotesSaveWaitingForAPromptKeepsTheHomesNotes(t *testing.T) {
	task := model.Task{Number: 41, Title: "Shared draft", Notes: "before", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(tea.PasteMsg{Content: " mine"})
	s, save := s.Update(ctrl('s'))
	s, _ = s.Update(press('R'))
	s, _ = board.Feed(s, board.Answer(staleHome(task, "theirs"), save[0]))
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
