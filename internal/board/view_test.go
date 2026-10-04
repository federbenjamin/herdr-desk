package board_test

import (
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/board"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

var w3Now = time.Date(2026, time.October, 4, 15, 4, 5, 0, time.UTC)

func w3State(width int, data board.Data) board.State {
	s := board.NewState(board.Config{IsHome: true, Now: func() time.Time { return w3Now }})
	s, _ = s.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	s, _ = s.Update(board.Loaded{Data: data})
	return s
}

func w3LiveData() board.Data {
	return board.Data{
		Tasks: []model.Task{
			{Number: 1, Title: "ship release", Status: model.StatusBlocked, Project: "/work/alpha", UpdatedTS: w3Now.Add(-40 * time.Second)},
			{Number: 2, Title: "review patch", Status: model.StatusReview, Project: "/work/beta", UpdatedTS: w3Now.Add(-5 * time.Minute), Thread: "ops"},
			{Number: 3, Title: "run checks", Status: model.StatusStarted, Project: "/work/alpha", Root: "/work/alpha", UpdatedTS: w3Now.Add(-2 * time.Minute)},
			{Number: 4, Title: "queue agent", Status: model.StatusReady, Project: "/work/alpha", Thread: "agent", UpdatedTS: w3Now.Add(-30 * time.Minute)},
			{Number: 5, Title: "sort inbox", Status: model.StatusOpen, UpdatedTS: w3Now.Add(-30 * time.Hour)},
		},
		Runs: []model.Run{{Task: 3, Root: "/work/alpha", Isolation: "worktree", Model: "gpt", StartedTS: w3Now.Add(-30 * time.Hour)}},
		Status: api.Status{
			RunnerOn:    true,
			RunnerState: api.RunnerStateOn,
			RunnerCap:   4,
		},
		Notes: map[int]string{1: "await deploy", 3: "watching logs"},
	}
}

func w3RequireContains(t *testing.T, text string, want ...string) {
	t.Helper()
	for _, part := range want {
		if !strings.Contains(text, part) {
			t.Fatalf("screen does not contain %q:\n%s", part, text)
		}
	}
}

func TestViewSectionsAndAgeKeepTheBoardVocabularyStable(t *testing.T) {
	wantSections := []board.Section{
		{Title: "NEEDS YOU", Statuses: []model.Status{model.StatusBlocked, model.StatusReview}},
		{Title: "IN MOTION", Statuses: []model.Status{model.StatusStarted}},
		{Title: "ON DECK", Statuses: []model.Status{model.StatusReady, model.StatusOpen}},
	}
	if got := board.Sections(); !reflect.DeepEqual(got, wantSections) {
		t.Fatalf("Sections() = %#v, want %#v", got, wantSections)
	}

	for _, tc := range []struct {
		duration time.Duration
		want     string
	}{
		{duration: -time.Second, want: "0s"},
		{duration: 40 * time.Second, want: "40s"},
		{duration: 5 * time.Minute, want: "5m"},
		{duration: 30 * time.Hour, want: "30h"},
		{duration: 48 * time.Hour, want: "2d"},
	} {
		if got := board.Age(tc.duration); got != tc.want {
			t.Errorf("Age(%s) = %q, want %q", tc.duration, got, tc.want)
		}
	}
}

func TestViewWideBoardShowsEverySectionRowAndDetail(t *testing.T) {
	s := w3State(120, w3LiveData())
	text := s.Text()
	w3RequireContains(t, text,
		"desk  all ▾  thread: all ▾",
		"runner ● on · 1/4 · home",
		"NEEDS YOU", "IN MOTION", "ON DECK",
		"T1", "blocked", "ship release", "↳ \"await deploy\"",
		"T2", "review", "review patch", "#ops",
		"T3", "started", "run checks", "↳ last note: \"watching logs\"",
		"T4", "ready", "#agent · queued", "inbox",
		"T5", "open", "sort inbox",
		"root - · isolation - · model -", // The wide layout draws the task beside the board.
	)
	for _, line := range strings.Split(text, "\n") {
		if len([]rune(line)) > 120 {
			t.Fatalf("line is %d columns wide at width 120: %q", len([]rune(line)), line)
		}
	}
	if strings.ContainsRune(text, '\x1b') {
		t.Fatalf("Text() contains an escape byte: %q", text)
	}
	rendered := s.Render()
	for _, forbidden := range []string{"38;2;", "48;2;", "38;5;", "48;5;"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("Render() contains non-palette colour sequence %q: %q", forbidden, rendered)
		}
	}
}

func TestViewMediumBoardShowsTheUntruncatedRowDetails(t *testing.T) {
	s := w3State(109, w3LiveData())
	w3RequireContains(t, s.Text(),
		"T1  blocked  ship release", "alpha · 40s ago", "↳ \"await deploy\"",
		"T2  review   review patch", "#ops · beta · 5m ago",
		"T3  started  run checks", "alpha · worktree · gpt · 30h", "↳ last note: \"watching logs\"",
		"T4  ready    queue agent", "#agent · queued · alpha · 30m ago",
		"inbox", "T5  open     sort inbox", "30h ago",
	)
}

func TestViewNarrowBoardDropsDetailsAndUsesShortFooter(t *testing.T) {
	s := w3State(60, w3LiveData())
	text := s.Text()
	w3RequireContains(t, text, "T1", "ship release", "? keys  q quit")
	for _, absent := range []string{"alpha · 40s ago", "#agent · queued", "worktree · gpt"} {
		if strings.Contains(text, absent) {
			t.Fatalf("narrow board retained row detail %q:\n%s", absent, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if len([]rune(line)) > 60 {
			t.Fatalf("line is %d columns wide at width 60: %q", len([]rune(line)), line)
		}
	}
}

func TestViewFooterWrapsAtDoubleSpaceGroups(t *testing.T) {
	text := w3State(90, w3LiveData()).Text()
	w3RequireContains(t, text,
		"+ add  n ready  s start  b blocked  r review  x done  a #agent  f focus  k kill  P pause",
		"/ search  p project  t thread  d done  ? keys",
	)
	for _, line := range strings.Split(text, "\n") {
		if len([]rune(line)) > 90 {
			t.Fatalf("line is %d columns wide at width 90: %q", len([]rune(line)), line)
		}
	}
}

func TestViewFiltersRowsBySearchProjectAndThread(t *testing.T) {
	s := w3State(90, w3LiveData())
	s, _ = s.Update(press('/'))
	for _, r := range "queue" {
		s, _ = s.Update(press(r))
	}
	search := s.Text()
	w3RequireContains(t, search, "queue agent")
	for _, absent := range []string{"ship release", "review patch", "sort inbox"} {
		if strings.Contains(search, absent) {
			t.Fatalf("search left non-matching row %q visible:\n%s", absent, search)
		}
	}
	s, _ = s.Update(named(tea.KeyEscape))
	s, _ = s.Update(press('p'))
	project := s.Text()
	w3RequireContains(t, project, "desk  alpha ▾", "ship release", "queue agent")
	if strings.Contains(project, "review patch") {
		t.Fatalf("project filter retained a beta task:\n%s", project)
	}
	s, _ = s.Update(press('t'))
	firstThread := s.Text()
	w3RequireContains(t, firstThread, "thread: ops ▾")
	if strings.Contains(firstThread, "review patch") || strings.Contains(firstThread, "queue agent") {
		t.Fatalf("project and thread filters did not combine:\n%s", firstThread)
	}
	s, _ = s.Update(press('t'))
	thread := s.Text()
	w3RequireContains(t, thread, "thread: agent ▾", "queue agent")
	if strings.Contains(thread, "ship release") {
		t.Fatalf("thread filter retained a task without the selected thread:\n%s", thread)
	}
}

func TestViewRunnerLabelsUseTheReportedRunnerState(t *testing.T) {
	cases := []struct {
		name string
		data board.Data
		want string
	}{
		{
			name: "on without a cap counts live runs",
			data: board.Data{Runs: []model.Run{{}, {}}, Status: api.Status{RunnerOn: true}},
			want: "runner ● on · 2 · home",
		},
		{
			name: "paused reports its configured capacity",
			data: board.Data{Runs: []model.Run{{}}, Status: api.Status{RunnerState: api.RunnerStatePaused, RunnerCap: 3}},
			want: "runner ◐ paused · 1/3 · home",
		},
		{
			name: "off omits a run count",
			data: board.Data{Status: api.Status{RunnerState: api.RunnerStateOff}},
			want: "runner ○ off · home",
		},
		{
			name: "unavailable state is displayed verbatim",
			data: board.Data{Status: api.Status{RunnerState: api.RunnerStateNoRouter}},
			want: "runner ○ no-router · home",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w3RequireContains(t, w3State(90, tc.data).Text(), tc.want)
		})
	}

	s := board.NewState(board.Config{Now: func() time.Time { return w3Now }})
	s, _ = s.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	s, _ = s.Update(board.Loaded{Data: w3LiveData()})
	w3RequireContains(t, s.Text(), "runner ● on · 1/4 · client")
}

func TestViewOfflineRefusesWritesButKeepsFiltersAvailable(t *testing.T) {
	snapshot := w3Now.Add(-5 * time.Minute)
	data := w3LiveData()
	data.Offline = true
	data.SnapshotTS = &snapshot
	data.Runs = nil
	data.Status = api.Status{}
	s := w3State(90, data)
	w3RequireContains(t, s.Text(), "offline (snapshot 5m)")

	for _, key := range []struct {
		code rune
		text string
	}{
		{'+', "+"}, {'n', "n"}, {'s', "s"}, {'b', "b"}, {'r', "r"}, {'x', "x"}, {'a', "a"}, {'k', "k"}, {'P', "P"},
	} {
		var effects []board.Effect
		s, effects = s.Update(press(key.code))
		if len(effects) != 0 {
			t.Fatalf("offline %q emitted effects %#v", key.text, effects)
		}
		w3RequireContains(t, s.Text(), "offline: "+key.text+" needs the home")
	}
	s, effects := s.Update(press('d'))
	if len(effects) != 0 {
		t.Fatalf("offline done drawer emitted effects %#v", effects)
	}
	w3RequireContains(t, s.Text(), "offline: the done drawer needs the home")
	s, effects = s.Update(press('p'))
	if len(effects) != 0 {
		t.Fatalf("offline project filter emitted effects %#v", effects)
	}
	w3RequireContains(t, s.Text(), "desk  alpha ▾")
}

// w3Inject holds escape sequences that would act on a terminal: clear the screen, set the window title, blink,
// and a C1 control.
const w3Inject = "\x1b[2J\x1b]0;owned\x07\x1b[5m\u009b\r"

var w3BoardSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// w3RequireInert fails when the screen holds a control character, or when Render holds anything but the board's
// own colour sequences.
func w3RequireInert(t *testing.T, s board.State) {
	t.Helper()
	for _, r := range s.Text() {
		if r != '\n' && unicode.IsControl(r) {
			t.Fatalf("Text holds the control character %q:\n%q", r, s.Text())
		}
	}
	render := s.Render()
	if strings.Contains(render, "\x1b[5m") {
		t.Fatalf("Render holds an injected blink sequence:\n%q", render)
	}
	for _, r := range w3BoardSGR.ReplaceAllString(render, "") {
		if r != '\n' && unicode.IsControl(r) {
			t.Fatalf("Render holds the control character %q outside the board's colours:\n%q", r, render)
		}
	}
}

func TestViewTaskTextReachesTheTerminalAsInertText(t *testing.T) {
	task := model.Task{
		Number: 1, Status: model.StatusStarted, UpdatedTS: w3Now,
		Title: "title" + w3Inject, Project: "/work/alpha" + w3Inject, Thread: "ops" + w3Inject,
		Notes: "notes" + w3Inject + "\nsecond" + w3Inject, Root: "/root" + w3Inject, Isolation: "self" + w3Inject, Model: "m" + w3Inject,
		Steps: []model.Step{{ShortID: "s1", Text: "step" + w3Inject}},
	}
	blocked := model.Task{Number: 2, Status: model.StatusBlocked, Title: "b", UpdatedTS: w3Now, Thread: "agent"}
	data := board.Data{
		Tasks:  []model.Task{task, blocked},
		Runs:   []model.Run{{Task: 1, Root: "/root" + w3Inject, Isolation: "self" + w3Inject, Model: "m" + w3Inject, StartedTS: w3Now}},
		Status: api.Status{RunnerState: "odd" + w3Inject},
		Notes:  map[int]string{1: "last" + w3Inject, 2: "ask" + w3Inject},
	}
	history := []model.Event{
		{TS: w3Now, Kind: model.KindNote, Data: model.MustData(model.NoteData{Text: "note" + w3Inject, Ref: "docs/a.md" + w3Inject})},
		{TS: w3Now, Kind: model.KindDecision, Data: model.MustData(model.DecisionData{Text: "decide" + w3Inject})},
		{TS: w3Now, Kind: model.KindSet, Data: model.MustData(model.Patch{Thread: &task.Thread, Ref: "https://example.test/" + w3Inject})},
	}

	for _, width := range []int{60, 90, 120} {
		s := w3State(width, data)
		w3RequireInert(t, s)
		w3RequireContains(t, s.Text(), "title[2J]0;owned[5m")
		s, _ = s.Update(press('G'))
		s, _ = s.Update(named(tea.KeyEnter))
		s, _ = s.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: task, History: history}})
		w3RequireInert(t, s)
		if width >= 90 {
			w3RequireContains(t, s.Text(), "docs/a.md[2J]0;owned[5m")
		}
		s, _ = s.Update(press('o'))
		w3RequireInert(t, s)
		s, _ = s.Update(board.Failed{Err: errors.New("refused" + w3Inject)})
		w3RequireInert(t, s)
	}
}
