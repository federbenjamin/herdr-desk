package board_test

import (
	"errors"
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

func w2State(tasks ...model.Task) board.State {
	s := board.NewState(board.Config{Now: func() time.Time { return time.Unix(100, 0) }})
	s, _ = s.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	s, _ = s.Update(board.Loaded{Data: board.Data{Tasks: tasks}})
	return s
}

func w2Task(number int, status model.Status) model.Task {
	return model.Task{
		Number:    number,
		Title:     "task " + string(rune('a'+number-1)),
		Status:    status,
		UpdatedTS: time.Unix(100, 0),
	}
}

func w2Key(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: text}
}

func w2Ctrl(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl}
}

func w2Effects(t *testing.T, got, want []board.Effect) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v, want %#v", got, want)
	}
}

func w2Status(status model.Status) *model.Status { return &status }

func TestKeysMovementEnterAndQuitUseTheSelectedTask(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen), w2Task(2, model.StatusReady))

	s, effects := s.Update(w2Key(tea.KeyDown, ""))
	w2Effects(t, effects, nil)
	if !strings.Contains(s.Text(), "▸ T2") {
		t.Fatalf("after down, screen = %q, want T2 selected", s.Text())
	}

	_, effects = s.Update(w2Key(tea.KeyEnter, ""))
	w2Effects(t, effects, []board.Effect{board.LoadTask{Task: 2}})

	for _, key := range []tea.KeyPressMsg{w2Key('q', "q"), w2Ctrl('c')} {
		_, effects = s.Update(key)
		w2Effects(t, effects, []board.Effect{board.Quit{}})
	}
}

func TestKeysStatusChangesAndDoneConfirmationEmitOnlyTheirPatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		status model.Status
		key    tea.KeyPressMsg
		want   model.Status
	}{
		{"ready", model.StatusOpen, w2Key('n', "n"), model.StatusReady},
		{"started", model.StatusReady, w2Key('s', "s"), model.StatusStarted},
		{"blocked", model.StatusReady, w2Key('b', "b"), model.StatusBlocked},
		{"review", model.StatusReady, w2Key('r', "r"), model.StatusReview},
		{"done from review", model.StatusReview, w2Key('x', "x"), model.StatusDone},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := w2State(w2Task(7, test.status))
			_, effects := s.Update(test.key)
			w2Effects(t, effects, []board.Effect{board.SetTask{
				Task:  7,
				Patch: model.Patch{Status: w2Status(test.want)},
			}})
		})
	}

	s := w2State(w2Task(8, model.StatusOpen))
	s, effects := s.Update(w2Key('x', "x"))
	w2Effects(t, effects, nil)
	if !strings.Contains(s.Text(), "mark T8 done? y/n") {
		t.Fatalf("x on an open task = %q, want done confirmation", s.Text())
	}
	_, effects = s.Update(w2Key('y', "y"))
	w2Effects(t, effects, []board.Effect{board.SetTask{
		Task:  8,
		Patch: model.Patch{Status: w2Status(model.StatusDone)},
	}})
}

func TestKeysCaptureRefusalAndBlockedAnswerKeepTheirInputUntilResolved(t *testing.T) {
	s := w2State(w2Task(3, model.StatusOpen))
	s, effects := s.Update(w2Key('+', "+"))
	w2Effects(t, effects, nil)
	if !strings.Contains(s.Text(), "add: ") {
		t.Fatalf("plus = %q, want add prompt", s.Text())
	}
	for _, key := range []tea.KeyPressMsg{w2Key('f', "f"), w2Key('i', "i"), w2Key('x', "x")} {
		s, effects = s.Update(key)
		w2Effects(t, effects, nil)
	}
	s, effects = s.Update(board.Failed{Err: errors.New("refused")})
	w2Effects(t, effects, nil)
	if !strings.Contains(s.Text(), "add: fix") || !strings.Contains(s.Text(), "refused") {
		t.Fatalf("refused add = %q, want retained line and refusal", s.Text())
	}
	s, effects = s.Update(w2Key(tea.KeyEnter, ""))
	w2Effects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{Title: "fix"}}})
	s, effects = s.Update(board.Added{Task: w2Task(4, model.StatusOpen)})
	w2Effects(t, effects, []board.Effect{board.Refresh{}})
	if strings.Contains(s.Text(), "add: fix") {
		t.Fatalf("added = %q, want add prompt closed", s.Text())
	}

	s = w2State(w2Task(9, model.StatusBlocked))
	s, effects = s.Update(w2Key('n', "n"))
	w2Effects(t, effects, nil)
	if !strings.Contains(s.Text(), "answer: ") {
		t.Fatalf("n on blocked = %q, want answer prompt", s.Text())
	}
	for _, key := range []tea.KeyPressMsg{w2Key(' ', " "), w2Key('w', "w"), w2Key('h', "h"), w2Key('y', "y"), w2Key(' ', " ")} {
		s, effects = s.Update(key)
		w2Effects(t, effects, nil)
	}
	_, effects = s.Update(w2Key(tea.KeyEnter, ""))
	w2Effects(t, effects, []board.Effect{board.Rearm{Task: 9, Answer: "why"}})
}

func TestKeysRunnerActionsRequireTheRightRunnerState(t *testing.T) {
	run := model.Run{Task: 4, Pane: "pane-4", Workspace: "work-4"}
	s := board.NewState(board.Config{CanFocus: true})
	s, _ = s.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	s, _ = s.Update(board.Loaded{Data: board.Data{
		Tasks:  []model.Task{w2Task(4, model.StatusStarted)},
		Runs:   []model.Run{run},
		Status: api.Status{RunnerState: api.RunnerStateOn},
	}})

	_, effects := s.Update(w2Key('f', "f"))
	w2Effects(t, effects, []board.Effect{board.FocusRun{Run: run}})

	s, effects = s.Update(w2Key('k', "k"))
	w2Effects(t, effects, nil)
	if !strings.Contains(s.Text(), "kill T4's run? y/n") {
		t.Fatalf("k = %q, want kill confirmation", s.Text())
	}
	_, effects = s.Update(w2Key('y', "y"))
	w2Effects(t, effects, []board.Effect{board.KillRun{Task: 4}})

	_, effects = s.Update(w2Key('P', "P"))
	w2Effects(t, effects, []board.Effect{board.PauseRunner{Paused: true}})
}

func TestKeysOfflineWritesAndCtrlDDoNotAskTheHome(t *testing.T) {
	for _, key := range []struct {
		name      string
		msg       tea.KeyPressMsg
		statusKey string
	}{
		{"add", w2Key('+', "+"), "+"},
		{"ready", w2Key('n', "n"), "n"},
		{"started", w2Key('s', "s"), "s"},
		{"blocked", w2Key('b', "b"), "b"},
		{"review", w2Key('r', "r"), "r"},
		{"done", w2Key('x', "x"), "x"},
		{"agent", w2Key('a', "a"), "a"},
		{"kill", w2Key('k', "k"), "k"},
		{"pause", w2Key('P', "P"), "P"},
	} {
		t.Run(key.name, func(t *testing.T) {
			s := board.NewState(board.Config{})
			s, _ = s.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
			s, _ = s.Update(board.Loaded{Data: board.Data{Offline: true, Tasks: []model.Task{w2Task(1, model.StatusOpen)}}})
			_, effects := s.Update(key.msg)
			w2Effects(t, effects, nil)
			if !strings.Contains(s.Text(), "offline: "+key.statusKey+" needs the home") {
				t.Fatalf("%s offline = %q, want offline refusal", key.name, s.Text())
			}
		})
	}

	boardState := w2State(w2Task(1, model.StatusOpen))
	promptState, _ := boardState.Update(w2Key('+', "+"))
	taskState, _ := boardState.Update(w2Key(tea.KeyEnter, ""))
	taskState, _ = taskState.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: w2Task(1, model.StatusOpen)}})
	for _, test := range []struct {
		name  string
		state board.State
	}{
		{"board", boardState},
		{"prompt", promptState},
		{"task", taskState},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := test.state.Text()
			after, effects := test.state.Update(w2Ctrl('d'))
			w2Effects(t, effects, nil)
			if after.Text() != before {
				t.Fatalf("ctrl+d changed %s screen from %q to %q", test.name, before, after.Text())
			}
		})
	}
}

func TestKeysTickWaitsForItsRefreshAndFailureClearsOnTheNextKey(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen))
	s, effects := s.Update(board.Tick{})
	w2Effects(t, effects, []board.Effect{board.Refresh{}})
	s, effects = s.Update(board.Tick{})
	w2Effects(t, effects, nil)
	s, effects = s.Update(board.Failed{Err: errors.New("home down")})
	w2Effects(t, effects, []board.Effect{board.Refresh{}})
	if !strings.Contains(s.Text(), "home down") {
		t.Fatalf("failed refresh = %q, want error status", s.Text())
	}
	s, effects = s.Update(w2Key('?', "?"))
	w2Effects(t, effects, nil)
	if strings.Contains(s.Text(), "home down") {
		t.Fatalf("key after failure = %q, want error cleared", s.Text())
	}
}
