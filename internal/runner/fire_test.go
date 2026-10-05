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

	"github.com/federbenjamin/herdr-desk"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func fireString(s string) *string { return &s }

func TestTickStartsArmedTasksInArmingOrderUpToTheCap(t *testing.T) {
	f := newFixture(t, "", "self")
	f.config.Runner.Cap = 3
	first := f.armThread("first", "agent")
	second := f.armThread("second", "agent")
	third := f.armThread("third", "agent")
	fourth := f.armThread("fourth", "agent")

	f.runner().Tick(f.ctx)
	runs := f.runs()
	if len(runs) != 3 {
		t.Fatalf("started runs = %#v, want three", runs)
	}
	if got, want := []int{runs[0].Task, runs[1].Task, runs[2].Task}, []int{first.Number, second.Number, third.Number}; !reflect.DeepEqual(got, want) {
		t.Fatalf("started task order = %v, want %v", got, want)
	}
	if got := f.herdr.Workspaces(); len(got) != 3 || got[0].Cwd != f.root || got[0].Command == "" {
		t.Fatalf("workspaces = %#v, want one started workspace per run rooted at %q; runs = %#v; log:\n%s", got, f.root, runs, f.logged())
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
	f := newFixture(t, "", "self")
	if _, err := f.store.AddTask(f.ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "open", Thread: "agent"}}); err != nil {
		t.Fatal(err)
	}
	f.armThread("other thread", "user")
	archived := f.armThread("archived", "agent")
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
	f := newFixture(t, "", "self")
	f.config.Runner.MaxRunsPerDay = 1
	yesterday := f.armThread("yesterday", "agent")
	f.runner().Tick(f.ctx)
	runs := f.runs()
	if len(runs) != 1 {
		t.Fatalf("initial runs = %#v, want one", runs)
	}
	if changed, err := f.store.UpdateRun(f.ctx, runs[0].ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !changed {
		t.Fatalf("end yesterday run = (%t, %v)", changed, err)
	}
	f.now = f.now.AddDate(0, 0, 1)
	today := f.armThread("today", "agent")
	f.runner().Tick(f.ctx)
	runs = f.runs()
	if len(runs) != 2 || runs[1].Task != today.Number || runs[0].Task != yesterday.Number {
		t.Fatalf("runs across midnight = %#v, want yesterday then today", runs)
	}
}

func TestTickPassesPromptSchemaInputAndHooksToTheRouter(t *testing.T) {
	f := newFixture(t, "", "self")
	stdin := filepath.Join(t.TempDir(), "stdin")
	argv := filepath.Join(t.TempDir(), "argv")
	hooks := filepath.Join(t.TempDir(), "hooks")
	t.Setenv("ROUTER_STDIN", stdin)
	t.Setenv("ROUTER_ARGV", argv)
	t.Setenv("ROUTER_HOOKS", hooks)
	task := f.armThread("route me", "agent")

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
	if string(system) != herdrdesk.RouterSystem() {
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
	f := newFixture(t, "", "self")
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
	f.armThread("custom prompt", "agent")

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
			f := newFixture(t, "", "self")
			path := filepath.Join(t.TempDir(), "router")
			if err := os.WriteFile(path, []byte(tc.router), 0o700); err != nil {
				t.Fatal(err)
			}
			f.config.Agent.Router = []string{path, "{system}", "{schema}"}
			if tc.name == "router times out" {
				f.config.Agent.Router = []string{path}
				f.timeout = 100 * time.Millisecond
			}
			task := f.armThread("bad route", "agent")
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
	f := newFixture(t, "", "self")
	trace := filepath.Join(t.TempDir(), "router-trace")
	t.Setenv("ROUTER_STDIN", trace)
	task := f.armThread("bad decided route", "agent")
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
	f := newFixture(t, "", "self")
	trace := filepath.Join(t.TempDir(), "router-trace")
	t.Setenv("ROUTER_STDIN", trace)
	task := f.armThread("already routed", "agent")
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
	f := newFixture(t, "", "self")
	f.config.Roots[0].Isolation = "in-place"
	first := f.armThread("first", "agent")
	second := f.armThread("second", "agent")
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
			f := newFixture(t, "", "self")
			f.config.Roots[0].Isolation = isolation
			if isolation == "worktree" {
				fireGitRoot(t, f.root)
			}
			f.armThread("first", "agent")
			f.armThread("second", "agent")
			f.runner().Tick(f.ctx)
			runs := f.runs()
			if len(runs) != 2 || runs[0].State != model.RunRunning || runs[1].State != model.RunRunning {
				t.Fatalf("%s runs = %#v, want both running", isolation, runs)
			}
		})
	}
}

func TestTickFailsRoutingRunsLeftByADaemonRestart(t *testing.T) {
	f := newFixture(t, "", "self")
	task := f.armThread("interrupted route", "agent")
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
		adjust func(*fixture)
		want   string
	}{
		{"off wins", func(f *fixture) { f.config.Runner.Enabled = false }, runner.StateOff},
		{"paused beats missing herdr", func(f *fixture) { f.config.Runner.Enabled = true; f.herdr = nil; firePauseFile(t, f.paths) }, runner.StatePaused},
		{"no herdr beats no router", func(f *fixture) { f.herdr = nil; f.config.Agent.Router = nil }, runner.StateNoHerdr},
		{"no router", func(f *fixture) { f.config.Agent.Router = []string{"not-an-executable"} }, runner.StateNoRouter},
		{"on", func(f *fixture) {}, runner.StateOn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "", "self")
			tc.adjust(f)
			if tc.want == runner.StateNoHerdr {
				if path, err := herdr.Find(); err == nil {
					t.Fatalf("herdr found at %q under the seal", path)
				}
			}
			f.armThread("not fired outside on", "agent")
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
	f := newFixture(t, "", "self")
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
	f.armThread("wait while paused", "agent")
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
	f := newFixture(t, "", "self")
	notice := filepath.Join(t.TempDir(), "notice")
	script := filepath.Join(t.TempDir(), "notify")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s|%s\\n' \"$1\" \"$2\" >> "+fmt.Sprintf("%q", notice)+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.config.Notify.Command = []string{script, "{title}", "{body}"}
	first := f.armThread("first task", "agent")
	second := f.armThread("second task", "agent")
	r := f.runner()
	r.Tick(f.ctx)
	got, err := os.ReadFile(notice)
	if err != nil {
		t.Fatalf("read spawn notifications: %v", err)
	}
	want := "herdr-desk: T" + fmt.Sprint(first.Number) + " started|first task\n" + "herdr-desk: T" + fmt.Sprint(second.Number) + " started|second task\n"
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
	f := newFixture(t, "", "self")
	f.config.Notify.Command = []string{filepath.Join(t.TempDir(), "missing-notify"), "{title}", "{body}"}
	f.armThread("still starts", "agent")
	f.runner().Tick(f.ctx)
	if len(f.runs()) != 1 || f.runs()[0].State != model.RunRunning {
		t.Fatalf("runs after notify failure = %#v, want started run", f.runs())
	}
}

func TestOnStateReceivesInitialStateAndOnlyOrderedChanges(t *testing.T) {
	f := newFixture(t, "", "self")
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

func TestPauseFileThatCannotBeReadKeepsTheRunnerPaused(t *testing.T) {
	f := newFixture(t, "", "self")
	firePauseFile(t, f.paths)
	dir := filepath.Dir(f.paths.RunnerPause())
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := os.Stat(f.paths.RunnerPause()); err == nil || os.IsNotExist(err) {
		t.Skipf("stat of a file in a mode 0 folder = %v, want a permission error (running as root?)", err)
	}
	f.armThread("not while the pause is unreadable", "agent")
	r := f.runner()
	if !r.Paused() || r.State() != runner.StatePaused {
		t.Fatalf("paused = %t; state = %q, want paused when the pause file cannot be read", r.Paused(), r.State())
	}
	r.Tick(f.ctx)
	if got := f.runs(); len(got) != 0 {
		t.Fatalf("runs = %#v, want none while the pause file cannot be read", got)
	}
	if got := f.logged(); strings.Count(got, "pause file cannot be read") != 1 {
		t.Fatalf("log = %q, want the unreadable pause file logged once", got)
	}
}

func TestNoHerdrLogsWhyDeskHerdrCannotBeUsed(t *testing.T) {
	f := newFixture(t, "", "self")
	t.Setenv("DESK_HERDR", "herdr")
	r := f.runnerWith(nil)
	r.Tick(f.ctx)
	if got := r.State(); got != runner.StateNoHerdr {
		t.Fatalf("State() = %q, want no-herdr", got)
	}
	if got := f.logged(); strings.Count(got, `DESK_HERDR "herdr" is not an absolute path`) != 1 {
		t.Fatalf("log = %q, want the DESK_HERDR reason logged once", got)
	}
}

func TestTickBlocksTheTaskWhenItsRouteCannotBeSaved(t *testing.T) {
	f := newFixture(t, "", "self")
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.store, err = store.Open(f.paths.DB(), store.Options{
		Now: func() time.Time { return f.now },
		Scanner: func(_ context.Context, text string) (string, error) {
			if strings.Contains(text, "model-a") {
				return "test-pattern", nil
			}
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = f.store.Close() })
	task := f.armThread("route refused by the scanner", "agent")
	f.runner().Tick(f.ctx)
	run := f.run(task.Number)
	detail := f.task(task.Number)
	if run.State != model.RunFailed || detail.Task.Status != model.StatusBlocked || len(f.herdr.Workspaces()) != 0 || !fireHasRouterFailure(detail.History) {
		t.Fatalf("run = %#v; task = %#v; workspaces = %#v, want a failed run, a blocked task with a router note, and no spawn", run, detail, f.herdr.Workspaces())
	}
}

func TestTickMatchesARootAndAProjectWrittenThroughASymlink(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, link, "self")
	stdin := filepath.Join(t.TempDir(), "stdin")
	t.Setenv("ROUTER_STDIN", stdin)
	project := filepath.Join(real, "sub")
	routed, err := f.store.AddTask(f.ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "under the link", Thread: "agent", Project: project}})
	if err != nil {
		t.Fatal(err)
	}
	ready := model.StatusReady
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, routed.Number, model.Patch{Status: &ready}); err != nil {
		t.Fatal(err)
	}
	f.runner().Tick(f.ctx)
	input, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatalf("read router stdin: %v", err)
	}
	var got struct {
		Task struct {
			Project string `json:"project"`
		} `json:"task"`
	}
	if err := json.Unmarshal(input, &got); err != nil || got.Task.Project != filepath.Join(link, "sub") {
		t.Fatalf("router project = %q (%v), want %q: the root's own path", got.Task.Project, err, filepath.Join(link, "sub"))
	}

	byReal := f.armRoute("root written by its real path", real, "self")
	f.runner().Tick(f.ctx)
	if run := f.run(byReal.Number); run.State != model.RunRunning || run.Root != link {
		t.Fatalf("run = %#v, want it running on the configured root %q", run, link)
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
