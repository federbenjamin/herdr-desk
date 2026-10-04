package board_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/board"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

var w4Now = time.Date(2026, time.March, 14, 15, 9, 0, 0, time.Local)

func TestTaskPageTextShowsTaskFieldsNotesStepsHistoryAndFiles(t *testing.T) {
	task := model.Task{
		Number:    42,
		Title:     "Ship the task page",
		Notes:     "Keep the history readable.",
		Status:    model.StatusStarted,
		Project:   "/work/desk",
		Thread:    "agent",
		Root:      "/work/desk",
		Isolation: "worktree",
		Model:     "gpt-5",
		Steps: []model.Step{
			{ShortID: "s1", Text: "draw the page", Done: true},
			{ShortID: "s2", Text: "open refs"},
		},
	}
	ready := model.StatusReady
	history := []model.Event{
		{
			TS:      w4Now.Add(-2 * time.Hour),
			Session: "session-one",
			Who:     model.WhoUser,
			Kind:    model.KindTask,
			Data:    model.MustData(model.TaskData{Status: model.StatusOpen}),
		},
		{
			TS:      w4Now.Add(-time.Hour),
			Session: "session-two",
			Who:     model.WhoAgent,
			Kind:    model.KindSet,
			Data:    model.MustData(model.Patch{Status: &ready, Ref: "https://example.test/pr/42", Merged: true}),
		},
		{
			TS:      w4Now.Add(-30 * time.Minute),
			Session: "session-two",
			Who:     model.WhoAgent,
			Kind:    model.KindNote,
			Data:    model.MustData(model.NoteData{Text: "reviewed", Ref: "docs/task.md"}),
		},
		{
			TS:      w4Now.Add(-20 * time.Minute),
			Session: "session-two",
			Who:     model.WhoUser,
			Kind:    model.KindDecision,
			Data:    model.MustData(model.DecisionData{Text: "keep the plain text screen"}),
		},
		{
			TS:      w4Now.Add(-10 * time.Minute),
			Session: "session-two",
			Who:     model.WhoUser,
			Kind:    model.KindMerged,
			Data:    model.MustData(model.MergedData{Branch: "feature/task-page"}),
		},
	}

	s := w4TaskPage(t, 90, task, history)
	text := s.Text()
	for _, want := range []string{
		"T42  started  Ship the task page",
		"desk · #agent",
		"root /work/desk · isolation worktree · model gpt-5",
		"NOTES",
		"Keep the history readable.",
		"STEPS  1/2",
		"[x] draw the page",
		"[ ] open refs",
		"HISTORY  across 2 sessions",
		"you     created · open",
		"agent   ready · merged [https://example.test/pr/42]",
		"agent   note \"reviewed\" [docs/task.md]",
		"you     decision keep the plain text screen",
		"you     merged feature/task-page",
		"FILES",
		"https://example.test/pr/42",
		"docs/task.md",
		"e notes  t steps  n ready  x done  o open  R root  I isolation  M model  f focus  esc back",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("task page text missing %q:\n%s", want, text)
		}
	}
}

func TestTaskPageEditorsAndIsolationEmitSetTask(t *testing.T) {
	task := model.Task{
		Number:  7,
		Title:   "Edit fields",
		Notes:   "existing note",
		Status:  model.StatusOpen,
		Root:    "/work/desk",
		Model:   "gpt-5",
		Project: "/work/desk",
	}

	for _, tc := range []struct {
		name string
		keys []tea.KeyPressMsg
		want board.Effect
	}{
		{
			name: "saving notes preserves the editor contents",
			keys: []tea.KeyPressMsg{
				press('e'),
				ctrl('s'),
			},
			want: board.SetTask{Task: 7, Patch: model.Patch{Notes: w4String("existing note")}},
		},
		{
			name: "submitting root preserves its populated value",
			keys: []tea.KeyPressMsg{
				press('R'),
				{Code: tea.KeyEnter},
			},
			want: board.SetTask{Task: 7, Patch: model.Patch{Root: w4String("/work/desk")}},
		},
		{
			name: "isolation advances from unset to self",
			keys: []tea.KeyPressMsg{press('I')},
			want: board.SetTask{Task: 7, Patch: model.Patch{Isolation: w4String("self")}},
		},
		{
			name: "submitting model preserves its populated value",
			keys: []tea.KeyPressMsg{
				press('M'),
				{Code: tea.KeyEnter},
			},
			want: board.SetTask{Task: 7, Patch: model.Patch{Model: w4String("gpt-5")}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := w4TaskPage(t, 90, task, nil)
			var effects []board.Effect
			for _, key := range tc.keys {
				s, effects = s.Update(key)
			}
			wantEffects(t, effects, []board.Effect{tc.want})
		})
	}
}

func TestTaskPageHistoryNamesFalseArchiveAsUnarchived(t *testing.T) {
	archived := false
	task := model.Task{Number: 43, Title: "Restore task", Status: model.StatusOpen}
	history := []model.Event{{
		TS:   w4Now,
		Kind: model.KindSet,
		Data: model.MustData(model.Patch{Archived: &archived}),
	}}

	text := w4TaskPage(t, 90, task, history).Text()
	if !strings.Contains(text, "unarchived") {
		t.Fatalf("false archive history = %q, want unarchived", text)
	}
}

func TestTaskPagePasteAppendsToTheOpenNotesEditor(t *testing.T) {
	task := model.Task{Number: 44, Title: "Paste a note", Notes: "existing", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)

	s, effects := s.Update(press('e'))
	wantEffects(t, effects, nil)
	s, effects = s.Update(tea.PasteMsg{Content: " pasted"})
	wantEffects(t, effects, nil)
	_, effects = s.Update(ctrl('s'))
	wantEffects(t, effects, []board.Effect{board.SetTask{
		Task:  44,
		Patch: model.Patch{Notes: w4String("existing pasted")},
	}})
}

func TestTaskPageStepKeysEmitTheirNamedOperations(t *testing.T) {
	task := model.Task{
		Number:  8,
		Title:   "Change steps",
		Status:  model.StatusOpen,
		Project: "/work/desk",
		Steps: []model.Step{
			{ShortID: "s1", Text: "first"},
			{ShortID: "s2", Text: "second"},
		},
	}

	for _, tc := range []struct {
		name string
		keys []tea.KeyPressMsg
		want board.Effect
	}{
		{
			name: "space toggles the first step",
			keys: []tea.KeyPressMsg{press('t'), {Code: tea.KeySpace}},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "toggle", ShortID: "s1"}},
		},
		{
			name: "enter toggles the step reached with down",
			keys: []tea.KeyPressMsg{press('t'), {Code: tea.KeyDown}, {Code: tea.KeyEnter}},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "toggle", ShortID: "s2"}},
		},
		{
			name: "up returns selection to the preceding step",
			keys: []tea.KeyPressMsg{press('t'), {Code: tea.KeyDown}, {Code: tea.KeyUp}, {Code: tea.KeyEnter}},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "toggle", ShortID: "s1"}},
		},
		{
			name: "adding text creates an add operation",
			keys: []tea.KeyPressMsg{press('t'), press('a'), press('n'), press('e'), press('w'), {Code: tea.KeyEnter}},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "add", Text: "new"}},
		},
		{
			name: "renaming a step submits its populated text",
			keys: []tea.KeyPressMsg{press('t'), press('r'), {Code: tea.KeyEnter}},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "rename", ShortID: "s1", Text: "first"}},
		},
		{
			name: "removing a step uses its short id",
			keys: []tea.KeyPressMsg{press('t'), press('x')},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "remove", ShortID: "s1"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := w4TaskPage(t, 90, task, nil)
			var effects []board.Effect
			for _, key := range tc.keys {
				s, effects = s.Update(key)
			}
			wantEffects(t, effects, []board.Effect{tc.want})
		})
	}
}

func TestTaskPageOpenRefUsesOneRefOrAPickList(t *testing.T) {
	task := model.Task{Number: 11, Title: "Open a ref", Status: model.StatusOpen, Project: "/work/desk"}
	oneRef := []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Text: "see it", Ref: "docs/one.md"})}}
	twoRefs := []model.Event{
		{Kind: model.KindNote, Data: model.MustData(model.NoteData{Text: "first", Ref: "docs/one.md"})},
		{Kind: model.KindSet, Data: model.MustData(model.Patch{Ref: "docs/two.md"})},
	}

	t.Run("a single ref opens immediately", func(t *testing.T) {
		s := w4TaskPage(t, 90, task, oneRef)
		s, effects := s.Update(press('o'))
		_ = s
		wantEffects(t, effects, []board.Effect{board.OpenRef{Ref: "docs/one.md", Dir: "/work/desk"}})
	})

	t.Run("several refs select the navigated item", func(t *testing.T) {
		s := w4TaskPage(t, 90, task, twoRefs)
		s, effects := s.Update(press('o'))
		wantEffects(t, effects, nil)
		s, _ = s.Update(press('j'))
		_, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		wantEffects(t, effects, []board.Effect{board.OpenRef{Ref: "docs/two.md", Dir: "/work/desk"}})
	})

	t.Run("no refs explains why nothing opened", func(t *testing.T) {
		s := w4TaskPage(t, 90, task, nil)
		s, effects := s.Update(press('o'))
		wantEffects(t, effects, nil)
		if !strings.Contains(s.Text(), "T11 has no ref") {
			t.Errorf("no-ref status = %q, want T11 has no ref", s.Text())
		}
	})
}

func TestTaskPageNotesEditorDrawsEditsAndCancelsWithoutSaving(t *testing.T) {
	task := model.Task{Number: 12, Title: "Edit notes", Notes: "first\nsecond", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)

	s, effects := s.Update(press('e'))
	wantEffects(t, effects, nil)
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.Contains(text, "first\nsecond") {
		t.Fatalf("notes editor = %q, want editing notes", text)
	}
	if render := s.Render(); !strings.Contains(render, "first") || !strings.Contains(render, "second") {
		t.Fatalf("rendered notes editor = %q, want both note lines", render)
	}

	s, effects = s.Update(press('!'))
	wantEffects(t, effects, nil)
	if text := s.Text(); !strings.Contains(text, "first\nsecond!") {
		t.Fatalf("typed notes = %q, want appended character", text)
	}

	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	wantEffects(t, effects, nil)
	if text := s.Text(); strings.Contains(text, "editing") || !strings.Contains(text, "first\nsecond") {
		t.Fatalf("cancelled notes editor = %q, want original saved notes", text)
	}
}

func TestTaskPageAFailedNotesSaveOpensTheEditorAgainWithTheText(t *testing.T) {
	task := model.Task{Number: 21, Title: "Save notes", Notes: "draft", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(tea.PasteMsg{Content: " two"})
	s, effects := s.Update(ctrl('s'))
	wantEffects(t, effects, []board.Effect{board.SetTask{Task: 21, Patch: model.Patch{Notes: w4String("draft two")}}})
	if strings.Contains(s.Text(), "editing") {
		t.Fatalf("ctrl+s left the editor open: %q", s.Text())
	}
	s, _ = s.Update(board.FailedOf(effects[0], errors.New("secret-found: the notes hold a token")))
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.Contains(text, "draft two") || !strings.HasSuffix(text, "secret-found: the notes hold a token") {
		t.Fatalf("a failed save = %q, want the editor back with the text and the error", text)
	}
	_, effects = s.Update(ctrl('s'))
	wantEffects(t, effects, []board.Effect{board.SetTask{Task: 21, Patch: model.Patch{Notes: w4String("draft two")}}})
}

func TestTaskPageANotesSaveThatFailsWhileAPromptIsOpenWaitsForItToClose(t *testing.T) {
	task := model.Task{Number: 25, Title: "Save later", Notes: "draft", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(tea.PasteMsg{Content: " two"})
	s, save := s.Update(ctrl('s'))
	s, _ = s.Update(press('R'))
	s, _ = s.Update(board.FailedOf(save[0], errors.New("dial home: connection refused")))
	if text := s.Text(); strings.Contains(text, "editing") || !strings.Contains(text, "root: ") {
		t.Fatalf("a failed save took the keys from the open prompt: %q", text)
	}
	s, _ = s.Update(named(tea.KeyEsc))
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.Contains(text, "draft two") || !strings.HasSuffix(text, "dial home: connection refused") {
		t.Fatalf("after the prompt closed = %q, want the editor back with the text and the error", text)
	}
	_, effects := s.Update(ctrl('s'))
	wantEffects(t, effects, []board.Effect{board.SetTask{Task: 25, Patch: model.Patch{Notes: w4String("draft two")}}})
}

func TestTaskPageAFailedNotesSaveIsNotLostToALaterSave(t *testing.T) {
	task := model.Task{Number: 26, Title: "Save twice", Notes: "draft", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(tea.PasteMsg{Content: " two"})
	s, save := s.Update(ctrl('s'))
	s, _ = s.Update(press('e'))
	s, _ = s.Update(board.FailedOf(save[0], errors.New("dial home: connection refused")))
	s, _ = s.Update(tea.PasteMsg{Content: " three"})
	s, effects := s.Update(ctrl('s'))
	wantEffects(t, effects, []board.Effect{board.SetTask{Task: 26, Patch: model.Patch{Notes: w4String("draft three")}}})
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.Contains(text, "draft two") || !strings.HasSuffix(text, "dial home: connection refused") {
		t.Fatalf("after a later save = %q, want the editor back with the failed text and its error", text)
	}
	_, effects = s.Update(ctrl('s'))
	wantEffects(t, effects, []board.Effect{board.SetTask{Task: 26, Patch: model.Patch{Notes: w4String("draft two")}}})
}

func TestTaskPageAFailedNotesSaveKeepsTheNotesItStartedFrom(t *testing.T) {
	task := model.Task{Number: 27, Title: "Save over theirs", Notes: "base", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(tea.PasteMsg{Content: " mine"})
	s, save := s.Update(ctrl('s'))
	s, _ = s.Update(press('R'))
	s, _ = s.Update(board.FailedOf(save[0], errors.New("dial home: connection refused")))
	theirs := task
	theirs.Notes = "theirs"
	s, _ = s.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: theirs}})
	s, _ = s.Update(named(tea.KeyEsc))
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.Contains(text, "base mine") {
		t.Fatalf("after the prompt closed = %q, want the editor back with the text", text)
	}
	s, effects := s.Update(ctrl('s'))
	wantEffects(t, effects, nil)
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.HasSuffix(text, "T27's notes changed while you edited: ctrl+s replaces them, esc keeps them") {
		t.Fatalf("a save over notes loaded after the failure = %q, want the editor open and a warning", text)
	}
	_, effects = s.Update(ctrl('s'))
	wantEffects(t, effects, []board.Effect{board.SetTask{Task: 27, Patch: model.Patch{Notes: w4String("base mine")}}})
}

func TestTaskPageEachFailedNotesSaveGivesBackItsOwnText(t *testing.T) {
	task := model.Task{Number: 28, Title: "Save while one is out", Notes: "draft", Status: model.StatusOpen}
	for _, laterFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("the later save fails first: %v", laterFirst), func(t *testing.T) {
			s := w4TaskPage(t, 90, task, nil)
			s, _ = s.Update(press('e'))
			s, _ = s.Update(tea.PasteMsg{Content: " two"})
			s, first := s.Update(ctrl('s'))
			s, _ = s.Update(press('e'))
			s, _ = s.Update(tea.PasteMsg{Content: " three"})
			s, second := s.Update(ctrl('s'))
			saves := []struct {
				effect board.Effect
				text   string
			}{{first[0], "draft two"}, {second[0], "draft three"}}
			if laterFirst {
				saves[0], saves[1] = saves[1], saves[0]
			}
			// Each save fails while the other is still out.
			for _, save := range saves {
				s, _ = s.Update(board.FailedOf(save.effect, errors.New("refused: "+save.text)))
				if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.HasSuffix(text, "refused: "+save.text) {
					t.Fatalf("a save failed = %q, want the editor back with its error", text)
				}
				var effects []board.Effect
				s, effects = s.Update(ctrl('s'))
				wantEffects(t, effects, []board.Effect{board.SetTask{Task: 28, Patch: model.Patch{Notes: w4String(save.text)}}})
			}
		})
	}
}

func TestTaskPageOnlyAFailureThatNamesTheSaveGivesItsTextBack(t *testing.T) {
	task := model.Task{Number: 29, Title: "Unnamed failure", Notes: "draft", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(tea.PasteMsg{Content: " two"})
	s, save := s.Update(ctrl('s'))
	s, _ = s.Update(board.Failed{Err: errors.New("w4 refresh failed")})
	if text := s.Text(); strings.Contains(text, "editing") || !strings.HasSuffix(text, "w4 refresh failed") {
		t.Fatalf("a failure that names no write = %q, want only its error", text)
	}
	s, _ = s.Update(board.FailedOf(save[0], errors.New("w4 save refused")))
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.Contains(text, "draft two") {
		t.Fatalf("the save's own failure = %q, want the editor back with the text", text)
	}
}

func TestTaskPageANotesSaveTheHomeHoldsIsNotGivenBack(t *testing.T) {
	task := model.Task{Number: 30, Title: "Held save", Notes: "draft", Status: model.StatusOpen}
	held := task
	held.Notes = "draft two"
	for _, test := range []struct {
		name string
		seen tea.Msg
		back bool
	}{
		{name: "a refresh holds it", seen: board.Loaded{Data: board.Data{Tasks: []model.Task{held}}}},
		{name: "the task's load holds it", seen: board.TaskLoaded{Detail: store.TaskDetail{Task: held}}},
		{name: "a snapshot is not the home now", seen: board.Loaded{Data: board.Data{Tasks: []model.Task{held}, Offline: true}}, back: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := w4TaskPage(t, 90, task, nil)
			s, _ = s.Update(press('e'))
			s, _ = s.Update(tea.PasteMsg{Content: " two"})
			s, save := s.Update(ctrl('s'))
			s, _ = s.Update(test.seen)
			s, _ = s.Update(board.FailedOf(save[0], errors.New("w4 answer lost")))
			if text := s.Text(); strings.Contains(text, "editing") != test.back || !strings.HasSuffix(text, "w4 answer lost") {
				t.Fatalf("a late failure = %q, want the editor back: %v", text, test.back)
			}
		})
	}
}

func TestTaskPageNotesSaveWarnsWhenTheNotesChangedWhileEditing(t *testing.T) {
	task := model.Task{Number: 22, Title: "Shared notes", Notes: "mine", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('e'))
	s, _ = s.Update(press('!'))
	theirs := task
	theirs.Notes = "theirs"
	s, _ = s.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: theirs}})
	s, effects := s.Update(ctrl('s'))
	wantEffects(t, effects, nil)
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.HasSuffix(text, "T22's notes changed while you edited: ctrl+s replaces them, esc keeps them") {
		t.Fatalf("a save over changed notes = %q, want the editor open and a warning", text)
	}
	_, effects = s.Update(ctrl('s'))
	wantEffects(t, effects, []board.Effect{board.SetTask{Task: 22, Patch: model.Patch{Notes: w4String("mine!")}}})
}

func TestTaskPageNotesEditorKeepsTheCursorLineOnScreen(t *testing.T) {
	var notes []string
	for i := range 40 {
		notes = append(notes, fmt.Sprintf("line %02d", i))
	}
	task := model.Task{Number: 23, Title: "Long notes", Notes: strings.Join(notes, "\n"), Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)
	// The editor opens with the cursor at the end of the last line.
	s, _ = s.Update(press('e'))
	if text := s.Text(); !strings.Contains(text, "line 39") || strings.Contains(text, "line 00") {
		t.Fatalf("the editor does not open on the cursor's line:\n%s", text)
	}
	for range 39 {
		s, _ = s.Update(named(tea.KeyUp))
	}
	s, _ = s.Update(press('!'))
	if text := s.Text(); !strings.Contains(text, "line 00!") || strings.Contains(text, "line 39") {
		t.Fatalf("the editor did not scroll up to the cursor's line:\n%s", text)
	}
	for range 30 {
		s, _ = s.Update(named(tea.KeyDown))
	}
	s, _ = s.Update(press('?'))
	if text := s.Text(); !strings.Contains(text, "line 30?") || strings.Contains(text, "line 00!") {
		t.Fatalf("the editor did not scroll down to the cursor's line:\n%s", text)
	}
	if !strings.Contains(s.Render(), "line 30?") {
		t.Fatalf("the rendered editor does not show the cursor's line:\n%s", s.Render())
	}
}

func TestTaskPageStepsModeKeepsTheSelectedStepOnScreen(t *testing.T) {
	steps := []model.Step{{ShortID: "s1", Text: "first step"}, {ShortID: "s2", Text: "second step"}, {ShortID: "s3", Text: "last step"}}
	task := model.Task{Number: 24, Title: "Steps below", Notes: strings.Repeat("note\n", 40), Status: model.StatusOpen, Steps: steps}
	s := w4TaskPage(t, 90, task, nil)
	s, _ = s.Update(press('t'))
	if !strings.Contains(s.Text(), "▸ [ ] first step") {
		t.Fatalf("steps mode does not show the selected step:\n%s", s.Text())
	}
	s, _ = s.Update(press('j'))
	s, _ = s.Update(press('j'))
	if !strings.Contains(s.Text(), "▸ [ ] last step") {
		t.Fatalf("steps mode does not show the selected step after moving:\n%s", s.Text())
	}
}

func TestTaskPageOfflineShowsOnlyWhatTheSnapshotHolds(t *testing.T) {
	task := model.Task{Number: 25, Title: "Was online", Notes: "online notes", Status: model.StatusOpen}
	history := []model.Event{{TS: w4Now, Kind: model.KindNote, Data: model.MustData(model.NoteData{Text: "online history"})}}
	s := w4TaskPage(t, 90, task, history)
	if !strings.Contains(s.Text(), "online history") {
		t.Fatalf("online task page = %q, want its history", s.Text())
	}
	snap := task
	snap.Notes = "snapshot notes"
	s, _ = s.Update(board.Loaded{Data: board.Data{Offline: true, Tasks: []model.Task{snap}}})
	if text := s.Text(); strings.Contains(text, "online history") || strings.Contains(text, "online notes") || !strings.Contains(text, "snapshot notes") {
		t.Fatalf("offline task page = %q, want only the snapshot's task", text)
	}
	s, _ = s.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: task, History: history}})
	if strings.Contains(s.Text(), "online history") {
		t.Fatalf("a TaskLoaded while offline put the history back: %q", s.Text())
	}
}

func TestTaskPageHistorySaysWhichEventsAreUnreadable(t *testing.T) {
	task := model.Task{Number: 26, Title: "Odd history", Status: model.StatusOpen}
	var history []model.Event
	for _, kind := range []model.Kind{model.KindTask, model.KindSet, model.KindStep, model.KindNote, model.KindDecision, model.KindMerged} {
		history = append(history, model.Event{TS: w4Now, Kind: kind, Data: []byte(`{"text": 5, "status": 5, "op": 5, "branch": 5, "ref": "w4-unreadable.md"}`)})
	}
	text := w4TaskPage(t, 90, task, history).Text()
	for _, kind := range []string{"task", "set", "step", "note", "decision", "merged"} {
		if !strings.Contains(text, "you     "+kind+" · unreadable") {
			t.Errorf("history missing %q · unreadable:\n%s", kind, text)
		}
	}
	for _, faked := range []string{"created · open", `note ""`, "FILES"} {
		if strings.Contains(text, faked) {
			t.Errorf("history draws an unreadable event as %q:\n%s", faked, text)
		}
	}
}

func TestTaskPageNavigationOverlayAndQuitUseTaskPageKeys(t *testing.T) {
	task := model.Task{Number: 19, Title: "Navigate", Status: model.StatusOpen, Notes: strings.Repeat("note\n", 40)}
	s := w4TaskPage(t, 90, task, nil)
	before := s.Text()
	s, effects := s.Update(press('j'))
	wantEffects(t, effects, nil)
	if s.Text() == before {
		t.Fatal("j did not scroll the long task page")
	}
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	wantEffects(t, effects, nil)
	if s.Text() != before {
		t.Fatal("up did not restore the initial task-page position")
	}

	s, effects = s.Update(press('?'))
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "KEYS") {
		t.Fatalf("question mark = %q, want key overlay", s.Text())
	}
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	wantEffects(t, effects, nil)
	if strings.Contains(s.Text(), "KEYS") {
		t.Fatalf("esc did not close key overlay: %q", s.Text())
	}

	_, effects = s.Update(press('q'))
	wantEffects(t, effects, []board.Effect{board.Quit{}})
}

func TestTaskPageOfflineRefusesWritesInEditorsAndSteps(t *testing.T) {
	task := model.Task{Number: 20, Title: "Offline", Status: model.StatusOpen}
	for _, tc := range []struct {
		name string
		keys []tea.KeyPressMsg
		want string
	}{
		{"notes", []tea.KeyPressMsg{press('e')}, "offline: e needs the home"},
		{"root", []tea.KeyPressMsg{press('R')}, "offline: R needs the home"},
		{"model", []tea.KeyPressMsg{press('M')}, "offline: M needs the home"},
		{"isolation", []tea.KeyPressMsg{press('I')}, "offline: I needs the home"},
		{"step toggle", []tea.KeyPressMsg{press('t'), {Code: tea.KeySpace}}, "offline: space needs the home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := w4TaskPageData(t, 90, board.Config{Now: func() time.Time { return w4Now }}, board.Data{Offline: true, Tasks: []model.Task{task}}, task, nil)
			for _, key := range tc.keys {
				var effects []board.Effect
				s, effects = s.Update(key)
				wantEffects(t, effects, nil)
			}
			if !strings.Contains(s.Text(), tc.want) {
				t.Fatalf("offline %s = %q, want %q", tc.name, s.Text(), tc.want)
			}
		})
	}
}

func TestTaskPageDelegatesStatusAndRunnerKeys(t *testing.T) {
	open := model.Task{Number: 13, Title: "Act on task", Status: model.StatusOpen, Thread: "worker"}
	review := model.Task{Number: 14, Title: "Finish review", Status: model.StatusReview}
	run := model.Run{ID: 71, Task: open.Number, Pane: "pane-71", Workspace: "workspace-71"}

	for _, tc := range []struct {
		name string
		task model.Task
		key  tea.KeyPressMsg
		want board.Effect
	}{
		{"ready sets the ready status", open, press('n'), board.SetTask{Task: 13, Patch: model.Patch{Status: w4Status(model.StatusReady)}}},
		{"start sets the started status", open, press('s'), board.SetTask{Task: 13, Patch: model.Patch{Status: w4Status(model.StatusStarted)}}},
		{"block sets the blocked status", open, press('b'), board.SetTask{Task: 13, Patch: model.Patch{Status: w4Status(model.StatusBlocked)}}},
		{"review sets the review status", open, press('r'), board.SetTask{Task: 13, Patch: model.Patch{Status: w4Status(model.StatusReview)}}},
		{"review task finishes immediately", review, press('x'), board.SetTask{Task: 14, Patch: model.Patch{Status: w4Status(model.StatusDone)}}},
		{"agent toggles the task thread", open, press('a'), board.SetTask{Task: 13, Patch: model.Patch{Thread: w4String("agent")}}},
		{"focus carries the live run", open, press('f'), board.FocusRun{Run: run}},
		{"pause asks the runner to pause", open, press('P'), board.PauseRunner{Paused: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := w4TaskPageData(t, 90, board.Config{CanFocus: true, Now: func() time.Time { return w4Now }}, board.Data{
				Tasks: []model.Task{tc.task}, Runs: []model.Run{run}, Status: api.Status{RunnerState: api.RunnerStateOn},
			}, tc.task, nil)
			_, effects := s.Update(tc.key)
			wantEffects(t, effects, []board.Effect{tc.want})
		})
	}
}

func TestTaskPageConfirmsDoneAndKillAndLeavesThePage(t *testing.T) {
	task := model.Task{Number: 15, Title: "Confirm actions", Status: model.StatusOpen}
	run := model.Run{Task: task.Number}
	s := w4TaskPageData(t, 90, board.Config{Now: func() time.Time { return w4Now }}, board.Data{Tasks: []model.Task{task}, Runs: []model.Run{run}}, task, nil)

	s, effects := s.Update(press('x'))
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "mark T15 done? y/n") {
		t.Fatalf("done prompt = %q", s.Text())
	}
	_, effects = s.Update(press('y'))
	wantEffects(t, effects, []board.Effect{board.SetTask{Task: 15, Patch: model.Patch{Status: w4Status(model.StatusDone)}}})

	s = w4TaskPageData(t, 90, board.Config{Now: func() time.Time { return w4Now }}, board.Data{Tasks: []model.Task{task}, Runs: []model.Run{run}}, task, nil)
	s, effects = s.Update(press('k'))
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "kill T15's run? y/n") {
		t.Fatalf("kill prompt = %q", s.Text())
	}
	_, effects = s.Update(press('y'))
	wantEffects(t, effects, []board.Effect{board.KillRun{Task: 15}})

	s = w4TaskPage(t, 90, task, nil)
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	wantEffects(t, effects, nil)
	if text := s.Text(); !strings.Contains(text, "desk  all") || strings.Contains(text, "e notes") {
		t.Fatalf("esc from task page = %q, want board page", text)
	}
}

func TestTaskPageStepModeHandlesEmptyStepsAndExits(t *testing.T) {
	task := model.Task{Number: 16, Title: "Start steps", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)

	s, effects := s.Update(press('t'))
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "a add  r rename  x remove") {
		t.Fatalf("step mode = %q", s.Text())
	}
	s, effects = s.Update(press('a'))
	wantEffects(t, effects, nil)
	s, effects = s.Update(press('n'))
	wantEffects(t, effects, nil)
	s, effects = s.Update(press('e'))
	wantEffects(t, effects, nil)
	s, effects = s.Update(press('w'))
	wantEffects(t, effects, nil)
	_, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	wantEffects(t, effects, []board.Effect{board.StepTask{Task: 16, Op: model.StepOp{Op: "add", Text: "new"}}})

	s = w4TaskPage(t, 90, task, nil)
	s, effects = s.Update(press('t'))
	wantEffects(t, effects, nil)
	s, effects = s.Update(press('r'))
	wantEffects(t, effects, nil)
	if strings.Contains(s.Text(), "step: ") {
		t.Fatalf("rename on no step opened a prompt: %q", s.Text())
	}
	s, effects = s.Update(press('z'))
	wantEffects(t, effects, nil)
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	wantEffects(t, effects, nil)
	if strings.Contains(s.Text(), "a add  r rename  x remove") {
		t.Fatalf("esc from step mode = %q", s.Text())
	}
}

func TestTaskPagePickListMovesUpCancelsAndQuits(t *testing.T) {
	task := model.Task{Number: 17, Title: "Choose ref", Status: model.StatusOpen, Project: "/work/desk"}
	history := []model.Event{
		{Kind: model.KindNote, Data: model.MustData(model.NoteData{Text: "one", Ref: "one.md"})},
		{Kind: model.KindSet, Data: model.MustData(model.Patch{Ref: "two.md"})},
	}
	s := w4TaskPage(t, 90, task, history)
	s, effects := s.Update(press('o'))
	wantEffects(t, effects, nil)
	s, effects = s.Update(press('j'))
	wantEffects(t, effects, nil)
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	wantEffects(t, effects, nil)
	_, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	wantEffects(t, effects, []board.Effect{board.OpenRef{Ref: "one.md", Dir: "/work/desk"}})

	s = w4TaskPage(t, 90, task, history)
	s, _ = s.Update(press('o'))
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	wantEffects(t, effects, nil)
	if strings.Contains(s.Text(), "open which ref?") {
		t.Fatalf("cancelled pick list = %q", s.Text())
	}

}

func TestTaskPageHistoryFormatsEverySpecifiedEventAndPatchField(t *testing.T) {
	status := model.StatusStarted
	title, notes, thread := "renamed", "changed", "agent"
	root, isolation, name := "/work/repo", "worktree", "gpt-5"
	task := model.Task{Number: 18, Title: "History", Status: model.StatusOpen}
	history := []model.Event{
		{TS: w4Now, Who: model.WhoUser, Kind: model.KindTask, Data: model.MustData(model.TaskData{Status: model.StatusBlocked, Thread: "ops"})},
		{TS: w4Now, Who: model.WhoUser, Kind: model.KindTask, Data: model.MustData(model.TaskData{})},
		{TS: w4Now, Who: model.WhoAgent, Kind: model.KindSet, Data: model.MustData(model.Patch{Status: &status, Title: &title, Notes: &notes})},
		{TS: w4Now, Who: model.WhoAgent, Kind: model.KindSet, Data: model.MustData(model.Patch{Thread: &thread, Root: &root})},
		{TS: w4Now, Who: model.WhoAgent, Kind: model.KindSet, Data: model.MustData(model.Patch{Isolation: &isolation, Model: &name})},
		{TS: w4Now, Who: model.WhoAgent, Kind: model.KindSet, Data: model.MustData(model.Patch{Merged: true, Ref: "change.md"})},
		{TS: w4Now, Run: 91, Kind: model.KindSet, Data: model.MustData(model.Patch{Status: &status})},
		{TS: w4Now, Run: 92, Kind: model.KindSet, Data: model.MustData(model.Patch{Status: &status})},
		{TS: w4Now, Kind: model.KindStep, Data: model.MustData(model.StepOp{Op: "toggle", Text: "write tests"})},
		{TS: w4Now, Kind: model.KindStep, Data: model.MustData(model.StepOp{Op: "remove", ShortID: "s8"})},
		{TS: w4Now, Kind: model.KindNote, Data: model.MustData(model.NoteData{Text: "keep it", Ref: "note.md"})},
		{TS: w4Now, Kind: model.KindDecision, Data: model.MustData(model.DecisionData{Text: "ship"})},
		{TS: w4Now, Kind: model.KindMerged, Data: model.MustData(model.MergedData{Branch: "feature/history"})},
		{TS: w4Now, Kind: model.KindCompacted},
	}
	data := board.Data{Tasks: []model.Task{task}, Runs: []model.Run{
		{ID: 91, Root: "/work/repo", Isolation: "worktree", Model: "gpt-5", Reason: "has capacity"},
		{ID: 92, Root: "/work/other", Isolation: "self", Model: "small"},
	}}
	text := w4TaskPageData(t, 90, board.Config{Now: func() time.Time { return w4Now }}, data, task, history).Text()
	for _, want := range []string{
		"created · blocked · #ops",
		"created · open",
		"started · title · notes",
		"thread #agent · root /work/repo",
		"isolation worktree · model gpt-5",
		"merged [change.md]",
		"runner  claimed · routed: repo, worktree, gpt-5 — has capacity",
		"runner  claimed · routed: other, self, small",
		"step toggle write tests",
		"step remove s8",
		"note \"keep it\" [note.md]",
		"decision ship",
		"merged feature/history",
		"compacted",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("history missing %q:\n%s", want, text)
		}
	}
}

func w4TaskPage(t *testing.T, width int, task model.Task, history []model.Event) board.State {
	t.Helper()
	return w4TaskPageData(t, width, board.Config{Now: func() time.Time { return w4Now }}, board.Data{Tasks: []model.Task{task}}, task, history)
}

func w4TaskPageData(t *testing.T, width int, cfg board.Config, data board.Data, task model.Task, history []model.Event) board.State {
	t.Helper()
	s := board.NewState(cfg)
	var effects []board.Effect
	s, effects = s.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	wantEffects(t, effects, nil)
	s, effects = s.Update(board.Loaded{Data: data})
	wantEffects(t, effects, nil)
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if data.Offline {
		wantEffects(t, effects, nil)
	} else {
		wantEffects(t, effects, []board.Effect{board.LoadTask{Task: task.Number}})
	}
	s, effects = s.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: task, History: history}})
	wantEffects(t, effects, nil)
	return s
}

func w4String(s string) *string {
	return &s
}

func w4Status(s model.Status) *model.Status {
	return &s
}
