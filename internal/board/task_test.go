package board_test

import (
	"reflect"
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

func TestTaskPageNotesEditorDrawsEditsAndCancelsWithoutSaving(t *testing.T) {
	task := model.Task{Number: 12, Title: "Edit notes", Notes: "first\nsecond", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)

	s, effects := s.Update(w4Key('e'))
	w4EffectsEqual(t, effects, nil)
	if text := s.Text(); !strings.Contains(text, "NOTES  editing") || !strings.Contains(text, "first\nsecond") {
		t.Fatalf("notes editor = %q, want editing notes", text)
	}
	if render := s.Render(); !strings.Contains(render, "first") || !strings.Contains(render, "second") {
		t.Fatalf("rendered notes editor = %q, want both note lines", render)
	}

	s, effects = s.Update(w4Key('!'))
	w4EffectsEqual(t, effects, nil)
	if text := s.Text(); !strings.Contains(text, "first\nsecond!") {
		t.Fatalf("typed notes = %q, want appended character", text)
	}

	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	w4EffectsEqual(t, effects, nil)
	if text := s.Text(); strings.Contains(text, "editing") || !strings.Contains(text, "first\nsecond") {
		t.Fatalf("cancelled notes editor = %q, want original saved notes", text)
	}
}

func TestTaskPageNavigationOverlayAndQuitUseTaskPageKeys(t *testing.T) {
	task := model.Task{Number: 19, Title: "Navigate", Status: model.StatusOpen, Notes: strings.Repeat("note\n", 40)}
	s := w4TaskPage(t, 90, task, nil)
	before := s.Text()
	s, effects := s.Update(w4Key('j'))
	w4EffectsEqual(t, effects, nil)
	if s.Text() == before {
		t.Fatal("j did not scroll the long task page")
	}
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	w4EffectsEqual(t, effects, nil)
	if s.Text() != before {
		t.Fatal("up did not restore the initial task-page position")
	}

	s, effects = s.Update(w4Key('?'))
	w4EffectsEqual(t, effects, nil)
	if !strings.Contains(s.Text(), "KEYS") {
		t.Fatalf("question mark = %q, want key overlay", s.Text())
	}
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	w4EffectsEqual(t, effects, nil)
	if strings.Contains(s.Text(), "KEYS") {
		t.Fatalf("esc did not close key overlay: %q", s.Text())
	}

	_, effects = s.Update(w4Key('q'))
	w4EffectsEqual(t, effects, []board.Effect{board.Quit{}})
}

func TestTaskPageOfflineRefusesWritesInEditorsAndSteps(t *testing.T) {
	task := model.Task{Number: 20, Title: "Offline", Status: model.StatusOpen}
	for _, tc := range []struct {
		name string
		keys []tea.KeyPressMsg
		want string
	}{
		{"notes", []tea.KeyPressMsg{w4Key('e')}, "offline: e needs the home"},
		{"root", []tea.KeyPressMsg{w4Key('R')}, "offline: R needs the home"},
		{"model", []tea.KeyPressMsg{w4Key('M')}, "offline: M needs the home"},
		{"isolation", []tea.KeyPressMsg{w4Key('I')}, "offline: I needs the home"},
		{"step toggle", []tea.KeyPressMsg{w4Key('t'), {Code: tea.KeySpace}}, "offline: space needs the home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := w4TaskPageData(t, 90, board.Config{Now: func() time.Time { return w4Now }}, board.Data{Offline: true, Tasks: []model.Task{task}}, task, nil)
			for _, key := range tc.keys {
				var effects []board.Effect
				s, effects = s.Update(key)
				w4EffectsEqual(t, effects, nil)
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
		{"ready sets the ready status", open, w4Key('n'), board.SetTask{Task: 13, Patch: model.Patch{Status: w4Status(model.StatusReady)}}},
		{"start sets the started status", open, w4Key('s'), board.SetTask{Task: 13, Patch: model.Patch{Status: w4Status(model.StatusStarted)}}},
		{"block sets the blocked status", open, w4Key('b'), board.SetTask{Task: 13, Patch: model.Patch{Status: w4Status(model.StatusBlocked)}}},
		{"review sets the review status", open, w4Key('r'), board.SetTask{Task: 13, Patch: model.Patch{Status: w4Status(model.StatusReview)}}},
		{"review task finishes immediately", review, w4Key('x'), board.SetTask{Task: 14, Patch: model.Patch{Status: w4Status(model.StatusDone)}}},
		{"agent toggles the task thread", open, w4Key('a'), board.SetTask{Task: 13, Patch: model.Patch{Thread: w4String("agent")}}},
		{"focus carries the live run", open, w4Key('f'), board.FocusRun{Run: run}},
		{"pause asks the runner to pause", open, w4Key('P'), board.PauseRunner{Paused: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := w4TaskPageData(t, 90, board.Config{CanFocus: true, Now: func() time.Time { return w4Now }}, board.Data{
				Tasks: []model.Task{tc.task}, Runs: []model.Run{run}, Status: api.Status{RunnerState: api.RunnerStateOn},
			}, tc.task, nil)
			_, effects := s.Update(tc.key)
			w4EffectsEqual(t, effects, []board.Effect{tc.want})
		})
	}
}

func TestTaskPageConfirmsDoneAndKillAndLeavesThePage(t *testing.T) {
	task := model.Task{Number: 15, Title: "Confirm actions", Status: model.StatusOpen}
	run := model.Run{Task: task.Number}
	s := w4TaskPageData(t, 90, board.Config{Now: func() time.Time { return w4Now }}, board.Data{Tasks: []model.Task{task}, Runs: []model.Run{run}}, task, nil)

	s, effects := s.Update(w4Key('x'))
	w4EffectsEqual(t, effects, nil)
	if !strings.Contains(s.Text(), "mark T15 done? y/n") {
		t.Fatalf("done prompt = %q", s.Text())
	}
	_, effects = s.Update(w4Key('y'))
	w4EffectsEqual(t, effects, []board.Effect{board.SetTask{Task: 15, Patch: model.Patch{Status: w4Status(model.StatusDone)}}})

	s = w4TaskPageData(t, 90, board.Config{Now: func() time.Time { return w4Now }}, board.Data{Tasks: []model.Task{task}, Runs: []model.Run{run}}, task, nil)
	s, effects = s.Update(w4Key('k'))
	w4EffectsEqual(t, effects, nil)
	if !strings.Contains(s.Text(), "kill T15's run? y/n") {
		t.Fatalf("kill prompt = %q", s.Text())
	}
	_, effects = s.Update(w4Key('y'))
	w4EffectsEqual(t, effects, []board.Effect{board.KillRun{Task: 15}})

	s = w4TaskPage(t, 90, task, nil)
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	w4EffectsEqual(t, effects, nil)
	if text := s.Text(); !strings.Contains(text, "desk  all") || strings.Contains(text, "e notes") {
		t.Fatalf("esc from task page = %q, want board page", text)
	}
}

func TestTaskPageStepModeHandlesEmptyStepsAndExits(t *testing.T) {
	task := model.Task{Number: 16, Title: "Start steps", Status: model.StatusOpen}
	s := w4TaskPage(t, 90, task, nil)

	s, effects := s.Update(w4Key('t'))
	w4EffectsEqual(t, effects, nil)
	if !strings.Contains(s.Text(), "a add  r rename  x remove") {
		t.Fatalf("step mode = %q", s.Text())
	}
	s, effects = s.Update(w4Key('a'))
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(w4Key('n'))
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(w4Key('e'))
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(w4Key('w'))
	w4EffectsEqual(t, effects, nil)
	_, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	w4EffectsEqual(t, effects, []board.Effect{board.StepTask{Task: 16, Op: model.StepOp{Op: "add", Text: "new"}}})

	s = w4TaskPage(t, 90, task, nil)
	s, effects = s.Update(w4Key('t'))
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(w4Key('r'))
	w4EffectsEqual(t, effects, nil)
	if strings.Contains(s.Text(), "step: ") {
		t.Fatalf("rename on no step opened a prompt: %q", s.Text())
	}
	s, effects = s.Update(w4Key('z'))
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	w4EffectsEqual(t, effects, nil)
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
	s, effects := s.Update(w4Key('o'))
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(w4Key('j'))
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	w4EffectsEqual(t, effects, nil)
	_, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	w4EffectsEqual(t, effects, []board.Effect{board.OpenRef{Ref: "one.md", Dir: "/work/desk"}})

	s = w4TaskPage(t, 90, task, history)
	s, _ = s.Update(w4Key('o'))
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	w4EffectsEqual(t, effects, nil)
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
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(board.Loaded{Data: data})
	w4EffectsEqual(t, effects, nil)
	s, effects = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if data.Offline {
		w4EffectsEqual(t, effects, nil)
	} else {
		w4EffectsEqual(t, effects, []board.Effect{board.LoadTask{Task: task.Number}})
	}
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

func w4Status(s model.Status) *model.Status {
	return &s
}
