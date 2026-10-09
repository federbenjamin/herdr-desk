package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/herdr/herdrtest"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestKillStopsReportedPaneProcessesClosesThePaneAndBlocksTheTask(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	cmd, waited := killStartProcess(t, "sleep", "60")
	extra, extraWaited := killStartProcess(t, "sleep", "60")
	f.herdr.SetProcesses(run.Pane, herdr.Processes{Group: cmd.Process.Pid, PIDs: []int{cmd.Process.Pid, extra.Process.Pid}})

	got, err := r.Kill(f.ctx, store.Actor{}, task.Number)
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	killWaitForSignal(t, waited, syscall.SIGTERM)
	killWaitForSignal(t, extraWaited, syscall.SIGTERM)
	if got.Status != model.StatusBlocked || f.task(task.Number).Task.Status != model.StatusBlocked || f.run(task.Number).State != model.RunKilled || !reflect.DeepEqual(f.herdr.Closed(), []string{run.Pane}) {
		t.Fatalf("returned task = %#v; stored task = %#v; run = %#v; closed = %#v, want blocked task, killed run, and closed pane", got, f.task(task.Number).Task, f.run(task.Number), f.herdr.Closed())
	}
	killAssertRunnerNote(t, f.task(task.Number).History, run.ID, "")
	killAssertRunnerStatus(t, f.task(task.Number).History, run.ID, model.StatusBlocked)
}

func TestKillEscalatesToKILLWhenAChildIgnoresTERM(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	ready := filepath.Join(t.TempDir(), "term-ignored")
	cmd, waited := killStartProcess(t, "sh", "-c", `trap "" TERM; : > "$1"; while :; do sleep 60; done`, "--", ready)
	killWaitForFile(t, ready)
	f.herdr.SetProcesses(run.Pane, herdr.Processes{Group: cmd.Process.Pid, PIDs: []int{cmd.Process.Pid}})

	if _, err := r.Kill(f.ctx, store.Actor{}, task.Number); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	killWaitForSignal(t, waited, syscall.SIGKILL)
	if got := f.run(task.Number).State; got != model.RunKilled {
		t.Fatalf("run after TERM-resistant process = %q, want killed", got)
	}
}

func TestKillNeverSignalsTheRunnerOrProtectedPIDs(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	f.herdr.SetProcesses(run.Pane, herdr.Processes{Group: syscall.Getpgrp(), PIDs: []int{0, 1, os.Getpid()}})

	if _, err := r.Kill(f.ctx, store.Actor{}, task.Number); err != nil {
		t.Fatalf("Kill protected processes: %v", err)
	}
	if os.Getpid() == 0 || f.run(task.Number).State != model.RunKilled || f.task(task.Number).Task.Status != model.StatusBlocked {
		t.Fatalf("runner survived = %t; run = %#v; task = %#v, want runner alive, killed run, and blocked task", os.Getpid() != 0, f.run(task.Number), f.task(task.Number).Task)
	}
}

func TestKillRefusesAgentActorsAndTasksWithoutLiveRuns(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	if _, err := r.Kill(f.ctx, store.Actor{Session: run.Session}, task.Number); err == nil || !strings.Contains(err.Error(), model.CodeNotAllowed) {
		t.Fatalf("agent Kill error = %v, want not-allowed", err)
	}
	if f.run(task.Number).State != model.RunRunning || f.task(task.Number).Task.Status != model.StatusStarted || len(f.herdr.Closed()) != 0 {
		t.Fatalf("agent Kill changed run = %#v, task = %#v, or closed panes = %#v", f.run(task.Number), f.task(task.Number).Task, f.herdr.Closed())
	}

	noRun, err := f.store.AddTask(f.ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "no run", Thread: "agent"}})
	if err != nil {
		t.Fatalf("add no-run task: %v", err)
	}
	if _, err := r.Kill(f.ctx, store.Actor{}, noRun.Number); err == nil || !strings.Contains(err.Error(), model.CodeNoRun) {
		t.Fatalf("Kill without live run error = %v, want no-run", err)
	}
}

func TestKillStopsStartingAndWaitingRunsWithoutCallingHerdr(t *testing.T) {
	for _, tc := range []struct {
		state string
		cap   int
	}{{model.RunStarting, 3}, {model.RunWaiting, 0}} {
		state := tc.state
		t.Run(state, func(t *testing.T) {
			f := newFixture(t, "", "in-place")
			task := f.armRoute("kill me", f.root, "in-place")
			r := f.runner()
			run, err := f.store.StartRun(f.ctx, store.Actor{}, task.Number, store.RunRoute{Root: f.root, Isolation: "in-place", Model: "model-a"}, store.RunCaps{Slots: tc.cap, PerDay: 1000})
			if err != nil || run.State != state {
				t.Fatalf("start %s run = %#v, %v", state, run, err)
			}
			f.herdr.Fail("Panes", errors.New("must not be called"))
			f.herdr.Fail("Processes", errors.New("must not be called"))
			if _, err := r.Kill(f.ctx, store.Actor{}, task.Number); err != nil {
				t.Fatalf("Kill %s run: %v", state, err)
			}
			if got := f.run(task.Number).State; got != model.RunKilled {
				t.Fatalf("%s run after Kill = %q, want killed", state, got)
			}
			if f.task(task.Number).Task.Status != model.StatusBlocked || len(f.herdr.Closed()) != 0 {
				t.Fatalf("task = %#v; closed panes = %#v, want blocked task and no herdr call", f.task(task.Number).Task, f.herdr.Closed())
			}
		})
	}
}

func TestKillClosesAndBlocksWhenReadingPaneProcessesFails(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	f.herdr.Fail("Processes", errors.New("process list failed"))

	got, err := r.Kill(f.ctx, store.Actor{}, task.Number)
	if want := fmt.Sprintf("T%d is blocked, but its processes could not be read", task.Number); err == nil || err.Error() != want {
		t.Fatalf("Kill after Processes failure: error = %v, want %q: nothing was signalled, so the kill is not verified", err, want)
	}
	if got.Status != model.StatusBlocked || f.run(task.Number).State != model.RunKilled || !reflect.DeepEqual(f.herdr.Closed(), []string{run.Pane}) {
		t.Fatalf("returned task = %#v; run = %#v; closed = %#v, want blocked killed task and closed pane", got, f.run(task.Number), f.herdr.Closed())
	}
	killAssertRunnerNote(t, f.task(task.Number).History, run.ID, fmt.Sprintf("run %d killed: pane %s closed; its processes could not be read", run.ID, run.Pane))
	killAssertRunnerStatus(t, f.task(task.Number).History, run.ID, model.StatusBlocked)
}

func TestKillRecordsAPaneThatDidNotCloseAndJobsClosesItAgain(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	f.herdr.Fail("ClosePane", errors.New("close denied"))

	got, err := r.Kill(f.ctx, store.Actor{}, task.Number)
	if err == nil || !strings.Contains(err.Error(), "pane "+run.Pane+" did not close") {
		t.Fatalf("Kill with a pane that does not close: error = %v, want one naming the open pane", err)
	}
	if got.Status != model.StatusBlocked || f.run(task.Number).State != model.RunKilled || !f.run(task.Number).LeftOpen {
		t.Fatalf("returned task = %#v; run = %#v, want the task blocked and the run killed with its pane left open", got, f.run(task.Number))
	}
	killAssertRunnerNote(t, f.task(task.Number).History, run.ID, fmt.Sprintf("run %d killed: pane %s did not close", run.ID, run.Pane))

	r.Jobs(f.ctx)
	if got := f.herdr.Closed(); len(got) != 0 {
		t.Fatalf("closed panes while close still fails = %#v, want none", got)
	}
	f.herdr.Fail("ClosePane", nil)
	r.Jobs(f.ctx)
	r.Jobs(f.ctx)
	if got := f.herdr.Closed(); !reflect.DeepEqual(got, []string{run.Pane}) {
		t.Fatalf("closed panes once close works = %#v, want the left pane closed once", got)
	}
}

func TestKillCountsAPaneThatClosedWithItsProcessesAsClosed(t *testing.T) {
	f := newFixture(t, "", "in-place")
	h := &hookedHerdr{Herdr: f.herdr}
	task, run, _ := f.start()
	r := f.runnerWith(h)
	// The killed worker was the pane's last process, so herdr dropped the pane before the close reached it.
	h.before("ClosePane", func() { f.herdr.Remove(run.Pane) })

	if _, err := r.Kill(f.ctx, store.Actor{}, task.Number); err != nil {
		t.Fatalf("Kill of a pane that went away with its processes: %v", err)
	}
	killAssertRunnerNote(t, f.task(task.Number).History, run.ID, fmt.Sprintf("run %d killed: pane %s closed", run.ID, run.Pane))
}

func TestKillReportsAProcessThatSurvivesTheKill(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	// An exited child that is not reaped yet answers kill(pid, 0) like a live one, whatever it is sent.
	zombie := exec.Command("true")
	if err := zombie.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = zombie.Wait() })
	f.herdr.SetProcesses(run.Pane, herdr.Processes{PIDs: []int{zombie.Process.Pid}})

	_, err := r.Kill(f.ctx, store.Actor{}, task.Number)
	want := fmt.Sprintf("pid %d survived the kill", zombie.Process.Pid)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Kill error = %v, want one holding %q", err, want)
	}
	killAssertRunnerNote(t, f.task(task.Number).History, run.ID, fmt.Sprintf("run %d killed: pane %s closed; %s", run.ID, run.Pane, want))
}

func TestKillClaimsTheRunBeforeItTouchesThePane(t *testing.T) {
	f := newFixture(t, "", "in-place")
	h := &hookedHerdr{Herdr: f.herdr}
	task, run, _ := f.start()
	r := f.runnerWith(h)
	cmd, _ := killStartProcess(t, "sleep", "60")
	f.herdr.SetProcesses(run.Pane, herdr.Processes{Group: cmd.Process.Pid, PIDs: []int{cmd.Process.Pid}})
	review := model.StatusReview
	h.before("Panes", func() {
		if _, err := f.store.SetTask(f.ctx, store.Actor{Session: run.Session, Run: run.ID}, task.Number, model.Patch{Status: &review}); err != nil {
			t.Errorf("hand back during the kill: %v", err)
		}
	})

	if _, err := r.Kill(f.ctx, store.Actor{}, task.Number); err == nil || !strings.Contains(err.Error(), model.CodeNoRun) {
		t.Fatalf("Kill that lost the run: error = %v, want no-run", err)
	}
	if got := f.herdr.Closed(); len(got) != 0 {
		t.Fatalf("closed panes = %#v, want none: the kill lost the run to the hand-back", got)
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("the pane's process after a lost kill: %v, want it alive", err)
	}
	if got := f.task(task.Number).Task.Status; got != model.StatusReview {
		t.Fatalf("task = %q, want the worker's review", got)
	}
}

func TestKillFinishesItsWritesWhenItsContextEndsAfterTheClaim(t *testing.T) {
	f := newFixture(t, "", "in-place")
	h := &hookedHerdr{Herdr: f.herdr}
	task, run, _ := f.start()
	r := f.runnerWith(h)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	h.before("ClosePane", cancel)

	if _, err := r.Kill(ctx, store.Actor{}, task.Number); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got := f.task(task.Number).Task.Status; got != model.StatusBlocked || f.run(task.Number).State != model.RunKilled {
		t.Fatalf("task = %q; run = %#v, want the task blocked once the run is killed", got, f.run(task.Number))
	}
	killAssertRunnerStatus(t, f.task(task.Number).History, run.ID, model.StatusBlocked)
}

// hookedHerdr is the in-memory herdr with one-shot hooks before or after a method's call.
type hookedHerdr struct {
	*herdrtest.Herdr
	mu    sync.Mutex
	hooks map[string]func()
}

// before runs fn once, before the next call of method.
func (h *hookedHerdr) before(method string, fn func()) { h.set("before "+method, fn) }

// after runs fn once, after the next call of method.
func (h *hookedHerdr) after(method string, fn func()) { h.set("after "+method, fn) }

func (h *hookedHerdr) set(key string, fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hooks == nil {
		h.hooks = map[string]func(){}
	}
	h.hooks[key] = fn
}

func (h *hookedHerdr) fire(key string) {
	h.mu.Lock()
	fn := h.hooks[key]
	delete(h.hooks, key)
	h.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (h *hookedHerdr) CreateWorkspace(ctx context.Context, cwd, label string, env []string) (herdr.Created, error) {
	h.fire("before CreateWorkspace")
	defer h.fire("after CreateWorkspace")
	return h.Herdr.CreateWorkspace(ctx, cwd, label, env)
}

func (h *hookedHerdr) Run(ctx context.Context, pane, command string) error {
	h.fire("before Run")
	defer h.fire("after Run")
	return h.Herdr.Run(ctx, pane, command)
}

func (h *hookedHerdr) Panes(ctx context.Context) ([]herdr.Pane, error) {
	h.fire("before Panes")
	defer h.fire("after Panes")
	return h.Herdr.Panes(ctx)
}

func (h *hookedHerdr) Processes(ctx context.Context, pane string) (herdr.Processes, error) {
	h.fire("before Processes")
	defer h.fire("after Processes")
	return h.Herdr.Processes(ctx, pane)
}

func (h *hookedHerdr) ClosePane(ctx context.Context, pane string) error {
	h.fire("before ClosePane")
	defer h.fire("after ClosePane")
	return h.Herdr.ClosePane(ctx, pane)
}

func TestKillReturnsAnErrorAndWritesNothingWhenHerdrCannotBeAsked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		want   string
		runner func(*testing.T, *fixture) *runner.Runner
	}{
		{
			name: "Panes fails",
			want: "pane list failed",
			runner: func(_ *testing.T, f *fixture) *runner.Runner {
				f.herdr.Fail("Panes", errors.New("pane list failed"))
				return f.runner()
			},
		},
		{
			name: "DESK_HERDR names no usable herdr",
			want: `DESK_HERDR "herdr" is not an absolute path`,
			runner: func(t *testing.T, f *fixture) *runner.Runner {
				t.Setenv("DESK_HERDR", "herdr")
				return f.runnerWith(nil)
			},
		},
		{
			name: "DESK_HERDR names no usable herdr while the runner is paused",
			want: `DESK_HERDR "herdr" is not an absolute path`,
			runner: func(t *testing.T, f *fixture) *runner.Runner {
				t.Setenv("DESK_HERDR", "herdr")
				r := f.runnerWith(nil)
				if err := r.Pause(f.ctx, store.Actor{}, true); err != nil {
					t.Fatalf("pause: %v", err)
				}
				return r
			},
		},
		{
			name: "DESK_HERDR names no usable herdr while the runner is off",
			want: `DESK_HERDR "herdr" is not an absolute path`,
			runner: func(t *testing.T, f *fixture) *runner.Runner {
				t.Setenv("DESK_HERDR", "herdr")
				f.config.Runner.Enabled = false
				return f.runnerWith(nil)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "", "in-place")
			task, run, _ := f.start()
			before := f.task(task.Number)
			r := tc.runner(t, f)

			if _, err := r.Kill(f.ctx, store.Actor{}, task.Number); err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "%!") {
				t.Fatalf("Kill without reachable herdr error = %v, want an error holding %q", err, tc.want)
			}
			if got := f.run(task.Number); !reflect.DeepEqual(got, run) {
				t.Fatalf("run after unreachable herdr = %#v, want unchanged %#v", got, run)
			}
			if got := f.task(task.Number); !reflect.DeepEqual(got, before) {
				t.Fatalf("task after unreachable herdr = %#v, want unchanged %#v", got, before)
			}
			if got := f.herdr.Closed(); len(got) != 0 {
				t.Fatalf("closed panes after unreachable herdr = %#v, want none", got)
			}
		})
	}
}

func killStartProcess(t *testing.T, name string, args ...string) (*exec.Cmd, <-chan error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-waited:
		default:
		}
	})
	return cmd, waited
}

func killWaitForFile(t *testing.T, path string) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("TERM-resistant child did not write %s", path)
		}
	}
}

func killWaitForSignal(t *testing.T, waited <-chan error, want syscall.Signal) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	select {
	case err := <-waited:
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("child exit = %v, want signal %s", err, want)
		}
		status, ok := exit.Sys().(syscall.WaitStatus)
		if !ok || status.Signal() != want {
			t.Fatalf("child exit = %v, want signal %s", err, want)
		}
	case <-deadline.C:
		t.Fatalf("child process did not exit after %s", want)
	}
}

func killAssertRunnerNote(t *testing.T, history []model.Event, run int64, want string) {
	t.Helper()
	for _, event := range history {
		var note model.NoteData
		if event.Kind == model.KindNote && event.Run == run && event.Session == "" && reflect.DeepEqual(event.Tags, []string{model.TagRunner}) && json.Unmarshal(event.Data, &note) == nil && (want == "" || note.Text == want) {
			return
		}
	}
	if want == "" {
		t.Fatalf("history = %#v, want a runner note carrying run %d and no session", history, run)
	}
	t.Fatalf("history = %#v, want runner note %q carrying run %d and no session", history, want, run)
}

func killAssertRunnerStatus(t *testing.T, history []model.Event, run int64, want model.Status) {
	t.Helper()
	for _, event := range history {
		var patch model.Patch
		if event.Kind == model.KindSet && event.Run == run && event.Session == "" && json.Unmarshal(event.Data, &patch) == nil && patch.Status != nil && *patch.Status == want {
			return
		}
	}
	t.Fatalf("history = %#v, want %s status write carrying run %d and no session", history, want, run)
}
