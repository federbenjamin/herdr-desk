package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk"
	"github.com/federbenjamin/herdr-desk/internal/board"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

const deskCoordinatorSession = "coordinator-session"

// coordinatorHome is a home on the fake herdr. The pane command a coordinator opens runs this test binary, so the
// binary is made to behave as a home's rpc helper that exits 255 at once: it must not start the suite again inside
// the fake pane. The second return is the fake herdr's state folder.
func coordinatorHome(t *testing.T, cfg config.Config) (*testutil.Home, string) {
	t.Helper()
	dir := testutil.FakeHerdr(t)
	down := t.TempDir()
	if err := os.WriteFile(filepath.Join(down, "down"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DESK_TESTUTIL_RPC", down)
	return testutil.StartHome(t, testutil.HomeOptions{Config: cfg}), dir
}

func fakeCalls(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if _, call, ok := strings.Cut(line, "\t"); ok {
			calls = append(calls, call)
		}
	}
	return calls
}

func callsStarting(calls []string, prefix string) []string {
	var out []string
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func storedCoordinator(t *testing.T, home *testutil.Home) (model.Coordinator, bool) {
	t.Helper()
	st, err := store.Open(home.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open the home's store: %v", err)
	}
	defer st.Close()
	c, ok, err := st.Coordinator(context.Background())
	if err != nil {
		t.Fatalf("read the coordinator: %v", err)
	}
	return c, ok
}

func recordStoredCoordinator(t *testing.T, home *testutil.Home, c model.Coordinator) {
	t.Helper()
	st, err := store.Open(home.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open the home's store: %v", err)
	}
	defer st.Close()
	if err := st.SetCoordinator(context.Background(), store.Actor{}, c); err != nil {
		t.Fatalf("SetCoordinator: %v", err)
	}
}

func TestCoordinatorOpensAWorkspaceRecordsItAndTypesCoordinatorRun(t *testing.T) {
	home, dir := coordinatorHome(t, config.Default())

	result := runHomeDesk(t, home, "coordinator")
	requireSuccess(t, result)
	if result.stdout != "coordinator opened: workspace w1, pane p1\n" {
		t.Fatalf("coordinator stdout = %q, want the opened line naming the workspace and pane", result.stdout)
	}

	rec, ok := storedCoordinator(t, home)
	if !ok || rec.Workspace != "w1" || rec.Pane != "p1" || !model.ValidSessionID(rec.Session) {
		t.Fatalf("recorded coordinator = (%+v, %v), want w1/p1 and a valid new session", rec, ok)
	}

	calls := fakeCalls(t, dir)
	create := callsStarting(calls, "workspace create")
	if len(create) != 1 || !strings.Contains(create[0], "--label desk coordinator") || !strings.Contains(create[0], "--no-focus") ||
		!strings.Contains(create[0], "--env DESK_SESSION="+rec.Session) || !strings.Contains(create[0], "--cwd "+home.Paths.ScratchRoot()) {
		t.Fatalf("workspace create calls = %q, want one in the scratch root labelled desk coordinator, with DESK_SESSION = %s", create, rec.Session)
	}
	run := callsStarting(calls, "pane run")
	if len(run) != 1 || !strings.HasPrefix(run[0], "pane run p1 exec ") || !strings.HasSuffix(run[0], " coordinator run") {
		t.Fatalf("pane run calls = %q, want one typing `exec <binary> coordinator run` into p1", run)
	}
}

func TestCoordinatorReportsTheLivePaneAndRecordsNothingNew(t *testing.T) {
	home, dir := coordinatorHome(t, config.Default())
	// A pane the fake herdr never ran a command in stays open, so the coordinator recorded for it is live.
	created, err := (&herdr.Client{Bin: os.Getenv("DESK_HERDR")}).CreateWorkspace(context.Background(), t.TempDir(), "desk coordinator", nil)
	if err != nil {
		t.Fatalf("create the live pane: %v", err)
	}
	live := model.Coordinator{Session: deskCoordinatorSession, Workspace: created.Workspace, Pane: created.Pane}
	recordStoredCoordinator(t, home, live)

	result := runHomeDesk(t, home, "coordinator")
	requireSuccess(t, result)
	if want := "coordinator open: workspace " + created.Workspace + ", pane " + created.Pane + "\n"; result.stdout != want {
		t.Fatalf("coordinator stdout = %q, want %q", result.stdout, want)
	}

	calls := fakeCalls(t, dir)
	if focus := append(callsStarting(calls, "workspace focus"), callsStarting(calls, "pane zoom")...); len(focus) != 0 {
		t.Fatalf("herdr focus calls = %q, want none: a script's focus moves every attached herdr window", focus)
	}
	if n := len(callsStarting(calls, "workspace create")); n != 1 {
		t.Fatalf("workspace create ran %d times, want only the test's own: reporting opens nothing", n)
	}
	if len(callsStarting(calls, "pane run")) != 0 {
		t.Fatalf("a command was typed into a pane while reporting: %q", calls)
	}
	if got, _ := storedCoordinator(t, home); got.Session != live.Session || got.Pane != live.Pane {
		t.Fatalf("recorded coordinator = %+v after reporting, want it unchanged (%+v)", got, live)
	}
}

func TestCoordinatorOpensANewOneAndKeepsTheCursorWhenItsPaneIsGone(t *testing.T) {
	home, dir := coordinatorHome(t, config.Default())
	// The recorded pane "p77" does not exist in the fake herdr: it was closed.
	recordStoredCoordinator(t, home, model.Coordinator{Session: deskCoordinatorSession, Workspace: "w77", Pane: "p77"})
	addTask(t, home, "an event the new coordinator has not read")
	st, err := store.Open(home.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Changes(context.Background(), store.Actor{Session: deskCoordinatorSession}, 0); err != nil {
		t.Fatal(err)
	}
	moved, _, err := st.Coordinator(context.Background())
	st.Close()
	if err != nil || moved.Cursor == 0 {
		t.Fatalf("setup: cursor = %d, err = %v, want the old coordinator's cursor moved", moved.Cursor, err)
	}

	result := runHomeDesk(t, home, "coordinator")
	requireSuccess(t, result)
	if !strings.HasPrefix(result.stdout, "coordinator opened: ") {
		t.Fatalf("coordinator stdout = %q, want a new one opened", result.stdout)
	}
	rec, _ := storedCoordinator(t, home)
	if rec.Session == deskCoordinatorSession || rec.Pane == "p77" {
		t.Fatalf("recorded coordinator = %+v, want a new session and pane", rec)
	}
	if rec.Cursor != moved.Cursor {
		t.Fatalf("new coordinator's cursor = %d, want the old %d: changes already read must not return", rec.Cursor, moved.Cursor)
	}
	if len(callsStarting(fakeCalls(t, dir), "workspace create")) != 1 {
		t.Fatal("want exactly one workspace created")
	}
}

func TestCoordinatorRefusesAnAgentSessionBeforeOpeningAnything(t *testing.T) {
	home, dir := coordinatorHome(t, config.Default())
	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"coordinator"}, "", map[string]string{"DESK_SESSION": "agent-session"})
	requireRefusal(t, result, "coordinator", model.CodeNotAllowed, 1)
	if calls := fakeCalls(t, dir); len(calls) != 0 {
		t.Fatalf("herdr was called for an agent session: %q", calls)
	}
	if _, ok := storedCoordinator(t, home); ok {
		t.Fatal("an agent session recorded a coordinator")
	}
}

func TestCoordinatorWithNoHerdrIsRefusedNoHerdrAndRecordsNothing(t *testing.T) {
	// testutil points DESK_HERDR at a path that does not exist unless a test calls FakeHerdr.
	home := testutil.StartHome(t, testutil.HomeOptions{})
	requireRefusal(t, runHomeDesk(t, home, "coordinator"), "coordinator", model.CodeNoHerdr, 1)
	if _, ok := storedCoordinator(t, home); ok {
		t.Fatal("a coordinator was recorded with no herdr to hold it")
	}
}

func TestCoordinatorOnAClientMachineSaysToRunItOnTheHome(t *testing.T) {
	home, dir := coordinatorHome(t, config.Default())
	client := testutil.NewClientMachine(t, home)
	result := runDeskWithEnv(t, client, t.TempDir(), []string{"coordinator"}, "", nil)
	requireSuccess(t, result)
	if !strings.Contains(result.stdout, "client of home") || !strings.Contains(result.stdout, "run it on the home") {
		t.Fatalf("coordinator on a client = %q, want it to name the home and say to run it there", result.stdout)
	}
	if calls := fakeCalls(t, dir); len(calls) != 0 {
		t.Fatalf("herdr was called from a client machine: %q", calls)
	}
}

// coordinatorStub puts an executable named coordinator-stub first on PATH and returns its path.
func coordinatorStub(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	stub := filepath.Join(bin, "coordinator-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stub
}

func TestCoordinatorRunExecsTheTemplateWithTheSessionAndTheSkillAsOneArgument(t *testing.T) {
	stub := coordinatorStub(t)
	cfg := config.Default()
	cfg.Agent.Coordinator = []string{"coordinator-stub", "--session={session}", "--prompt={prompt}", "--flag"}
	home := testutil.StartHome(t, testutil.HomeOptions{Config: cfg})

	var gotPath string
	var gotArgv []string
	result := runDeskWithExec(t, home.Machine, []string{"coordinator", "run"}, map[string]string{"DESK_SESSION": deskCoordinatorSession},
		func(path string, argv []string) error {
			gotPath, gotArgv = path, append([]string(nil), argv...)
			return nil
		})
	requireSuccess(t, result)
	if gotPath != stub {
		t.Fatalf("Exec path = %q, want the PATH-resolved stub %q", gotPath, stub)
	}
	want := []string{"coordinator-stub", "--session=" + deskCoordinatorSession, "--prompt=" + herdrdesk.Coordinator(), "--flag"}
	if !reflect.DeepEqual(gotArgv, want) {
		t.Fatalf("Exec argv = %q, want the template expanded with the session and the skill as one argument", gotArgv)
	}
}

func TestCoordinatorRunRefusesWhatItCannotExec(t *testing.T) {
	coordinatorStub(t)
	good := config.Default()
	good.Agent.Coordinator = []string{"coordinator-stub", "{session}", "{prompt}"}
	for _, test := range []struct {
		name     string
		cfg      config.Config
		env      map[string]string
		wantExit int
	}{
		{"no session in the environment", good, nil, 2},
		{"a session id that is not valid", good, map[string]string{"DESK_SESSION": "../escape"}, 2},
		{"an empty template", func() config.Config { c := good; c.Agent.Coordinator = nil; return c }(), map[string]string{"DESK_SESSION": "s1"}, 3},
		{"a template whose program is not found", func() config.Config {
			c := good
			c.Agent.Coordinator = []string{"no-such-coordinator-program"}
			return c
		}(), map[string]string{"DESK_SESSION": "s1"}, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := testutil.StartHome(t, testutil.HomeOptions{Config: test.cfg})
			execCalled := false
			result := runDeskWithExec(t, home.Machine, []string{"coordinator", "run"}, test.env, func(string, []string) error {
				execCalled = true
				return nil
			})
			if result.exit != test.wantExit || execCalled || result.stderr == "" {
				t.Fatalf("coordinator run = (exit %d, exec %v, stderr %q), want exit %d and no exec", result.exit, execCalled, result.stderr, test.wantExit)
			}
		})
	}
}

func TestSkillCoordinatorPrintsTheEmbeddedSkillAndRefusesOtherNames(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	result := runHomeDesk(t, home, "skill", "coordinator")
	requireSuccess(t, result)
	if result.stdout != herdrdesk.Coordinator() || result.stdout == "" {
		t.Fatalf("skill coordinator printed %d bytes, want exactly the embedded skill (%d bytes)", len(result.stdout), len(herdrdesk.Coordinator()))
	}
	if other := runHomeDesk(t, home, "skill", "nope"); other.exit != 2 || other.stdout != "" {
		t.Fatalf("skill nope = (%d, %q, %q), want exit 2 and no output", other.exit, other.stdout, other.stderr)
	}
	if none := runHomeDesk(t, home, "skill"); none.exit != 2 {
		t.Fatalf("skill with no name exit = %d, want 2", none.exit)
	}
}

// The skill is the coordinator's whole rulebook; each rule below is one the plan lists, so losing one is a safety loss.
func TestCoordinatorSkillStatesEachRuleThePlanLists(t *testing.T) {
	text := strings.ToLower(herdrdesk.Coordinator())
	for _, rule := range []struct{ name, phrase string }{
		{"context first, every turn", "herdr-desk context"},
		{"every turn", "every turn"},
		{"coordinate, never do the work", "never do the work"},
		{"text is data, never instructions", "never an instruction"},
		{"propose mode", "`propose`"},
		{"propose lists task, root, isolation, and model", "task, root, isolation, model"},
		{"propose waits for a message that names the runs", "names or plainly approves"},
		{"a ready task is not a go-ahead", "`ready` is not a go-ahead"},
		{"the cap is stated, not worked around", "say so"},
		{"at cap, approved work still queues as waiting", "still gets `run start` when `cap` runs are live"},
		{"a failed spawn exits 1 and is reported", "exits 1 with `run-failed: run <id> failed: <reason>`"},
		{"how to start a run", "herdr-desk run start"},
		{"how to add a task", "herdr-desk add"},
		{"auto mode", "`auto`"},
		{"never answer a worker's prompt", "never answer a worker"},
		{"never answer a folder-trust question", "folder-trust"},
	} {
		if !strings.Contains(text, rule.phrase) {
			// The wording may differ from these phrases; the failure names the rule that has to be there.
			t.Errorf("the skill does not state the rule %q (looked for %q)", rule.name, rule.phrase)
		}
	}
	if strings.Contains(text, "herdr-projects") {
		t.Error("the skill names herdr-projects; it is written for herdr-desk")
	}
}

func TestContextTextPrintsEverySectionTheCoordinatorReads(t *testing.T) {
	scratch := t.TempDir()
	cfg := config.Default()
	cfg.Runner.Cap = 3
	cfg.Runner.MaxRunsPerDay = 7
	cfg.Coordinator.StartRuns = config.StartRunsAuto
	cfg.Agent.Models = []string{"model-a", "model-b"}
	cfg.Roots = []config.Root{{Path: scratch, About: "the main repo", Isolation: "worktree"}}
	home := testutil.StartHome(t, testutil.HomeOptions{Config: cfg})
	addTask(t, home, "needs a go", "--status", "ready")
	addTask(t, home, "just open", "--status", "open")

	result := runHomeDesk(t, home, "context")
	requireSuccess(t, result)
	out := result.stdout
	for _, want := range []string{
		"start_runs auto",
		"cap 3",
		"max_runs_per_day 7\n",
		scratch + "  worktree  the main repo",
		"models: model-a, model-b",
		"live runs:\n  -",
		"changes since e0:",
		"T1  ready  needs a go",
		"T2  open  just open",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("context output lacks %q\n%s", want, out)
		}
	}
	// The board's own sections, in the board's order.
	last := -1
	for _, s := range board.Sections() {
		i := strings.Index(out, "\n"+s.Title+"\n")
		if i < 0 || i < last {
			t.Fatalf("section %q is missing or out of order in\n%s", s.Title, out)
		}
		last = i
	}
	// A long payload is cut on the changes line; the whole is for show.
	long := strings.Repeat("n", 300)
	requireSuccess(t, runHomeDesk(t, home, "edit", "T2", "--notes", long))
	out = runHomeDesk(t, home, "context").stdout
	if strings.Contains(out, long) || !strings.Contains(out, "more; herdr-desk show has it all)") {
		t.Fatalf("a 300-rune notes payload is not cut on the changes line:\n%s", out)
	}
	if i, j := strings.Index(out, "T1  ready"), strings.Index(out, "ON DECK"); i < j {
		t.Fatalf("a ready task printed before the ON DECK section:\n%s", out)
	}
	scratchAt := strings.Index(out, home.Paths.ScratchRoot())
	if scratchAt < 0 || scratchAt < strings.Index(out, scratch) {
		t.Fatalf("the scratch root %q must be listed after the configured roots:\n%s", home.Paths.ScratchRoot(), out)
	}
}

func TestContextJSONIsOneObjectWithTheSameFacts(t *testing.T) {
	scratch := t.TempDir()
	cfg := config.Default()
	cfg.Runner.Cap = 4
	cfg.Runner.MaxRunsPerDay = 9
	cfg.Agent.Models = []string{"model-a"}
	cfg.Roots = []config.Root{{Path: scratch, About: "about text", Isolation: "in-place"}}
	home := testutil.StartHome(t, testutil.HomeOptions{Config: cfg})
	addTask(t, home, "blocked one", "--status", "blocked")

	result := runHomeDesk(t, home, "context", "--json")
	requireSuccess(t, result)
	var d struct {
		StartRuns     string   `json:"start_runs"`
		Cap           int      `json:"cap"`
		Today         int      `json:"today"`
		MaxRunsPerDay int      `json:"max_runs_per_day"`
		Models        []string `json:"models"`
		Roots         []struct {
			Path, About, Isolation string
		} `json:"roots"`
		Board []struct {
			Title string       `json:"title"`
			Tasks []model.Task `json:"tasks"`
		} `json:"board"`
		Runs    []model.Run   `json:"runs"`
		Changes store.Changes `json:"changes"`
	}
	dec := json.NewDecoder(strings.NewReader(result.stdout))
	if err := dec.Decode(&d); err != nil {
		t.Fatalf("context --json is not one object: %v\n%s", err, result.stdout)
	}
	if dec.More() {
		t.Fatalf("context --json printed more than one value:\n%s", result.stdout)
	}
	if d.StartRuns != "propose" || d.Cap != 4 || d.Today != 0 || d.MaxRunsPerDay != 9 || !reflect.DeepEqual(d.Models, []string{"model-a"}) {
		t.Fatalf("context JSON scalars = %+v", d)
	}
	if len(d.Roots) < 2 || d.Roots[0].Path != scratch || d.Roots[0].About != "about text" || d.Roots[len(d.Roots)-1].Path != home.Paths.ScratchRoot() {
		t.Fatalf("context JSON roots = %+v, want the configured root first and the scratch root last", d.Roots)
	}
	sections := board.Sections()
	if len(d.Board) != len(sections) {
		t.Fatalf("context JSON board has %d sections, want board.Sections()'s %d", len(d.Board), len(sections))
	}
	for i, s := range sections {
		if d.Board[i].Title != s.Title {
			t.Fatalf("board section %d = %q, want %q", i, d.Board[i].Title, s.Title)
		}
	}
	if len(d.Board[0].Tasks) != 1 || d.Board[0].Tasks[0].Title != "blocked one" {
		t.Fatalf("NEEDS YOU tasks = %+v, want the blocked task", d.Board[0].Tasks)
	}
	if d.Runs == nil || len(d.Runs) != 0 || len(d.Changes.Events) == 0 {
		t.Fatalf("runs = %#v, changes = %+v, want an empty (non-null) runs array and the add's events", d.Runs, d.Changes)
	}
	if strings.Contains(result.stdout, `"runs":null`) {
		t.Fatalf("runs printed as null:\n%s", result.stdout)
	}
}

func TestContextMovesTheCursorOnlyForTheCoordinatorsSession(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	recordStoredCoordinator(t, home, model.Coordinator{Session: deskCoordinatorSession, Workspace: "w1", Pane: "p1"})
	addTask(t, home, "first")

	read := func(env map[string]string) store.Changes {
		t.Helper()
		result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"context", "--json"}, "", env)
		requireSuccess(t, result)
		var d struct {
			Changes store.Changes `json:"changes"`
		}
		if err := json.Unmarshal([]byte(result.stdout), &d); err != nil {
			t.Fatalf("context --json: %v\n%s", err, result.stdout)
		}
		return d.Changes
	}
	person1, person2 := read(nil), read(nil)
	if person1.From != 0 || person2.From != 0 || len(person2.Events) != len(person1.Events) || len(person1.Events) == 0 {
		t.Fatalf("a person's reads = %+v then %+v, want the same events from e0 each time", person1, person2)
	}
	env := map[string]string{"DESK_SESSION": deskCoordinatorSession}
	first := read(env)
	second := read(env)
	if first.From != 0 || len(first.Events) == 0 || second.From != first.To || len(second.Events) != 0 {
		t.Fatalf("the coordinator's reads = %+v then %+v, want the second to start where the first ended with nothing new", first, second)
	}
	addTask(t, home, "second")
	third := read(env)
	if third.From != first.To || len(third.Events) == 0 {
		t.Fatalf("after a new task the coordinator's read = %+v, want only the new events after e%d", third, first.To)
	}
	if text := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"context"}, "", env); !strings.Contains(text.stdout, "changes since e"+strconv.FormatInt(third.To, 10)+":") {
		t.Fatalf("context text = %q, want the cursor the last read left (e%d)", text.stdout, third.To)
	}
}

// A run the store holds as running whose pane herdr no longer lists is over; context must say so, not list it live.
func TestContextReconcilesOnceSoAVanishedPaneIsNotALiveRun(t *testing.T) {
	cfg := config.Default()
	cfg.Runner.Enabled = true
	home, dir := coordinatorHome(t, cfg)
	task := addTask(t, home, "its pane vanished")
	st, err := store.Open(home.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.StartRun(context.Background(), store.Actor{}, task, store.RunRoute{Root: t.TempDir(), Isolation: "in-place", Model: "m"}, store.RunCaps{Slots: 5, PerDay: 1000})
	if err == nil {
		_, err = st.UpdateRun(context.Background(), run.ID, run.State, store.RunUpdate{State: model.RunRunning, Session: "worker-session", Workspace: "w99", Pane: "p99"})
	}
	st.Close()
	if err != nil {
		t.Fatalf("seed a running run: %v", err)
	}

	result := runHomeDesk(t, home, "context", "--json")
	requireSuccess(t, result)
	var d struct {
		Runs []model.Run `json:"runs"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &d); err != nil {
		t.Fatalf("context --json: %v\n%s", err, result.stdout)
	}
	if len(d.Runs) != 0 {
		t.Fatalf("live runs = %+v, want none: the run's pane is gone from herdr", d.Runs)
	}
	if n := len(callsStarting(fakeCalls(t, dir), "pane list")); n != 1 {
		t.Fatalf("herdr was asked for its panes %d times, want one reconcile", n)
	}
}
