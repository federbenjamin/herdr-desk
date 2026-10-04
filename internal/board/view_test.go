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
)

var w3Now = time.Date(2026, time.October, 4, 15, 4, 5, 0, time.UTC)

func w3State(width int, data board.Data) board.State {
	s := board.NewState(board.Config{IsHome: true, Now: func() time.Time { return w3Now }})
	s, _ = s.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	s, _ = s.Update(board.Loaded{Data: data})
	return s
}

func w3Key(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: text}
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
	s, _ = s.Update(w3Key('/', "/"))
	for _, r := range "queue" {
		s, _ = s.Update(w3Key(r, string(r)))
	}
	search := s.Text()
	w3RequireContains(t, search, "queue agent")
	for _, absent := range []string{"ship release", "review patch", "sort inbox"} {
		if strings.Contains(search, absent) {
			t.Fatalf("search left non-matching row %q visible:\n%s", absent, search)
		}
	}
	s, _ = s.Update(w3Key(tea.KeyEscape, ""))
	s, _ = s.Update(w3Key('p', "p"))
	project := s.Text()
	w3RequireContains(t, project, "desk  alpha ▾", "ship release", "queue agent")
	if strings.Contains(project, "review patch") {
		t.Fatalf("project filter retained a beta task:\n%s", project)
	}
	s, _ = s.Update(w3Key('t', "t"))
	firstThread := s.Text()
	w3RequireContains(t, firstThread, "thread: ops ▾")
	if strings.Contains(firstThread, "review patch") || strings.Contains(firstThread, "queue agent") {
		t.Fatalf("project and thread filters did not combine:\n%s", firstThread)
	}
	s, _ = s.Update(w3Key('t', "t"))
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
		s, effects = s.Update(w3Key(key.code, key.text))
		if len(effects) != 0 {
			t.Fatalf("offline %q emitted effects %#v", key.text, effects)
		}
		w3RequireContains(t, s.Text(), "offline: "+key.text+" needs the home")
	}
	s, effects := s.Update(w3Key('d', "d"))
	if len(effects) != 0 {
		t.Fatalf("offline done drawer emitted effects %#v", effects)
	}
	w3RequireContains(t, s.Text(), "offline: the done drawer needs the home")
	s, effects = s.Update(w3Key('p', "p"))
	if len(effects) != 0 {
		t.Fatalf("offline project filter emitted effects %#v", effects)
	}
	w3RequireContains(t, s.Text(), "desk  alpha ▾")
}
