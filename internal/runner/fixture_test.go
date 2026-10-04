package runner_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/herdr/herdrtest"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/runner"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
)

// fixture is the one runner test setup: a store whose clock is now, a runner config, the in-memory herdr, and a
// router script that answers the root.
type fixture struct {
	t       *testing.T
	ctx     context.Context
	paths   config.Paths
	store   *store.Store
	config  config.Config
	herdr   *herdrtest.Herdr // nil → the runner gets no Herdr option and looks for herdr itself
	now     time.Time        // the clock of the store and the runner
	exe     string
	root    string
	router  string
	timeout time.Duration

	mu     sync.Mutex
	states []string
	logs   []string
}

// newFixture returns a fixture whose config is enabled with cap 3, 20 runs a day, a 10 minute limit, the model
// model-a, and one root at root (a temp folder when "") with isolation. Its router answers that root.
func newFixture(t *testing.T, root, isolation string) *fixture {
	t.Helper()
	if root == "" {
		root = t.TempDir()
	}
	m := testutil.NewMachine(t)
	f := &fixture{
		t:     t,
		ctx:   context.Background(),
		paths: m.Paths,
		now:   time.Date(2026, time.October, 4, 15, 0, 0, 0, time.Local),
		herdr: herdrtest.NewHerdr(),
		exe:   "/opt/desk/bin/desk",
		root:  root,
		// Long enough for a loaded machine; a test of the router's timeout sets its own.
		timeout: 10 * time.Second,
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
	f.config.Runner.MaxRunMinutes = 10
	f.config.Roots = []config.Root{{Path: root, About: "test root", Isolation: isolation}}
	f.config.Agent.Models = []string{"model-a"}
	f.router = f.writeRouter(`{"root":%q,"isolation":"` + isolation + `","model":"model-a","reason":"fits"}`)
	f.config.Agent.Router = []string{f.router, "{system}", "{schema}"}
	return f
}

// writeRouter writes a router script that prints format with the root and records its stdin, argv, and DESK_HOOKS
// in the files ROUTER_STDIN, ROUTER_ARGV, and ROUTER_HOOKS name.
func (f *fixture) writeRouter(format string) string {
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

// runner returns a runner over the fixture's herdr.
func (f *fixture) runner() *runner.Runner {
	var h runner.Herdr
	if f.herdr != nil {
		h = f.herdr
	}
	return f.runnerWith(h)
}

// runnerWith returns a runner over h; nil leaves the runner to find herdr itself.
func (f *fixture) runnerWith(h runner.Herdr) *runner.Runner {
	f.t.Helper()
	return runner.New(runner.Options{
		Store:         f.store,
		Config:        f.config,
		Paths:         f.paths,
		Herdr:         h,
		Exe:           f.exe,
		Now:           func() time.Time { return f.now },
		RouterTimeout: f.timeout,
		KillGrace:     10 * time.Millisecond,
		OnState: func(state string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.states = append(f.states, state)
		},
		Logf: func(format string, args ...any) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.logs = append(f.logs, fmt.Sprintf(format, args...))
		},
	})
}

// logged returns every line the runner logged.
func (f *fixture) logged() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.logs, "\n")
}

// armThread adds a task on thread and sets it ready as a person.
func (f *fixture) armThread(title, thread string) model.Task {
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

// armRoute adds a task on thread agent with its root, isolation, and model set, so no router is called, and sets
// it ready as a person.
func (f *fixture) armRoute(title, root, isolation string) model.Task {
	f.t.Helper()
	task, err := f.store.AddTask(f.ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: title, Thread: "agent"}})
	if err != nil {
		f.t.Fatalf("add task: %v", err)
	}
	ready, modelName := model.StatusReady, "model-a"
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{
		Status: &ready, Root: &root, Isolation: &isolation, Model: &modelName,
	}); err != nil {
		f.t.Fatalf("arm T%d: %v", task.Number, err)
	}
	return task
}

// start arms a routed task on the fixture's root, ticks once, and returns the task, its running run, and the runner.
func (f *fixture) start() (model.Task, model.Run, *runner.Runner) {
	f.t.Helper()
	task := f.armRoute("watch me", f.config.Roots[0].Path, f.config.Roots[0].Isolation)
	r := f.runner()
	r.Tick(f.ctx)
	run := f.run(task.Number)
	if run.State != model.RunRunning || run.Session == "" || run.Pane == "" {
		f.t.Fatalf("started run = %#v, want running run with session and pane", run)
	}
	return task, run, r
}

func (f *fixture) runs() []model.Run {
	f.t.Helper()
	runs, err := f.store.ListRuns(f.ctx)
	if err != nil {
		f.t.Fatalf("list runs: %v", err)
	}
	return runs
}

// run returns the task's first run.
func (f *fixture) run(task int) model.Run {
	f.t.Helper()
	runs := f.runs()
	for _, run := range runs {
		if run.Task == task {
			return run
		}
	}
	f.t.Fatalf("no run for T%d in %#v", task, runs)
	return model.Run{}
}

func (f *fixture) task(number int) store.TaskDetail {
	f.t.Helper()
	detail, err := f.store.GetTask(f.ctx, number)
	if err != nil {
		f.t.Fatalf("get T%d: %v", number, err)
	}
	return detail
}
