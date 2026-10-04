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
	if !strings.Contains(s.Text(), "▸ T1") {
		t.Fatalf("after down, screen = %q, want T1 selected", s.Text())
	}

	_, effects = s.Update(w2Key(tea.KeyEnter, ""))
	w2Effects(t, effects, []board.Effect{board.LoadTask{Task: 1}})

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
	s, effects = s.Update(w2Key('y', "y"))
	w2Effects(t, effects, []board.Effect{board.KillRun{Task: 4}})

	_, effects = s.Update(w2Key('P', "P"))
	w2Effects(t, effects, []board.Effect{board.PauseRunner{Paused: true}})

	fallback := board.NewState(board.Config{})
	fallback, _ = fallback.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	fallback, _ = fallback.Update(board.Loaded{Data: board.Data{
		Tasks:  []model.Task{w2Task(4, model.StatusStarted)},
		Status: api.Status{RunnerOn: true},
	}})
	_, effects = fallback.Update(w2Key('P', "P"))
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
			s, effects := s.Update(key.msg)
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

func TestKeysBoardNavigationFiltersDrawerAndOverlayKeepBoardStateCoherent(t *testing.T) {
	tasks := []model.Task{
		{Number: 1, Title: "blocked", Status: model.StatusBlocked, Project: "/work/alpha", Thread: "red", UpdatedTS: time.Unix(100, 0)},
		{Number: 2, Title: "review", Status: model.StatusReview, Project: "/work/beta", Thread: "blue", UpdatedTS: time.Unix(100, 0)},
		{Number: 3, Title: "started", Status: model.StatusStarted, UpdatedTS: time.Unix(100, 0)},
		{Number: 4, Title: "ready", Status: model.StatusReady, Project: "/work/alpha", Thread: "red", UpdatedTS: time.Unix(100, 0)},
		{Number: 5, Title: "open", Status: model.StatusOpen, UpdatedTS: time.Unix(100, 0)},
	}
	s := w2State(tasks...)
	s, effects := s.Update(w2Key('G', "G"))
	w2Effects(t, effects, nil)
	if !strings.Contains(s.Text(), "▸ T5") {
		t.Fatalf("G screen = %q, want T5 selected", s.Text())
	}
	s, _ = s.Update(w2Key('g', "g"))
	if !strings.Contains(s.Text(), "▸ T1") {
		t.Fatalf("g screen = %q, want T1 selected", s.Text())
	}
	s, _ = s.Update(w2Key(tea.KeyUp, ""))
	if !strings.Contains(s.Text(), "▸ T1") {
		t.Fatalf("up at first row = %q, want T1 selected", s.Text())
	}

	s, _ = s.Update(w2Key('p', "p"))
	if !strings.Contains(s.Text(), "desk  alpha ▾") || strings.Contains(s.Text(), "T2") {
		t.Fatalf("first project filter = %q, want only alpha tasks", s.Text())
	}
	s, _ = s.Update(w2Key('t', "t"))
	if !strings.Contains(s.Text(), "thread: red ▾") || strings.Contains(s.Text(), "T2") {
		t.Fatalf("first thread filter = %q, want only red alpha tasks", s.Text())
	}

	s, _ = s.Update(w2Key('/', "/"))
	s, _ = s.Update(w2Key('b', "b"))
	if !strings.Contains(s.Text(), "search: b") || !strings.Contains(s.Text(), "T1") {
		t.Fatalf("search = %q, want matching task and query", s.Text())
	}
	s, _ = s.Update(w2Key(tea.KeyEsc, ""))
	if strings.Contains(s.Text(), "search: ") || strings.Contains(s.Text(), "T2") {
		t.Fatalf("search escape = %q, want search cleared while filters remain", s.Text())
	}

	s = w2State(tasks...)
	s, effects = s.Update(w2Key('d', "d"))
	w2Effects(t, effects, []board.Effect{board.Refresh{Done: true}})
	s, _ = s.Update(board.Loaded{Data: board.Data{Tasks: tasks, Done: []model.Task{w2Task(6, model.StatusDone)}}})
	if !strings.Contains(s.Text(), "DONE") || !strings.Contains(s.Text(), "T6") {
		t.Fatalf("done drawer = %q, want DONE section", s.Text())
	}
	s, _ = s.Update(w2Key(tea.KeyEsc, ""))
	if strings.Contains(s.Text(), "DONE") {
		t.Fatalf("drawer escape = %q, want drawer closed", s.Text())
	}

	s, _ = s.Update(w2Key('?', "?"))
	if !strings.Contains(s.Text(), "KEYS") || !strings.Contains(s.Text(), "pause or resume the runner") {
		t.Fatalf("keys overlay = %q, want board key list", s.Text())
	}
	s, _ = s.Update(w2Key(tea.KeyEsc, ""))
	if strings.Contains(s.Text(), "KEYS") {
		t.Fatalf("keys escape = %q, want overlay closed", s.Text())
	}
}

func TestKeysAgentFocusKillAndRunnerStatusMessagesExplainUnavailableActions(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen))
	_, effects := s.Update(w2Key('a', "a"))
	thread := "agent"
	w2Effects(t, effects, []board.Effect{board.SetTask{Task: 1, Patch: model.Patch{Thread: &thread}}})

	s, effects = s.Update(w2Key('f', "f"))
	w2Effects(t, effects, nil)
	if !strings.HasSuffix(s.Text(), "f works only on the home, with herdr") {
		t.Fatalf("f without focus support = %q", s.Text())
	}

	canFocus := board.NewState(board.Config{CanFocus: true})
	canFocus, _ = canFocus.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	canFocus, _ = canFocus.Update(board.Loaded{Data: board.Data{Tasks: []model.Task{w2Task(1, model.StatusStarted)}}})
	canFocus, effects = canFocus.Update(w2Key('f', "f"))
	w2Effects(t, effects, nil)
	if !strings.HasSuffix(canFocus.Text(), "T1 has no live run") {
		t.Fatalf("f without run = %q", canFocus.Text())
	}
	_, effects = canFocus.Update(w2Key('k', "k"))
	w2Effects(t, effects, nil)
	if !strings.HasSuffix(canFocus.Text(), "T1 has no live run") {
		t.Fatalf("k without run = %q", canFocus.Text())
	}

	paneLess := board.NewState(board.Config{CanFocus: true})
	paneLess, _ = paneLess.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	paneLess, _ = paneLess.Update(board.Loaded{Data: board.Data{Tasks: []model.Task{w2Task(1, model.StatusStarted)}, Runs: []model.Run{{Task: 1}}}})
	paneLess, effects = paneLess.Update(w2Key('f', "f"))
	w2Effects(t, effects, nil)
	if !strings.HasSuffix(paneLess.Text(), "T1's run has no pane yet") {
		t.Fatalf("f without pane = %q", paneLess.Text())
	}

	for _, test := range []struct {
		state string
		want  string
		eff   board.Effect
	}{
		{api.RunnerStatePaused, "", board.PauseRunner{Paused: false}},
		{api.RunnerStateNoRouter, "the runner is no-router", nil},
		{"", "the runner is off", nil},
	} {
		t.Run(test.state+test.want, func(t *testing.T) {
			state := w2State(w2Task(1, model.StatusOpen))
			state, _ = state.Update(board.Loaded{Data: board.Data{Tasks: []model.Task{w2Task(1, model.StatusOpen)}, Status: api.Status{RunnerState: test.state}}})
			state, effects := state.Update(w2Key('P', "P"))
			if test.eff == nil {
				w2Effects(t, effects, nil)
				if !strings.HasSuffix(state.Text(), test.want) {
					t.Fatalf("P with %q = %q", test.state, state.Text())
				}
				return
			}
			w2Effects(t, effects, []board.Effect{test.eff})
		})
	}
}

func TestKeysPasteGoesOnlyToTheOpenTextInput(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen))
	before := s.Text()
	after, effects := s.Update(tea.PasteMsg{Content: "ignored"})
	w2Effects(t, effects, nil)
	if after.Text() != before {
		t.Fatalf("paste without input changed screen from %q to %q", before, after.Text())
	}

	s, _ = s.Update(w2Key('+', "+"))
	s, effects = s.Update(tea.PasteMsg{Content: "fix #agent"})
	w2Effects(t, effects, nil)
	if !strings.Contains(s.Text(), "add: fix #agent") {
		t.Fatalf("paste into add box = %q, want pasted line", s.Text())
	}
}

func TestKeysPasteUpdatesSearch(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen))
	s, _ = s.Update(w2Key('/', "/"))
	s, effects := s.Update(tea.PasteMsg{Content: "task a"})
	w2Effects(t, effects, nil)
	if !strings.Contains(s.Text(), "search: task a") {
		t.Fatalf("paste into search = %q, want pasted query", s.Text())
	}
}

func TestKeysLayoutWidthsKeepOneOrBothSurfacesAsSpecified(t *testing.T) {
	for _, test := range []struct {
		width    int
		contains string
	}{
		{60, "? keys  q quit"},
		{90, "+ add  n ready"},
		{120, "│"},
	} {
		t.Run(string(rune(test.width)), func(t *testing.T) {
			s := board.NewState(board.Config{})
			s, _ = s.Update(tea.WindowSizeMsg{Width: test.width, Height: 24})
			s, _ = s.Update(board.Loaded{Data: board.Data{Tasks: []model.Task{w2Task(1, model.StatusOpen)}}})
			if !strings.Contains(s.Text(), test.contains) {
				t.Fatalf("width %d screen = %q, want %q", test.width, s.Text(), test.contains)
			}
		})
	}
}

func TestKeysMovementStaysAtListEndsAndLeavesAnEmptyBoardUntouched(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen), w2Task(2, model.StatusReady))
	for _, test := range []struct {
		name string
		key  tea.KeyPressMsg
		want string
	}{
		{"down selects the next row", w2Key(tea.KeyDown, ""), "▸ T1"},
		{"down stays at the last row", w2Key(tea.KeyDown, ""), "▸ T1"},
		{"j stays at the last row", w2Key('j', "j"), "▸ T1"},
		{"up selects the previous row", w2Key(tea.KeyUp, ""), "▸ T2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var effects []board.Effect
			s, effects = s.Update(test.key)
			w2Effects(t, effects, nil)
			if !strings.Contains(s.Text(), test.want) {
				t.Fatalf("%s = %q, want %q", test.name, s.Text(), test.want)
			}
		})
	}

	empty := w2State()
	for _, key := range []tea.KeyPressMsg{w2Key('g', "g"), w2Key('G', "G"), w2Key(tea.KeyEnter, ""), w2Key('n', "n")} {
		var effects []board.Effect
		empty, effects = empty.Update(key)
		w2Effects(t, effects, nil)
	}

	for _, test := range []struct {
		name    string
		offline bool
		want    []board.Effect
	}{
		{"wide online loads the new selection", false, []board.Effect{board.LoadTask{Task: 1}}},
		{"wide offline moves without loading", true, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := board.NewState(board.Config{})
			s, _ = s.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
			s, _ = s.Update(board.Loaded{Data: board.Data{Offline: test.offline, Tasks: []model.Task{w2Task(1, model.StatusOpen), w2Task(2, model.StatusReady)}}})
			_, effects := s.Update(w2Key(tea.KeyDown, ""))
			w2Effects(t, effects, test.want)
		})
	}
}

func TestKeysAddCancellationSuppressesCaptureQuitAndShowsFailuresExactly(t *testing.T) {
	for _, test := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"escape", w2Key(tea.KeyEsc, "")},
		{"empty line", w2Key(tea.KeyEnter, "")},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := w2State(w2Task(1, model.StatusOpen))
			s, _ = s.Update(w2Key('+', "+"))
			s, effects := s.Update(test.key)
			w2Effects(t, effects, nil)
			if strings.Contains(s.Text(), "add: ") {
				t.Fatalf("%s leaves the add box open: %q", test.name, s.Text())
			}
		})
	}

	s := w2State(w2Task(1, model.StatusOpen))
	s, _ = s.Update(w2Key('+', "+"))
	s, effects := s.Update(board.Failed{Err: &model.Refusal{Code: model.CodeNotAllowed, Msg: "runner is paused"}})
	w2Effects(t, effects, nil)
	if !strings.Contains(s.Text(), "not-allowed: runner is paused") {
		t.Fatalf("refused add = %q, want the refusal text", s.Text())
	}

	s = w2State(w2Task(1, model.StatusOpen))
	s, effects = s.Update(board.Failed{})
	w2Effects(t, effects, nil)
	if !strings.HasSuffix(s.Text(), "an effect failed") {
		t.Fatalf("missing failure error = %q, want fallback status", s.Text())
	}
}

func TestKeysSearchFindsTaskNumbersAndTitlesWithoutCaseSensitivity(t *testing.T) {
	tasks := []model.Task{
		{Number: 12, Title: "Fix Runner", Status: model.StatusOpen, UpdatedTS: time.Unix(100, 0)},
		{Number: 13, Title: "Other work", Status: model.StatusOpen, UpdatedTS: time.Unix(100, 0)},
	}
	for _, test := range []struct {
		name  string
		query string
		want  string
		gone  string
	}{
		{"task number", "t12", "T12", "T13"},
		{"title ignores case", "rUnNeR", "T12", "T13"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := w2State(tasks...)
			s, _ = s.Update(w2Key('/', "/"))
			for _, r := range test.query {
				var effects []board.Effect
				s, effects = s.Update(w2Key(r, string(r)))
				w2Effects(t, effects, nil)
			}
			if !strings.Contains(s.Text(), "search: "+test.query) || !strings.Contains(s.Text(), test.want) || strings.Contains(s.Text(), test.gone) {
				t.Fatalf("search %q = %q, want %q only", test.query, s.Text(), test.want)
			}
			s, _ = s.Update(w2Key(tea.KeyEnter, ""))
			s, effects := s.Update(w2Key(tea.KeyEsc, ""))
			w2Effects(t, effects, nil)
			if strings.Contains(s.Text(), "search: ") {
				t.Fatalf("escape after keeping search = %q, want search cleared", s.Text())
			}
		})
	}

	filters := w2State(
		model.Task{Number: 14, Title: "blue local", Status: model.StatusOpen, Thread: "blue", UpdatedTS: time.Unix(100, 0)},
		model.Task{Number: 15, Title: "red local", Status: model.StatusOpen, Thread: "red", UpdatedTS: time.Unix(100, 0)},
		model.Task{Number: 16, Title: "project task", Status: model.StatusOpen, Project: "/work/alpha", Thread: "red", UpdatedTS: time.Unix(100, 0)},
	)
	for range 2 {
		filters, _ = filters.Update(w2Key('p', "p"))
	}
	if !strings.Contains(filters.Text(), "desk  no project ▾") || !strings.Contains(filters.Text(), "T14") || strings.Contains(filters.Text(), "T16") {
		t.Fatalf("no project filter = %q, want local tasks only", filters.Text())
	}
	filters, _ = filters.Update(w2Key('t', "t"))
	if !strings.Contains(filters.Text(), "T14") || strings.Contains(filters.Text(), "T15") {
		t.Fatalf("thread filter = %q, want blue local task only", filters.Text())
	}

	offline := board.NewState(board.Config{})
	offline, _ = offline.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	offline, _ = offline.Update(board.Loaded{Data: board.Data{Offline: true}})
	offline, effects := offline.Update(w2Key('d', "d"))
	w2Effects(t, effects, nil)
	if !strings.HasSuffix(offline.Text(), "offline: the done drawer needs the home") {
		t.Fatalf("offline done drawer = %q, want refusal", offline.Text())
	}
}
