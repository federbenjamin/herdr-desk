package runner_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/herdr"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/runner"
	"github.com/federbenjamin/desk/internal/store"
)

func TestKillStopsReportedPaneProcessesClosesThePaneAndBlocksTheTask(t *testing.T) {
	f := newWatchFixture(t)
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
	watchAssertRunnerNote(t, f.task(task.Number).History, run.ID, "")
	watchAssertRunnerStatus(t, f.task(task.Number).History, run.ID, model.StatusBlocked)
}

func TestKillEscalatesToKILLWhenAChildIgnoresTERM(t *testing.T) {
	f := newWatchFixture(t)
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
	f := newWatchFixture(t)
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
	f := newWatchFixture(t)
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

func TestKillStopsRoutingAndWaitingRunsWithoutCallingHerdr(t *testing.T) {
	for _, state := range []string{model.RunRouting, model.RunWaiting} {
		t.Run(state, func(t *testing.T) {
			f := newWatchFixture(t)
			task, run, r := f.start()
			if changed, err := f.store.UpdateRun(f.ctx, run.ID, model.RunRunning, store.RunUpdate{State: state}); err != nil || !changed {
				t.Fatalf("make %s run = (%t, %v)", state, changed, err)
			}
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
	f := newWatchFixture(t)
	task, run, r := f.start()
	f.herdr.Fail("Processes", errors.New("process list failed"))

	got, err := r.Kill(f.ctx, store.Actor{}, task.Number)
	if err != nil {
		t.Fatalf("Kill after Processes failure: %v", err)
	}
	if got.Status != model.StatusBlocked || f.run(task.Number).State != model.RunKilled || !reflect.DeepEqual(f.herdr.Closed(), []string{run.Pane}) {
		t.Fatalf("returned task = %#v; run = %#v; closed = %#v, want blocked killed task and closed pane", got, f.run(task.Number), f.herdr.Closed())
	}
	watchAssertRunnerNote(t, f.task(task.Number).History, run.ID, "")
	watchAssertRunnerStatus(t, f.task(task.Number).History, run.ID, model.StatusBlocked)
}

func TestKillReturnsAnErrorAndWritesNothingWhenHerdrCannotBeAsked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		runner func(*testing.T, *watchFixture) *runner.Runner
	}{
		{
			name: "Panes fails",
			runner: func(_ *testing.T, f *watchFixture) *runner.Runner {
				f.herdr.Fail("Panes", errors.New("pane list failed"))
				return f.runner()
			},
		},
		{
			name: "herdr is absent from PATH",
			runner: func(t *testing.T, f *watchFixture) *runner.Runner {
				t.Setenv("PATH", t.TempDir())
				return f.runnerWithHerdr(nil)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWatchFixture(t)
			task, run, _ := f.start()
			before := f.task(task.Number)
			r := tc.runner(t, f)

			if _, err := r.Kill(f.ctx, store.Actor{}, task.Number); err == nil {
				t.Fatal("Kill without reachable herdr error = nil, want error")
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
