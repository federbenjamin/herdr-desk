package board_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

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
				w4Key('e'),
				w4Ctrl('s'),
			},
			want: board.SetTask{Task: 7, Patch: model.Patch{Notes: w4String("existing note")}},
		},
		{
			name: "submitting root preserves its populated value",
			keys: []tea.KeyPressMsg{
				w4Key('R'),
				{Code: tea.KeyEnter},
			},
			want: board.SetTask{Task: 7, Patch: model.Patch{Root: w4String("/work/desk")}},
		},
		{
			name: "isolation advances from unset to self",
			keys: []tea.KeyPressMsg{w4Key('I')},
			want: board.SetTask{Task: 7, Patch: model.Patch{Isolation: w4String("self")}},
		},
		{
			name: "submitting model preserves its populated value",
			keys: []tea.KeyPressMsg{
				w4Key('M'),
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
			w4EffectsEqual(t, effects, []board.Effect{tc.want})
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

	s, effects := s.Update(w4Key('e'))
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(tea.PasteMsg{Content: " pasted"})
	w4EffectsEqual(t, effects, nil)
	_, effects = s.Update(w4Ctrl('s'))
	w4EffectsEqual(t, effects, []board.Effect{board.SetTask{
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
			keys: []tea.KeyPressMsg{w4Key('t'), {Code: tea.KeySpace}},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "toggle", ShortID: "s1"}},
		},
		{
			name: "enter toggles the step reached with down",
			keys: []tea.KeyPressMsg{w4Key('t'), {Code: tea.KeyDown}, {Code: tea.KeyEnter}},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "toggle", ShortID: "s2"}},
		},
		{
			name: "up returns selection to the preceding step",
			keys: []tea.KeyPressMsg{w4Key('t'), {Code: tea.KeyDown}, {Code: tea.KeyUp}, {Code: tea.KeyEnter}},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "toggle", ShortID: "s1"}},
		},
		{
			name: "adding text creates an add operation",
			keys: []tea.KeyPressMsg{w4Key('t'), w4Key('a'), w4Key('n'), w4Key('e'), w4Key('w'), {Code: tea.KeyEnter}},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "add", Text: "new"}},
		},
		{
			name: "renaming a step submits its populated text",
			keys: []tea.KeyPressMsg{w4Key('t'), w4Key('r'), {Code: tea.KeyEnter}},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "rename", ShortID: "s1", Text: "first"}},
		},
		{
			name: "removing a step uses its short id",
			keys: []tea.KeyPressMsg{w4Key('t'), w4Key('x')},
			want: board.StepTask{Task: 8, Op: model.StepOp{Op: "remove", ShortID: "s1"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := w4TaskPage(t, 90, task, nil)
			var effects []board.Effect
			for _, key := range tc.keys {
				s, effects = s.Update(key)
			}
			w4EffectsEqual(t, effects, []board.Effect{tc.want})
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
		s, effects := s.Update(w4Key('o'))
		_ = s
		w4EffectsEqual(t, effects, []board.Effect{board.OpenRef{Ref: "docs/one.md", Dir: "/work/desk"}})
	})

	t.Run("several refs select the navigated item", func(t *testing.T) {
		s := w4TaskPage(t, 90, task, twoRefs)
		s, effects := s.Update(w4Key('o'))
		w4EffectsEqual(t, effects, nil)
		s, _ = s.Update(w4Key('j'))
		_, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		w4EffectsEqual(t, effects, []board.Effect{board.OpenRef{Ref: "docs/two.md", Dir: "/work/desk"}})
	})

	t.Run("no refs explains why nothing opened", func(t *testing.T) {
		s := w4TaskPage(t, 90, task, nil)
		s, effects := s.Update(w4Key('o'))
		w4EffectsEqual(t, effects, nil)
		if !strings.Contains(s.Text(), "T11 has no ref") {
			t.Errorf("no-ref status = %q, want T11 has no ref", s.Text())
		}
	})
}

func w4TaskPage(t *testing.T, width int, task model.Task, history []model.Event) board.State {
	t.Helper()
	s := board.NewState(board.Config{Now: func() time.Time { return w4Now }})
	var effects []board.Effect
	s, effects = s.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(board.Loaded{Data: board.Data{Tasks: []model.Task{task}}})
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	w4EffectsEqual(t, effects, []board.Effect{board.LoadTask{Task: task.Number}})
	s, effects = s.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: task, History: history}})
	w4EffectsEqual(t, effects, nil)
	return s
}

func w4EffectsEqual(t *testing.T, got, want []board.Effect) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v, want %#v", got, want)
	}
}

func w4Key(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: string(code)}
}

func w4Ctrl(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl}
}

func w4String(s string) *string {
	return &s
}
