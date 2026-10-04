package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/herdr"
	"github.com/federbenjamin/desk/internal/herdr/herdrtest"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/runner"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
)

type fireFixture struct {
	t       *testing.T
	ctx     context.Context
	paths   config.Paths
	store   *store.Store
	config  config.Config
	herdr   *herdrtest.Herdr
	now     time.Time
	states  []string
	logs    []string
	router  string
	root    string
	timeout time.Duration
}

func newFireFixture(t *testing.T) *fireFixture {
	t.Helper()
	m := testutil.NewMachine(t)
	f := &fireFixture{
		t:       t,
		ctx:     context.Background(),
		paths:   m.Paths,
		now:     time.Date(2026, time.October, 4, 15, 0, 0, 0, time.Local),
		herdr:   herdrtest.NewHerdr(),
		root:    t.TempDir(),
		states:  []string{},
		timeout: time.Second,
	}
	var err error
	f.store, err = store.Open(f.paths.DB(), store.Options{Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = f.store.Close() })
	f.config = config.Default()
	f.config.Runner.Enabled = true
	f.config.Runner.Cap = 3
	f.config.Runner.MaxRunsPerDay = 20
	f.config.Roots = []config.Root{{Path: f.root, About: "test root", Isolation: "self"}}
	f.config.Agent.Models = []string{"model-a"}
	f.router = f.writeRouter(`{"root":%q,"isolation":"self","model":"model-a","reason":"fits"}`)
	f.config.Agent.Router = []string{f.router, "{system}", "{schema}"}
	return f
}

func (f *fireFixture) writeRouter(format string) string {
	f.t.Helper()
	path := filepath.Join(f.t.TempDir(), "router")
	answer := fmt.Sprintf(format, f.root)
	text := "#!/bin/sh\n" +
		"if [ -n \"$ROUTER_STDIN\" ]; then cat > \"$ROUTER_STDIN\"; fi\n" +
		"if [ -n \"$ROUTER_ARGV\" ]; then printf '%s\\n' \"$@\" > \"$ROUTER_ARGV\"; fi\n" +
		"if [ -n \"$ROUTER_HOOKS\" ]; then printf '%s' \"$DESK_HOOKS\" > \"$ROUTER_HOOKS\"; fi\n" +
		"printf '%s\\n' '" + answer + "'\n"
	if err := os.WriteFile(path, []byte(text), 0o700); err != nil {
		f.t.Fatalf("write router: %v", err)
	}
	return path
}

func (f *fireFixture) runner() *runner.Runner {
	f.t.Helper()
	var h runner.Herdr
	if f.herdr != nil {
		h = f.herdr
	}
	return runner.New(runner.Options{
		Store:         f.store,
		Config:        f.config,
		Paths:         f.paths,
		Herdr:         h,
		Exe:           "/opt/desk/bin/desk",
		Now:           func() time.Time { return f.now },
		RouterTimeout: f.timeout,
		KillGrace:     10 * time.Millisecond,
		OnState:       func(state string) { f.states = append(f.states, state) },
		Logf:          func(format string, args ...any) { f.logs = append(f.logs, fmt.Sprintf(format, args...)) },
	})
}

func (f *fireFixture) arm(title, thread string) model.Task {
	f.t.Helper()
	task, err := f.store.AddTask(f.ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: title, Thread: thread}})
	if err != nil {
		f.t.Fatalf("add %q: %v", title, err)
	}
	ready := model.StatusReady
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &ready}); err != nil {
		f.t.Fatalf("arm T%d: %v", task.Number, err)
	}
	return task
}

func (f *fireFixture) runs() []model.Run {
	f.t.Helper()
	runs, err := f.store.ListRuns(f.ctx)
	if err != nil {
		f.t.Fatalf("list runs: %v", err)
	}
	return runs
}

func (f *fireFixture) task(number int) store.TaskDetail {
	f.t.Helper()
	task, err := f.store.GetTask(f.ctx, number)
	if err != nil {
		f.t.Fatalf("get T%d: %v", number, err)
	}
	return task
}

func fireString(s string) *string { return &s }

func TestTickStartsArmedTasksInArmingOrderUpToTheCap(t *testing.T) {
	f := newFireFixture(t)
	f.config.Runner.Cap = 3
	first := f.arm("first", "agent")
	second := f.arm("second", "agent")
	third := f.arm("third", "agent")
	fourth := f.arm("fourth", "agent")

	f.runner().Tick(f.ctx)
	runs := f.runs()
	if len(runs) != 3 {
		t.Fatalf("started runs = %#v, want three", runs)
	}
	if got, want := []int{runs[0].Task, runs[1].Task, runs[2].Task}, []int{first.Number, second.Number, third.Number}; !reflect.DeepEqual(got, want) {
		t.Fatalf("started task order = %v, want %v", got, want)
	}
	if got := f.herdr.Workspaces(); len(got) != 3 || got[0].Cwd != f.root || got[0].Command == "" {
		t.Fatalf("workspaces = %#v, want one started workspace per run rooted at %q", got, f.root)
	}
	if got := f.task(fourth.Number).Task.Status; got != model.StatusReady {
		t.Fatalf("fourth task status = %q, want ready while cap is full", got)
	}
	if changed, err := f.store.UpdateRun(f.ctx, runs[0].ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !changed {
		t.Fatalf("end first run = (%t, %v), want (true, nil)", changed, err)
	}
	f.runner().Tick(f.ctx)
	runs = f.runs()
	if len(runs) != 4 || runs[3].Task != fourth.Number {
		t.Fatalf("runs after a slot opens = %#v, want fourth task started", runs)
	}
}

func TestTickDoesNotStartTasksThatAreNotArmed(t *testing.T) {
	f := newFireFixture(t)
	if _, err := f.store.AddTask(f.ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "open", Thread: "agent"}}); err != nil {
		t.Fatal(err)
	}
	f.arm("other thread", "user")
	archived := f.arm("archived", "agent")
	trueValue := true
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, archived.Number, model.Patch{Archived: &trueValue}); err != nil {
		t.Fatalf("archive task: %v", err)
	}

	f.runner().Tick(f.ctx)
	if got := f.runs(); len(got) != 0 {
		t.Fatalf("runs = %#v, want none", got)
	}
}

func TestTickCountsOnlyRunsStartedSinceLocalMidnight(t *testing.T) {
	f := newFireFixture(t)
	f.config.Runner.MaxRunsPerDay = 1
	yesterday := f.arm("yesterday", "agent")
	f.runner().Tick(f.ctx)
	runs := f.runs()
	if len(runs) != 1 {
		t.Fatalf("initial runs = %#v, want one", runs)
	}
	if changed, err := f.store.UpdateRun(f.ctx, runs[0].ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !changed {
		t.Fatalf("end yesterday run = (%t, %v)", changed, err)
	}
	f.now = f.now.AddDate(0, 0, 1)
	today := f.arm("today", "agent")
	f.runner().Tick(f.ctx)
	runs = f.runs()
	if len(runs) != 2 || runs[1].Task != today.Number || runs[0].Task != yesterday.Number {
		t.Fatalf("runs across midnight = %#v, want yesterday then today", runs)
	}
}

func TestTickPassesPromptSchemaInputAndHooksToTheRouter(t *testing.T) {
	f := newFireFixture(t)
	stdin := filepath.Join(t.TempDir(), "stdin")
	argv := filepath.Join(t.TempDir(), "argv")
	hooks := filepath.Join(t.TempDir(), "hooks")
	t.Setenv("ROUTER_STDIN", stdin)
	t.Setenv("ROUTER_ARGV", argv)
	t.Setenv("ROUTER_HOOKS", hooks)
	task := f.arm("route me", "agent")

	f.runner().Tick(f.ctx)
	input, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatalf("read router stdin: %v", err)
	}
	var gotInput struct {
		Task struct {
			Number int `json:"number"`
		} `json:"task"`
		Roots []struct {
			Path string `json:"path"`
		} `json:"roots"`
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(input, &gotInput); err != nil {
		t.Fatalf("router stdin is not JSON: %v", err)
	}
	if gotInput.Task.Number != task.Number || len(gotInput.Roots) < 1 || gotInput.Roots[0].Path != f.root || !reflect.DeepEqual(gotInput.Models, f.config.Agent.Models) {
		t.Fatalf("router input = %#v, want task, listed root, and models", gotInput)
	}
	arguments, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("read router argv: %v", err)
	}
	parts := strings.Split(strings.TrimSpace(string(arguments)), "\n")
	if len(parts) != 2 {
		t.Fatalf("router argv = %q, want system path and schema text", arguments)
	}
	system, err := os.ReadFile(parts[0])
	if err != nil {
		t.Fatalf("read router system: %v", err)
	}
	if string(system) != desk.RouterSystem() {
		t.Fatalf("router system = %q, want embedded router system", system)
	}
	if info, err := os.Stat(parts[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("router system mode = %v (%v), want 0600", info, err)
	}
	if !strings.Contains(parts[1], f.root) {
		t.Fatalf("router schema = %q, want listed root", parts[1])
	}
	if got, err := os.ReadFile(hooks); err != nil || string(got) != "off" {
		t.Fatalf("DESK_HOOKS = %q (%v), want off", got, err)
	}

	detail := f.task(task.Number)
	run := f.runs()[0]
	if detail.Task.Root != f.root || detail.Task.Isolation != "self" || detail.Task.Model != "model-a" || run.Root != f.root || run.Isolation != "self" || run.Model != "model-a" || run.Reason != "fits" {
		t.Fatalf("task = %#v; run = %#v; want applied route", detail.Task, run)
	}
	if !fireHasRouterNote(detail.History, "routed to "+f.root+" (self, model-a): fits") {
		t.Fatalf("history = %#v, want tagged route note", detail.History)
	}
}

func TestTickUsesConfiguredRouterPromptAndSchemaFiles(t *testing.T) {
	f := newFireFixture(t)
	systemFile := filepath.Join(t.TempDir(), "system.md")
	schemaFile := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(systemFile, []byte("custom system"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaFile, []byte("custom schema"), 0o600); err != nil {
		t.Fatal(err)
	}
	argv := filepath.Join(t.TempDir(), "argv")
	t.Setenv("ROUTER_ARGV", argv)
	f.config.Router = config.RouterFiles{System: systemFile, Schema: schemaFile}
	f.arm("custom prompt", "agent")

	f.runner().Tick(f.ctx)
	arguments, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("read router argv: %v", err)
	}
	parts := strings.Split(strings.TrimSpace(string(arguments)), "\n")
	if len(parts) != 2 || parts[0] != systemFile || parts[1] != "custom schema" {
		t.Fatalf("router argv = %q, want configured system path and schema contents", arguments)
	}
}

func TestTickBlocksTheTaskWhenRoutingFails(t *testing.T) {
	cases := []struct {
		name   string
		router string
		patch  model.Patch
	}{
		{name: "router exits non-zero", router: "#!/bin/sh\nexit 1\n"},
		{name: "router times out", router: "#!/bin/sh\nsleep 1\n", patch: model.Patch{}},
		{name: "router prints non-json", router: "#!/bin/sh\nprintf 'not json\\n'\n"},
		{name: "router chooses an unlisted root", router: "#!/bin/sh\nprintf '%s\\n' '{\"root\":\"/not-listed\",\"isolation\":\"self\",\"model\":\"model-a\"}'\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFireFixture(t)
			path := filepath.Join(t.TempDir(), "router")
			if err := os.WriteFile(path, []byte(tc.router), 0o700); err != nil {
				t.Fatal(err)
			}
			f.config.Agent.Router = []string{path, "{system}", "{schema}"}
			if tc.name == "router times out" {
				f.config.Agent.Router = []string{path}
				f.timeout = 100 * time.Millisecond
			}
			task := f.arm("bad route", "agent")
			f.runner().Tick(f.ctx)
			run := f.runs()[0]
			detail := f.task(task.Number)
			if run.State != model.RunFailed || detail.Task.Status != model.StatusBlocked || !fireHasRouterFailure(detail.History) {
				t.Fatalf("run = %#v; task = %#v; want failed run, blocked task, and router note", run, detail)
			}
		})
	}
}

func TestTickBlocksTheTaskWhenItsOwnRouteIsInvalid(t *testing.T) {
	f := newFireFixture(t)
	trace := filepath.Join(t.TempDir(), "router-trace")
	t.Setenv("ROUTER_STDIN", trace)
	task := f.arm("bad decided route", "agent")
	badRoot, isolation, selectedModel := "/not-listed", "self", "model-a"
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Root: &badRoot, Isolation: &isolation, Model: &selectedModel}); err != nil {
		t.Fatalf("set bad root: %v", err)
	}
	f.runner().Tick(f.ctx)
	run := f.runs()[0]
	detail := f.task(task.Number)
	if run.State != model.RunFailed || detail.Task.Status != model.StatusBlocked || !fireHasRouterFailure(detail.History) {
		t.Fatalf("run = %#v; task = %#v; want failed route", run, detail)
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("router trace exists (%v), want Resolve failure before router runs", err)
	}
}

func TestTickSkipsRouterWhenTaskAlreadyDecidesEveryRouteField(t *testing.T) {
	f := newFireFixture(t)
	trace := filepath.Join(t.TempDir(), "router-trace")
	t.Setenv("ROUTER_STDIN", trace)
	task := f.arm("already routed", "agent")
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Root: fireString(f.root), Isolation: fireString("self"), Model: fireString("model-a")}); err != nil {
		t.Fatalf("set route: %v", err)
	}
	f.runner().Tick(f.ctx)
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("router trace exists (%v), want no router invocation", err)
	}
	if run := f.runs()[0]; run.State != model.RunRunning || run.Root != f.root || run.Isolation != "self" || run.Model != "model-a" {
		t.Fatalf("run = %#v, want directly spawned decided route", run)
	}
}

func TestTickWaitsOnlyForConflictingInPlaceRoutes(t *testing.T) {
	f := newFireFixture(t)
	f.config.Roots[0].Isolation = "in-place"
	first := f.arm("first", "agent")
	second := f.arm("second", "agent")
	r := f.runner()
	r.Tick(f.ctx)
	runs := f.runs()
	if len(runs) != 2 || runs[0].Task != first.Number || runs[1].Task != second.Number || runs[0].State != model.RunRunning || runs[1].State != model.RunWaiting || runs[1].Workspace != "" {
		t.Fatalf("in-place runs = %#v, want running first and workspace-less waiting second", runs)
	}
	if changed, err := f.store.UpdateRun(f.ctx, runs[0].ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !changed {
		t.Fatalf("end first run = (%t, %v)", changed, err)
	}
	r.Tick(f.ctx)
	if run := f.runs()[1]; run.State != model.RunRunning || run.Workspace == "" {
		t.Fatalf("waiting run after root frees = %#v, want running with workspace", run)
	}
}

func TestTickDoesNotWaitForSelfOrWorktreeRoutes(t *testing.T) {
	for _, isolation := range []string{"self", "worktree"} {
		t.Run(isolation, func(t *testing.T) {
			f := newFireFixture(t)
			f.config.Roots[0].Isolation = isolation
			if isolation == "worktree" {
				fireGitRoot(t, f.root)
			}
			f.arm("first", "agent")
			f.arm("second", "agent")
			f.runner().Tick(f.ctx)
			runs := f.runs()
			if len(runs) != 2 || runs[0].State != model.RunRunning || runs[1].State != model.RunRunning {
				t.Fatalf("%s runs = %#v, want both running", isolation, runs)
			}
		})
	}
}

func TestTickFailsRoutingRunsLeftByADaemonRestart(t *testing.T) {
	f := newFireFixture(t)
	task := f.arm("interrupted route", "agent")
	if _, err := f.store.StartRun(f.ctx, task.Number); err != nil {
		t.Fatalf("start routing run: %v", err)
	}
	f.runner().Tick(f.ctx)
	run := f.runs()[0]
	detail := f.task(task.Number)
	if run.State != model.RunFailed || detail.Task.Status != model.StatusBlocked || !fireHasNote(detail.History, "daemon restarted") {
		t.Fatalf("run = %#v; task = %#v; want failed stale routing run", run, detail)
	}
}

func TestStateUsesTheDocumentedPrecedenceAndPreventsFiring(t *testing.T) {
	cases := []struct {
		name   string
		adjust func(*fireFixture)
		want   string
	}{
		{"off wins", func(f *fireFixture) { f.config.Runner.Enabled = false }, runner.StateOff},
		{"paused beats missing herdr", func(f *fireFixture) { f.config.Runner.Enabled = true; f.herdr = nil; firePauseFile(t, f.paths) }, runner.StatePaused},
		{"no herdr beats no router", func(f *fireFixture) { f.herdr = nil; f.config.Agent.Router = nil; t.Setenv("PATH", t.TempDir()) }, runner.StateNoHerdr},
		{"no router", func(f *fireFixture) { f.config.Agent.Router = []string{"not-an-executable"} }, runner.StateNoRouter},
		{"on", func(f *fireFixture) {}, runner.StateOn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFireFixture(t)
			tc.adjust(f)
			if tc.want == runner.StateNoHerdr {
				if path, err := herdr.Find(); err == nil {
					t.Fatalf("herdr found at %q with empty PATH and the seal", path)
				}
			}
			f.arm("not fired outside on", "agent")
			r := f.runner()
			if got := r.State(); got != tc.want {
				t.Fatalf("State() = %q, want %q", got, tc.want)
			}
			r.Tick(f.ctx)
			if tc.want != runner.StateOn && len(f.runs()) != 0 {
				t.Fatalf("runs = %#v, want no spawn in %s", f.runs(), tc.want)
			}
		})
	}
}

func TestPausePersistsPrivateFileRefusesAgentsAndResumesTicking(t *testing.T) {
	f := newFireFixture(t)
	r := f.runner()
	if err := r.Pause(f.ctx, store.Actor{Session: "agent"}, true); err == nil || !strings.Contains(err.Error(), "not-allowed") {
		t.Fatalf("agent Pause() error = %v, want not-allowed", err)
	}
	if err := r.Pause(f.ctx, store.Actor{}, true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if !r.Paused() || r.State() != runner.StatePaused {
		t.Fatalf("paused = %t; state = %q, want true and paused", r.Paused(), r.State())
	}
	if info, err := os.Stat(f.paths.RunnerPause()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("pause file mode = %v (%v), want 0600", info, err)
	}
	f.arm("wait while paused", "agent")
	r.Tick(f.ctx)
	if len(f.runs()) != 0 {
		t.Fatalf("runs while paused = %#v, want none", f.runs())
	}
	if err := r.Pause(f.ctx, store.Actor{}, false); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if r.Paused() {
		t.Fatal("Paused() = true after resume")
	}
	r.Tick(f.ctx)
	if len(f.runs()) != 1 {
		t.Fatalf("runs after resume = %#v, want one", f.runs())
	}
}

func TestNotifyRunsForEachSpawnAndOnNoRouterStateTransition(t *testing.T) {
	f := newFireFixture(t)
	notice := filepath.Join(t.TempDir(), "notice")
	script := filepath.Join(t.TempDir(), "notify")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s|%s\\n' \"$1\" \"$2\" >> "+fmt.Sprintf("%q", notice)+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.config.Notify.Command = []string{script, "{title}", "{body}"}
	first := f.arm("first task", "agent")
	second := f.arm("second task", "agent")
	r := f.runner()
	r.Tick(f.ctx)
	got, err := os.ReadFile(notice)
	if err != nil {
		t.Fatalf("read spawn notifications: %v", err)
	}
	want := "desk: T" + fmt.Sprint(first.Number) + " started|first task\n" + "desk: T" + fmt.Sprint(second.Number) + " started|second task\n"
	if string(got) != want {
		t.Fatalf("spawn notifications = %q, want %q", got, want)
	}
	if err := os.Remove(f.router); err != nil {
		t.Fatalf("remove router to enter no-router: %v", err)
	}
	r.Tick(f.ctx)
	afterTransition, err := os.ReadFile(notice)
	if err != nil {
		t.Fatalf("read state notification: %v", err)
	}
	if lines := strings.Split(strings.TrimSuffix(string(afterTransition), "\n"), "\n"); len(lines) != 3 {
		t.Fatalf("notifications after no-router transition = %q, want exactly one additional notification", afterTransition)
	}
	r.Tick(f.ctx)
	got, err = os.ReadFile(notice)
	if err != nil {
		t.Fatalf("read state notification: %v", err)
	}
	if string(got) != string(afterTransition) {
		t.Fatalf("notifications after unchanged no-router tick = %q, want %q", got, afterTransition)
	}
}

func TestNotifyFailureDoesNotPreventTheSpawn(t *testing.T) {
	f := newFireFixture(t)
	f.config.Notify.Command = []string{filepath.Join(t.TempDir(), "missing-notify"), "{title}", "{body}"}
	f.arm("still starts", "agent")
	f.runner().Tick(f.ctx)
	if len(f.runs()) != 1 || f.runs()[0].State != model.RunRunning {
		t.Fatalf("runs after notify failure = %#v, want started run", f.runs())
	}
}

func TestOnStateReceivesInitialStateAndOnlyOrderedChanges(t *testing.T) {
	f := newFireFixture(t)
	r := f.runner()
	if got, want := f.states, []string{runner.StateOn}; !reflect.DeepEqual(got, want) {
		t.Fatalf("initial OnState calls = %v, want %v", got, want)
	}
	r.Tick(f.ctx)
	r.Tick(f.ctx)
	if got, want := f.states, []string{runner.StateOn}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unchanged-state calls = %v, want %v", got, want)
	}
	if err := r.Pause(f.ctx, store.Actor{}, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Pause(f.ctx, store.Actor{}, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.router); err != nil {
		t.Fatalf("remove router to enter no-router: %v", err)
	}
	r.Tick(f.ctx)
	if got, want := f.states, []string{runner.StateOn, runner.StatePaused, runner.StateOn, runner.StateNoRouter}; !reflect.DeepEqual(got, want) {
		t.Fatalf("OnState calls = %v, want %v", got, want)
	}
}

func firePauseFile(t *testing.T, paths config.Paths) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(paths.RunnerPause()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RunnerPause(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fireGitRoot(t *testing.T, root string) {
	t.Helper()
	for _, argv := range [][]string{
		{"init", root},
		{"-C", root, "-c", "user.name=desk test", "-c", "user.email=desk@example.test", "commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", argv...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(argv, " "), err, output)
		}
	}
}

func fireHasRouterNote(history []model.Event, want string) bool {
	for _, event := range history {
		if event.Kind != model.KindNote || !reflect.DeepEqual(event.Tags, []string{model.TagRunner, model.TagRouter}) {
			continue
		}
		var note model.NoteData
		if json.Unmarshal(event.Data, &note) == nil && note.Text == want {
			return true
		}
	}
	return false
}

func fireHasRouterFailure(history []model.Event) bool {
	for _, event := range history {
		if event.Kind != model.KindNote || !reflect.DeepEqual(event.Tags, []string{model.TagRunner, model.TagRouter}) {
			continue
		}
		var note model.NoteData
		if json.Unmarshal(event.Data, &note) == nil && strings.HasPrefix(note.Text, "router: ") {
			return true
		}
	}
	return false
}

func fireHasNote(history []model.Event, fragment string) bool {
	for _, event := range history {
		var note model.NoteData
		if event.Kind == model.KindNote && json.Unmarshal(event.Data, &note) == nil && strings.Contains(note.Text, fragment) {
			return true
		}
	}
	return false
}
