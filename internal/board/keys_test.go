package board_test

import (
	"errors"
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

func w2Status(status model.Status) *model.Status { return &status }

func TestKeysMovementEnterAndQuitUseTheSelectedTask(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen), w2Task(2, model.StatusReady))

	s, effects := s.Update(named(tea.KeyDown))
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "▸ T1") {
		t.Fatalf("after down, screen = %q, want T1 selected", s.Text())
	}

	_, effects = s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.LoadTask{Task: 1}})

	for _, key := range []tea.KeyPressMsg{press('q'), ctrl('c')} {
		_, effects = s.Update(key)
		wantEffects(t, effects, []board.Effect{board.Quit{}})
	}
}

func TestKeysStatusChangesAndDoneConfirmationEmitOnlyTheirPatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		status model.Status
		key    tea.KeyPressMsg
		want   model.Status
	}{
		{"ready", model.StatusOpen, press('n'), model.StatusReady},
		{"started", model.StatusReady, press('s'), model.StatusStarted},
		{"blocked", model.StatusReady, press('b'), model.StatusBlocked},
		{"review", model.StatusReady, press('r'), model.StatusReview},
		{"done from review", model.StatusReview, press('x'), model.StatusDone},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := w2State(w2Task(7, test.status))
			_, effects := s.Update(test.key)
			wantEffects(t, effects, []board.Effect{board.SetTask{
				Task:  7,
				Patch: model.Patch{Status: w2Status(test.want)},
			}})
		})
	}

	s := w2State(w2Task(8, model.StatusOpen))
	s, effects := s.Update(press('x'))
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "mark T8 done? y/n") {
		t.Fatalf("x on an open task = %q, want done confirmation", s.Text())
	}
	_, effects = s.Update(press('y'))
	wantEffects(t, effects, []board.Effect{board.SetTask{
		Task:  8,
		Patch: model.Patch{Status: w2Status(model.StatusDone)},
	}})
}

func TestKeysCaptureRefusalAndBlockedAnswerKeepTheirInputUntilResolved(t *testing.T) {
	s := w2State(w2Task(3, model.StatusOpen))
	s, effects := s.Update(press('+'))
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "add: ") {
		t.Fatalf("plus = %q, want add prompt", s.Text())
	}
	for _, key := range []tea.KeyPressMsg{press('f'), press('i'), press('x')} {
		s, effects = s.Update(key)
		wantEffects(t, effects, nil)
	}
	s, effects = s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{Title: "fix"}}})
	s, effects = s.Update(board.Failed{Err: errors.New("refused")})
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "add: fix") || !strings.Contains(s.Text(), "refused") {
		t.Fatalf("refused add = %q, want retained line and refusal", s.Text())
	}
	s, effects = s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{Title: "fix"}}})
	s, effects = s.Update(board.Added{Task: w2Task(4, model.StatusOpen)})
	wantEffects(t, effects, []board.Effect{board.Refresh{}})
	if strings.Contains(s.Text(), "add: fix") {
		t.Fatalf("added = %q, want add prompt closed", s.Text())
	}

	s = w2State(w2Task(9, model.StatusBlocked))
	s, effects = s.Update(press('n'))
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "answer: ") {
		t.Fatalf("n on blocked = %q, want answer prompt", s.Text())
	}
	for _, key := range []tea.KeyPressMsg{press(' '), press('w'), press('h'), press('y'), press(' ')} {
		s, effects = s.Update(key)
		wantEffects(t, effects, nil)
	}
	_, effects = s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.Rearm{Task: 9, Answer: "why"}})
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

	_, effects := s.Update(press('f'))
	wantEffects(t, effects, []board.Effect{board.FocusRun{Run: run}})

	s, effects = s.Update(press('k'))
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "kill T4's run? y/n") {
		t.Fatalf("k = %q, want kill confirmation", s.Text())
	}
	s, effects = s.Update(press('y'))
	wantEffects(t, effects, []board.Effect{board.KillRun{Task: 4}})

	_, effects = s.Update(press('P'))
	wantEffects(t, effects, []board.Effect{board.PauseRunner{Paused: true}})

	fallback := board.NewState(board.Config{})
	fallback, _ = fallback.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	fallback, _ = fallback.Update(board.Loaded{Data: board.Data{
		Tasks:  []model.Task{w2Task(4, model.StatusStarted)},
		Status: api.Status{RunnerOn: true},
	}})
	_, effects = fallback.Update(press('P'))
	wantEffects(t, effects, []board.Effect{board.PauseRunner{Paused: true}})
}

func TestKeysOfflineWritesAndCtrlDDoNotAskTheHome(t *testing.T) {
	for _, key := range []struct {
		name      string
		msg       tea.KeyPressMsg
		statusKey string
	}{
		{"add", press('+'), "+"},
		{"ready", press('n'), "n"},
		{"started", press('s'), "s"},
		{"blocked", press('b'), "b"},
		{"review", press('r'), "r"},
		{"done", press('x'), "x"},
		{"agent", press('a'), "a"},
		{"kill", press('k'), "k"},
		{"pause", press('P'), "P"},
	} {
		t.Run(key.name, func(t *testing.T) {
			s := board.NewState(board.Config{})
			s, _ = s.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
			s, _ = s.Update(board.Loaded{Data: board.Data{Offline: true, Tasks: []model.Task{w2Task(1, model.StatusOpen)}}})
			s, effects := s.Update(key.msg)
			wantEffects(t, effects, nil)
			if !strings.Contains(s.Text(), "offline: "+key.statusKey+" needs the home") {
				t.Fatalf("%s offline = %q, want offline refusal", key.name, s.Text())
			}
		})
	}

	boardState := w2State(w2Task(1, model.StatusOpen))
	promptState, _ := boardState.Update(press('+'))
	taskState, _ := boardState.Update(named(tea.KeyEnter))
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
			after, effects := test.state.Update(ctrl('d'))
			wantEffects(t, effects, nil)
			if after.Text() != before {
				t.Fatalf("ctrl+d changed %s screen from %q to %q", test.name, before, after.Text())
			}
		})
	}
}

func TestKeysTickWaitsForItsRefreshAndFailureClearsOnTheNextKey(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen))
	s, effects := s.Update(board.Tick{})
	wantEffects(t, effects, []board.Effect{board.Refresh{}})
	s, effects = s.Update(board.Tick{})
	wantEffects(t, effects, nil)
	s, effects = s.Update(board.Failed{Err: errors.New("home down")})
	wantEffects(t, effects, []board.Effect{board.Refresh{}})
	if !strings.Contains(s.Text(), "home down") {
		t.Fatalf("failed refresh = %q, want error status", s.Text())
	}
	s, effects = s.Update(press('?'))
	wantEffects(t, effects, nil)
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
	s, effects := s.Update(press('G'))
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "▸ T5") {
		t.Fatalf("G screen = %q, want T5 selected", s.Text())
	}
	s, _ = s.Update(press('g'))
	if !strings.Contains(s.Text(), "▸ T1") {
		t.Fatalf("g screen = %q, want T1 selected", s.Text())
	}
	s, _ = s.Update(named(tea.KeyUp))
	if !strings.Contains(s.Text(), "▸ T1") {
		t.Fatalf("up at first row = %q, want T1 selected", s.Text())
	}

	s, _ = s.Update(press('p'))
	if !strings.Contains(s.Text(), "desk  alpha ▾") || strings.Contains(s.Text(), "T2") {
		t.Fatalf("first project filter = %q, want only alpha tasks", s.Text())
	}
	s, _ = s.Update(press('t'))
	if !strings.Contains(s.Text(), "thread: red ▾") || strings.Contains(s.Text(), "T2") {
		t.Fatalf("first thread filter = %q, want only red alpha tasks", s.Text())
	}

	s, _ = s.Update(press('/'))
	s, _ = s.Update(press('b'))
	if !strings.Contains(s.Text(), "search: b") || !strings.Contains(s.Text(), "T1") {
		t.Fatalf("search = %q, want matching task and query", s.Text())
	}
	s, _ = s.Update(named(tea.KeyEsc))
	if strings.Contains(s.Text(), "search: ") || strings.Contains(s.Text(), "T2") {
		t.Fatalf("search escape = %q, want search cleared while filters remain", s.Text())
	}

	s = w2State(tasks...)
	s, effects = s.Update(press('d'))
	wantEffects(t, effects, []board.Effect{board.Refresh{Done: true}})
	s, _ = s.Update(board.Loaded{Data: board.Data{Tasks: tasks, Done: []model.Task{w2Task(6, model.StatusDone)}}})
	if !strings.Contains(s.Text(), "DONE") || !strings.Contains(s.Text(), "T6") {
		t.Fatalf("done drawer = %q, want DONE section", s.Text())
	}
	s, _ = s.Update(named(tea.KeyEsc))
	if strings.Contains(s.Text(), "DONE") {
		t.Fatalf("drawer escape = %q, want drawer closed", s.Text())
	}

	s, _ = s.Update(press('?'))
	if !strings.Contains(s.Text(), "KEYS") || !strings.Contains(s.Text(), "pause or resume the runner") {
		t.Fatalf("keys overlay = %q, want board key list", s.Text())
	}
	s, _ = s.Update(named(tea.KeyEsc))
	if strings.Contains(s.Text(), "KEYS") {
		t.Fatalf("keys escape = %q, want overlay closed", s.Text())
	}
}

func TestKeysAgentFocusKillAndRunnerStatusMessagesExplainUnavailableActions(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen))
	_, effects := s.Update(press('a'))
	thread := "agent"
	wantEffects(t, effects, []board.Effect{board.SetTask{Task: 1, Patch: model.Patch{Thread: &thread}}})

	s, effects = s.Update(press('f'))
	wantEffects(t, effects, nil)
	if !strings.HasSuffix(s.Text(), "f works only on the home, with herdr") {
		t.Fatalf("f without focus support = %q", s.Text())
	}

	canFocus := board.NewState(board.Config{CanFocus: true})
	canFocus, _ = canFocus.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	canFocus, _ = canFocus.Update(board.Loaded{Data: board.Data{Tasks: []model.Task{w2Task(1, model.StatusStarted)}}})
	canFocus, effects = canFocus.Update(press('f'))
	wantEffects(t, effects, nil)
	if !strings.HasSuffix(canFocus.Text(), "T1 has no live run") {
		t.Fatalf("f without run = %q", canFocus.Text())
	}
	_, effects = canFocus.Update(press('k'))
	wantEffects(t, effects, nil)
	if !strings.HasSuffix(canFocus.Text(), "T1 has no live run") {
		t.Fatalf("k without run = %q", canFocus.Text())
	}

	paneLess := board.NewState(board.Config{CanFocus: true})
	paneLess, _ = paneLess.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	paneLess, _ = paneLess.Update(board.Loaded{Data: board.Data{Tasks: []model.Task{w2Task(1, model.StatusStarted)}, Runs: []model.Run{{Task: 1}}}})
	paneLess, effects = paneLess.Update(press('f'))
	wantEffects(t, effects, nil)
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
			state, effects := state.Update(press('P'))
			if test.eff == nil {
				wantEffects(t, effects, nil)
				if !strings.HasSuffix(state.Text(), test.want) {
					t.Fatalf("P with %q = %q", test.state, state.Text())
				}
				return
			}
			wantEffects(t, effects, []board.Effect{test.eff})
		})
	}
}

func TestKeysPasteGoesOnlyToTheOpenTextInput(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen))
	before := s.Text()
	after, effects := s.Update(tea.PasteMsg{Content: "ignored"})
	wantEffects(t, effects, nil)
	if after.Text() != before {
		t.Fatalf("paste without input changed screen from %q to %q", before, after.Text())
	}

	s, _ = s.Update(press('+'))
	s, effects = s.Update(tea.PasteMsg{Content: "fix #agent"})
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "add: fix #agent") {
		t.Fatalf("paste into add box = %q, want pasted line", s.Text())
	}
}

func TestKeysPasteUpdatesSearch(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen), w2Task(2, model.StatusOpen))
	s, _ = s.Update(press('/'))
	s, effects := s.Update(tea.PasteMsg{Content: "task a"})
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "search: task a") || strings.Contains(s.Text(), "T2") {
		t.Fatalf("paste into search = %q, want pasted query and only the matching row", s.Text())
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
		{"down selects the next row", named(tea.KeyDown), "▸ T1"},
		{"down stays at the last row", named(tea.KeyDown), "▸ T1"},
		{"j stays at the last row", press('j'), "▸ T1"},
		{"up selects the previous row", named(tea.KeyUp), "▸ T2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var effects []board.Effect
			s, effects = s.Update(test.key)
			wantEffects(t, effects, nil)
			if !strings.Contains(s.Text(), test.want) {
				t.Fatalf("%s = %q, want %q", test.name, s.Text(), test.want)
			}
		})
	}

	empty := w2State()
	for _, key := range []tea.KeyPressMsg{press('g'), press('G'), named(tea.KeyEnter), press('n')} {
		var effects []board.Effect
		empty, effects = empty.Update(key)
		wantEffects(t, effects, nil)
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
			_, effects := s.Update(named(tea.KeyDown))
			wantEffects(t, effects, test.want)
		})
	}
}

func TestKeysAddCancellationSuppressesCaptureQuitAndShowsFailuresExactly(t *testing.T) {
	for _, test := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"escape", named(tea.KeyEsc)},
		{"empty line", named(tea.KeyEnter)},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := w2State(w2Task(1, model.StatusOpen))
			s, _ = s.Update(press('+'))
			s, effects := s.Update(test.key)
			wantEffects(t, effects, nil)
			if strings.Contains(s.Text(), "add: ") {
				t.Fatalf("%s leaves the add box open: %q", test.name, s.Text())
			}
		})
	}

	s := w2State(w2Task(1, model.StatusOpen))
	s, _ = s.Update(press('+'))
	s, _ = s.Update(press('w'))
	s, _ = s.Update(named(tea.KeyEnter))
	s, effects := s.Update(board.Failed{Err: &model.Refusal{Code: model.CodeNotAllowed, Msg: "runner is paused"}})
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "not-allowed: runner is paused") {
		t.Fatalf("refused add = %q, want the refusal text", s.Text())
	}

	s = w2State(w2Task(1, model.StatusOpen))
	s, effects = s.Update(board.Failed{})
	wantEffects(t, effects, nil)
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
			s, _ = s.Update(press('/'))
			for _, r := range test.query {
				var effects []board.Effect
				s, effects = s.Update(press(r))
				wantEffects(t, effects, nil)
			}
			if !strings.Contains(s.Text(), "search: "+test.query) || !strings.Contains(s.Text(), test.want) || strings.Contains(s.Text(), test.gone) {
				t.Fatalf("search %q = %q, want %q only", test.query, s.Text(), test.want)
			}
			s, _ = s.Update(named(tea.KeyEnter))
			s, effects := s.Update(named(tea.KeyEsc))
			wantEffects(t, effects, nil)
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
		filters, _ = filters.Update(press('p'))
	}
	if !strings.Contains(filters.Text(), "desk  no project ▾") || !strings.Contains(filters.Text(), "T14") || strings.Contains(filters.Text(), "T16") {
		t.Fatalf("no project filter = %q, want local tasks only", filters.Text())
	}
	filters, _ = filters.Update(press('t'))
	if !strings.Contains(filters.Text(), "T14") || strings.Contains(filters.Text(), "T15") {
		t.Fatalf("thread filter = %q, want blue local task only", filters.Text())
	}

	offline := board.NewState(board.Config{})
	offline, _ = offline.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	offline, _ = offline.Update(board.Loaded{Data: board.Data{Offline: true}})
	offline, effects := offline.Update(press('d'))
	wantEffects(t, effects, nil)
	if !strings.HasSuffix(offline.Text(), "offline: the done drawer needs the home") {
		t.Fatalf("offline done drawer = %q, want refusal", offline.Text())
	}
}

func TestKeysAddBoxShowsOnlyItsOwnAddTasksFailure(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen))
	s, _ = s.Update(press('+'))
	for _, r := range "fix" {
		s, _ = s.Update(press(r))
	}
	s, effects := s.Update(board.Failed{Err: errors.New("dial home: connection refused")})
	wantEffects(t, effects, nil)
	lines := strings.Split(s.Text(), "\n")
	if box := strings.Join(lines[len(lines)-4:len(lines)-1], "\n"); strings.Contains(box, "connection refused") || !strings.Contains(box, "add: fix") {
		t.Fatalf("an unsubmitted line shows a failure under it:\n%s", s.Text())
	}
	if lines[len(lines)-1] != "dial home: connection refused" {
		t.Fatalf("status line = %q, want the failure", lines[len(lines)-1])
	}

	s, effects = s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{Title: "fix"}}})
	s, _ = s.Update(board.Failed{Err: errors.New("unknown-project: no such project")})
	lines = strings.Split(s.Text(), "\n")
	if got := lines[len(lines)-2]; got != "unknown-project: no such project" || lines[len(lines)-1] != "" {
		t.Fatalf("the add's refusal is not under its line:\n%s", s.Text())
	}
}

func TestKeysAddBoxSendsOneAddTaskUntilItAnswers(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen))
	s, _ = s.Update(press('+'))
	s, _ = s.Update(tea.PasteMsg{Content: "fix"})
	s, effects := s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{Title: "fix"}}})
	for _, msg := range []tea.Msg{named(tea.KeyEnter), press('x'), tea.PasteMsg{Content: "y"}} {
		s, effects = s.Update(msg)
		wantEffects(t, effects, nil)
	}
	if !strings.Contains(s.Text(), "add: fix\n") {
		t.Fatalf("the line changed while its add was out: %q", s.Text())
	}
	s, _ = s.Update(board.Failed{Err: errors.New("refused")})
	_, effects = s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{Title: "fix"}}})

	for _, test := range []struct {
		name   string
		answer tea.Msg
		want   []board.Effect
		status string
	}{
		{"esc then the task lands", board.Added{Task: w2Task(2, model.StatusOpen)}, []board.Effect{board.Refresh{}}, ""},
		{"esc then the add fails", board.Failed{Err: errors.New("refused")}, nil, "refused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := w2State(w2Task(1, model.StatusOpen))
			s, _ = s.Update(press('+'))
			s, _ = s.Update(press('z'))
			s, _ = s.Update(named(tea.KeyEnter))
			s, effects := s.Update(named(tea.KeyEsc))
			wantEffects(t, effects, nil)
			if !strings.Contains(s.Text(), "add: z") {
				t.Fatalf("esc closed the box before its add answered: %q", s.Text())
			}
			s, effects = s.Update(test.answer)
			wantEffects(t, effects, test.want)
			lines := strings.Split(s.Text(), "\n")
			if strings.Contains(s.Text(), "add: ") || lines[len(lines)-1] != test.status {
				t.Fatalf("after the answer the box is open or the status is not %q:\n%s", test.status, s.Text())
			}
		})
	}
}

func TestKeysDoneDrawerWaitsForTheOutstandingRefresh(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen))
	s, effects := s.Update(board.Tick{})
	wantEffects(t, effects, []board.Effect{board.Refresh{}})
	s, effects = s.Update(press('d'))
	wantEffects(t, effects, nil)
	_, effects = s.Update(board.Loaded{Data: board.Data{Tasks: []model.Task{w2Task(1, model.StatusOpen)}}})
	wantEffects(t, effects, []board.Effect{board.Refresh{Done: true}})
}

func TestKeysPasteIntoAConfirmPromptChangesNothing(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen))
	s, _ = s.Update(press('x'))
	before := s.Text()
	s, effects := s.Update(tea.PasteMsg{Content: "y"})
	wantEffects(t, effects, nil)
	if s.Text() != before {
		t.Fatalf("paste into a y/n prompt changed the screen from %q to %q", before, s.Text())
	}
	_, effects = s.Update(press('y'))
	wantEffects(t, effects, []board.Effect{board.SetTask{Task: 1, Patch: model.Patch{Status: w2Status(model.StatusDone)}}})
}

func TestKeysAFailedAnswerOpensItsPromptAgainWithTheText(t *testing.T) {
	s := w2State(w2Task(9, model.StatusBlocked))
	s, _ = s.Update(press('n'))
	s, _ = s.Update(tea.PasteMsg{Content: "keep it"})
	s, effects := s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.Rearm{Task: 9, Answer: "keep it"}})
	if strings.Contains(s.Text(), "answer: ") {
		t.Fatalf("enter left the answer prompt open: %q", s.Text())
	}
	s, _ = s.Update(board.FailedOf(effects[0], errors.New("dial home: connection refused")))
	if !strings.Contains(s.Text(), "answer: keep it") || !strings.HasSuffix(s.Text(), "dial home: connection refused") {
		t.Fatalf("a failed answer = %q, want its prompt back with the text and the error", s.Text())
	}
	_, effects = s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.Rearm{Task: 9, Answer: "keep it"}})
}

func TestKeysAnAnswerThatFailsWhileAPromptIsOpenWaitsForItToClose(t *testing.T) {
	s := w2State(w2Task(9, model.StatusBlocked))
	s, _ = s.Update(press('n'))
	s, _ = s.Update(tea.PasteMsg{Content: "keep it"})
	s, answer := s.Update(named(tea.KeyEnter))
	s, _ = s.Update(press('/'))
	s, _ = s.Update(board.FailedOf(answer[0], errors.New("dial home: connection refused")))
	if text := s.Text(); strings.Contains(text, "answer: ") || !strings.Contains(text, "search: ") {
		t.Fatalf("a failed answer took the keys from the open prompt: %q", text)
	}
	s, _ = s.Update(named(tea.KeyEsc))
	if !strings.Contains(s.Text(), "answer: keep it") || !strings.HasSuffix(s.Text(), "dial home: connection refused") {
		t.Fatalf("after the prompt closed = %q, want the answer prompt back with the text and the error", s.Text())
	}
	_, effects := s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.Rearm{Task: 9, Answer: "keep it"}})
}

func TestKeysEachFailedAnswerGivesBackItsOwnText(t *testing.T) {
	s := w2State(w2Task(9, model.StatusBlocked))
	s, _ = s.Update(press('n'))
	s, _ = s.Update(tea.PasteMsg{Content: "first"})
	s, first := s.Update(named(tea.KeyEnter))
	s, _ = s.Update(press('n'))
	s, _ = s.Update(tea.PasteMsg{Content: "second"})
	s, second := s.Update(named(tea.KeyEnter))
	// The earlier answer fails while the later one is still out.
	s, _ = s.Update(board.FailedOf(first[0], errors.New("first refused")))
	if text := s.Text(); !strings.Contains(text, "answer: first") || !strings.HasSuffix(text, "first refused") {
		t.Fatalf("the earlier answer failed = %q, want its prompt back with its own text", text)
	}
	s, effects := s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.Rearm{Task: 9, Answer: "first"}})
	s, _ = s.Update(board.FailedOf(second[0], errors.New("second refused")))
	if text := s.Text(); !strings.Contains(text, "answer: second") || !strings.HasSuffix(text, "second refused") {
		t.Fatalf("the later answer failed = %q, want its prompt back with its own text", text)
	}
}

func TestKeysAFailedAnswerIsNotLostToALaterAnswer(t *testing.T) {
	s := w2State(w2Task(9, model.StatusBlocked))
	s, _ = s.Update(press('n'))
	s, _ = s.Update(tea.PasteMsg{Content: "keep it"})
	s, answer := s.Update(named(tea.KeyEnter))
	s, _ = s.Update(press('n'))
	s, _ = s.Update(board.FailedOf(answer[0], errors.New("dial home: connection refused")))
	s, _ = s.Update(tea.PasteMsg{Content: "later"})
	s, effects := s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.Rearm{Task: 9, Answer: "later"}})
	if !strings.Contains(s.Text(), "answer: keep it") || !strings.HasSuffix(s.Text(), "dial home: connection refused") {
		t.Fatalf("after a later answer = %q, want the failed answer's prompt back with the text and the error", s.Text())
	}
	_, effects = s.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.Rearm{Task: 9, Answer: "keep it"}})
}
