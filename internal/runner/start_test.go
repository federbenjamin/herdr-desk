package runner_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// stBreak makes every refusal condition true at once except the ones a case lifts: the runner is off, paused,
// has no herdr, and the day's cap is spent, and the caller is a stranger agent session.
func stBreak(f *fixture, task int) {
	f.t.Helper()
	f.config.Runner.MaxRunsPerDay = 1
	if _, err := f.store.StartRun(f.ctx, task, store.RunRoute{Root: f.root, Isolation: "self"}, store.RunCaps{Slots: 3, PerDay: 1000}); err != nil {
		f.t.Fatalf("spend the day's cap: %v", err)
	}
}

// The refusal order is part of the contract: a caller told runner-paused must not first have been told
// cap-reached, and an agent must learn nothing about the runner's state.
func TestStartRefusesInTheDocumentedOrderWhenSeveralGatesAreShut(t *testing.T) {
	cases := []struct {
		name  string
		shut  func(f *fixture)
		actor store.Actor
		want  string
	}{
		{"not-allowed beats everything", func(f *fixture) {
			f.config.Runner.Enabled = false
			firePauseFile(t, f.paths)
			f.herdr = nil
		}, store.Actor{Session: "stranger"}, model.CodeNotAllowed},
		{"runner-off beats paused, no herdr, and the cap", func(f *fixture) {
			f.config.Runner.Enabled = false
			firePauseFile(t, f.paths)
			f.herdr = nil
		}, store.Actor{}, model.CodeRunnerOff},
		{"runner-paused beats no herdr and the cap", func(f *fixture) {
			firePauseFile(t, f.paths)
			f.herdr = nil
		}, store.Actor{}, model.CodeRunnerPaused},
		{"no-herdr beats the cap", func(f *fixture) { f.herdr = nil }, store.Actor{}, model.CodeNoHerdr},
		{"cap-reached when nothing else is shut", func(f *fixture) {}, store.Actor{}, model.CodeCapReached},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "", "self")
			spent := f.armThread("spends the cap", "agent")
			stBreak(f, spent.Number)
			tc.shut(f)
			task := f.armThread("refused", "agent")
			before := len(f.runs())
			_, err := f.runner().Start(f.ctx, tc.actor, task.Number, store.RunRoute{})
			if got := fireCode(err); got != tc.want {
				t.Fatalf("Start() error = %v, want code %q", err, tc.want)
			}
			if len(f.runs()) != before || len(f.task(task.Number).History) != 2 {
				t.Fatalf("a refused start wrote runs %#v or history %#v; want nothing", f.runs(), f.task(task.Number).History)
			}
		})
	}
}

func TestStartRefusalNamesTheCapAndTodaysCount(t *testing.T) {
	f := newFixture(t, "", "self")
	f.config.Runner.MaxRunsPerDay = 1
	r := f.runner()
	f.startRun(r, f.armThread("one", "agent").Number)
	_, err := r.Start(f.ctx, store.Actor{}, f.armThread("two", "agent").Number, store.RunRoute{})
	if err == nil || !strings.Contains(err.Error(), "1 runs started today") || !strings.Contains(err.Error(), "max_runs_per_day is 1") {
		t.Fatalf("Start() error = %v, want the count and the limit", err)
	}
}

// A second start must hand back the live run it found: a second workspace, a second note, or a second
// notification would be the bug.
func TestStartOfATaskWithALiveRunReturnsThatRunAndChangesNothing(t *testing.T) {
	f := newFixture(t, "", "in-place")
	r := f.runner()
	running := f.armThread("running", "agent")
	waiting := f.armThread("waiting", "agent")
	first := f.startRun(r, running.Number)
	waitFirst := f.startRun(r, waiting.Number)
	if waitFirst.State != model.RunWaiting {
		t.Fatalf("second run = %#v, want waiting on the busy root", waitFirst)
	}
	notes := len(f.task(running.Number).History)
	waitNotes := len(f.task(waiting.Number).History)

	for _, c := range []struct {
		task int
		want model.Run
		hist int
	}{{running.Number, first, notes}, {waiting.Number, waitFirst, waitNotes}} {
		got, err := r.Start(f.ctx, store.Actor{}, c.task, store.RunRoute{Root: f.root, Isolation: "worktree"})
		if err != nil || got.ID != c.want.ID || got.State != c.want.State {
			t.Fatalf("restart of T%d = %#v, %v; want run %d %s and no error", c.task, got, err, c.want.ID, c.want.State)
		}
		if got := len(f.task(c.task).History); got != c.hist {
			t.Fatalf("restart of T%d grew its history from %d to %d", c.task, c.hist, got)
		}
	}
	if len(f.runs()) != 2 || len(f.herdr.Workspaces()) != 1 {
		t.Fatalf("runs = %#v, workspaces = %#v; want two runs and one workspace", f.runs(), f.herdr.Workspaces())
	}
}

func TestStartEndsAnIdleRunAndStartsANewOne(t *testing.T) {
	f := newFixture(t, "", "self")
	task, old, r := f.start()
	if ok, err := f.store.UpdateRun(f.ctx, old.ID, model.RunRunning, store.RunUpdate{State: model.RunIdle}); err != nil || !ok {
		t.Fatalf("idle the run = (%t, %v)", ok, err)
	}
	got, err := r.Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{Root: f.root})
	if err != nil {
		t.Fatalf("Start over an idle run: %v", err)
	}
	if got.ID == old.ID || got.State != model.RunRunning {
		t.Fatalf("new run = %#v, want a fresh running run, not run %d", got, old.ID)
	}
	runs := f.runs()
	if len(runs) != 2 || runs[0].State != model.RunEnded || runs[1].ID != got.ID {
		t.Fatalf("runs = %#v, want the idle run ended and the new one running", runs)
	}
	// The idle worker is still at its prompt: left open, it could go on working in the root beside the new run.
	if closed := f.herdr.Closed(); !slices.Contains(closed, old.Pane) || slices.Contains(closed, got.Pane) {
		t.Fatalf("closed panes = %v, want the idle run's pane %s closed and the new pane %s open", closed, old.Pane, got.Pane)
	}
}

// An idle run's pane that herdr will not close still holds a worker at its prompt: the new run must not start
// beside it, and the old run carries the retry the ticker owes, though it is no longer its task's newest.
func TestStartOverAnIdleRunWhosePaneDoesNotCloseFailsTheNewRunAndMarksTheOldOneLeftOpen(t *testing.T) {
	f := newFixture(t, "", "self")
	task, old, r := f.start()
	if ok, err := f.store.UpdateRun(f.ctx, old.ID, model.RunRunning, store.RunUpdate{State: model.RunIdle}); err != nil || !ok {
		t.Fatalf("idle the run = (%t, %v)", ok, err)
	}
	f.herdr.Fail("ClosePane", fmt.Errorf("herdr is busy"))
	got, err := r.Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{Root: f.root})
	if err != nil {
		t.Fatalf("Start over an idle run: %v", err)
	}
	if got.ID == old.ID || got.State != model.RunFailed || !strings.Contains(got.Reason, "pane "+old.Pane+" of idle run") {
		t.Fatalf("new run = %#v, want a failed run whose reason names the idle run's pane %s", got, old.Pane)
	}
	if len(f.herdr.Workspaces()) != 1 {
		t.Fatalf("workspaces = %#v, want no new worker beside the open pane", f.herdr.Workspaces())
	}
	if runs := f.runs(); runs[0].State != model.RunEnded || !runs[0].LeftOpen {
		t.Fatalf("old run = %#v, want ended and marked left open", runs[0])
	}
	f.herdr.Fail("ClosePane", nil)
	r.Jobs(f.ctx)
	if !slices.Contains(f.herdr.Closed(), old.Pane) || f.runs()[0].LeftOpen {
		t.Fatalf("after a tick: closed = %v, old run = %#v; want the pane closed and the mark cleared", f.herdr.Closed(), f.runs()[0])
	}
}

// herdr may give a closed pane's id to a pane in another workspace: replacing an idle run must close only the pane
// herdr lists with the run's id in the run's own workspace.
func TestStartOverAnIdleRunLeavesAPaneThatReusedItsIdInAnotherWorkspace(t *testing.T) {
	f := newFixture(t, "", "self")
	task, old, r := f.start()
	if ok, err := f.store.UpdateRun(f.ctx, old.ID, model.RunRunning, store.RunUpdate{State: model.RunIdle}); err != nil || !ok {
		t.Fatalf("idle the run = (%t, %v)", ok, err)
	}
	f.herdr.Remove(old.Pane)
	f.herdr.Set(old.Pane, "someone-else", "working") // the same id, in no workspace of the run's
	got, err := r.Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{Root: f.root})
	if err != nil {
		t.Fatalf("Start over an idle run: %v", err)
	}
	if got.State != model.RunRunning {
		t.Fatalf("new run = %#v, want running: the idle run's own pane is gone", got)
	}
	if slices.Contains(f.herdr.Closed(), old.Pane) {
		t.Fatalf("closed panes = %v, want the other workspace's pane %s left open", f.herdr.Closed(), old.Pane)
	}
}

// failingSpawn is herdr whose workspace create fails after closing the store, so the run cannot be read back.
type failingSpawn struct {
	runner.Herdr
	st *store.Store
}

func (h failingSpawn) CreateWorkspace(context.Context, string, string, []string) (herdr.Created, error) {
	_ = h.st.Close()
	return herdr.Created{}, fmt.Errorf("herdr is gone")
}

// A spawn whose run cannot be read back must not be reported as the starting row StartRun inserted: run start
// exits 0 for that.
func TestStartReturnsAnErrorWhenTheRunCannotBeReadBackAfterItsSpawn(t *testing.T) {
	f := newFixture(t, "", "self")
	task := f.armThread("cannot read back", "agent")
	r := f.runnerWith(failingSpawn{Herdr: f.herdr, st: f.store})
	run, err := r.Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{Root: f.root})
	if err == nil || !strings.Contains(err.Error(), "could not be read back") {
		t.Fatalf("Start = %#v, %v; want an error that the run could not be read back", run, err)
	}
	if !strings.Contains(f.logged(), "read the run back") {
		t.Fatalf("log = %q, want the failed read logged", f.logged())
	}
}

// A kill whose note the secret scan refuses must still block the task: a broken scanner may not leave a task
// started behind a killed run.
func TestKillBlocksTheTaskWhenTheScanRefusesItsNote(t *testing.T) {
	f := newFixture(t, "", "self")
	task, run, _ := f.start()
	_ = f.store.Close()
	var err error
	f.store, err = store.Open(f.paths.DB(), store.Options{Now: func() time.Time { return f.now }, Scanner: func(_ context.Context, text string) (string, error) {
		if strings.Contains(text, "killed") {
			return "", fmt.Errorf("the scanner is broken")
		}
		return "", nil
	}})
	if err != nil {
		t.Fatalf("reopen the store: %v", err)
	}
	got, err := f.runner().Kill(f.ctx, store.Actor{}, task.Number)
	if err != nil || got.Status != model.StatusBlocked {
		t.Fatalf("Kill() = %#v, %v; want the task blocked and no error", got, err)
	}
	if cur := f.run(task.Number); cur.ID != run.ID || cur.State != model.RunKilled {
		t.Fatalf("run = %#v, want run %d killed", cur, run.ID)
	}
	if !strings.Contains(f.logged(), "with a fixed note") {
		t.Fatalf("log = %q, want the withheld note logged", f.logged())
	}
}

// The note is the audit line of a start: run, state at creation, root, isolation, and the model when there is one.
func TestStartWritesTheRunnerNoteInItsFormatForStartingAndWaitingRuns(t *testing.T) {
	f := newFixture(t, "", "in-place")
	f.config.Agent.Models = nil
	r := f.runner()
	first := f.armThread("first", "agent")
	second := f.armThread("second", "agent")
	one := f.startRun(r, first.Number)
	two := f.startRun(r, second.Number)
	for _, c := range []struct {
		task int
		want string
	}{
		{first.Number, fmt.Sprintf("run %d starting: %s (in-place)", one.ID, f.root)},
		{second.Number, fmt.Sprintf("run %d waiting: %s (in-place)", two.ID, f.root)},
	} {
		n := 0
		for _, e := range f.task(c.task).History {
			if strings.Contains(string(e.Data), c.want) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("T%d history holds %d notes %q, want exactly one: %#v", c.task, n, c.want, f.task(c.task).History)
		}
	}
}

// Two runners over one store, as a CLI call and the ticker are: the one whose cap is higher drains the queue.
func TestStartWaitingClaimsAndSpawnsUntilNothingIsClaimable(t *testing.T) {
	f := newFixture(t, "", "self")
	f.config.Runner.Cap = 1
	narrow := f.runner()
	var tasks []model.Task
	for _, title := range []string{"a", "b", "c", "d"} {
		task := f.armThread(title, "agent")
		tasks = append(tasks, task)
		f.startRun(narrow, task.Number)
	}
	runs := f.runs()
	if runs[0].State != model.RunRunning || runs[1].State != model.RunWaiting || runs[3].State != model.RunWaiting {
		t.Fatalf("runs under cap 1 = %#v, want one running and three waiting", runs)
	}

	f.config.Runner.Cap = 3
	f.runner().StartWaiting(f.ctx)
	runs = f.runs()
	for i, run := range runs[:3] {
		if run.State != model.RunRunning || run.Pane == "" {
			t.Fatalf("run %d after StartWaiting = %#v, want running with a pane; log:\n%s", i, run, f.logged())
		}
	}
	if runs[3].State != model.RunWaiting {
		t.Fatalf("fourth run = %#v, want still waiting at the cap of 3", runs[3])
	}
	if got := len(f.herdr.Workspaces()); got != 3 {
		t.Fatalf("workspaces = %d, want 3: one spawn per claimed run, none for the fourth", got)
	}
	f.runner().StartWaiting(f.ctx)
	if got := len(f.herdr.Workspaces()); got != 3 {
		t.Fatalf("workspaces after a second pass = %d, want still 3", got)
	}
}

func TestStartWaitingSpawnsTheOldestWaitingRunFirst(t *testing.T) {
	f := newFixture(t, "", "in-place")
	r := f.runner()
	first := f.armThread("holds the root", "agent")
	older := f.armThread("older", "agent")
	newer := f.armThread("newer", "agent")
	f.startRun(r, first.Number)
	f.startRun(r, older.Number)
	f.startRun(r, newer.Number)
	if ok, err := f.store.UpdateRun(f.ctx, f.run(first.Number).ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !ok {
		t.Fatalf("end the first run = (%t, %v)", ok, err)
	}
	r.StartWaiting(f.ctx)
	if got := f.run(older.Number).State; got != model.RunRunning {
		t.Fatalf("older waiting run = %q, want running", got)
	}
	if got := f.run(newer.Number).State; got != model.RunWaiting {
		t.Fatalf("newer waiting run = %q, want waiting behind the older on the one root", got)
	}
}

// A waiting run is a promise to start when the runner may; paused and off runners keep it.
func TestStartWaitingAndAfterSetStartNothingWhilePausedOrOff(t *testing.T) {
	for _, name := range []string{"paused", "off"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "", "in-place")
			r := f.runner()
			first := f.armThread("first", "agent")
			second := f.armThread("second", "agent")
			f.startRun(r, first.Number)
			f.startRun(r, second.Number)
			if ok, err := f.store.UpdateRun(f.ctx, f.run(first.Number).ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !ok {
				t.Fatalf("end the first run = (%t, %v)", ok, err)
			}
			if name == "paused" {
				firePauseFile(t, f.paths)
			} else {
				f.config.Runner.Enabled = false
			}
			stopped := f.runner()
			stopped.StartWaiting(f.ctx)
			stopped.AfterSet(f.ctx, first.Number)
			stopped.Jobs(f.ctx)
			if got := f.run(second.Number); got.State != model.RunWaiting {
				t.Fatalf("waiting run while %s = %#v, want it kept waiting", name, got)
			}
		})
	}
}

func TestAfterSetStartsAWaitingRunOnceASlotFrees(t *testing.T) {
	f := newFixture(t, "", "in-place")
	r := f.runner()
	first := f.armThread("first", "agent")
	second := f.armThread("second", "agent")
	f.startRun(r, first.Number)
	f.startRun(r, second.Number)
	r.AfterSet(f.ctx, first.Number)
	if got := f.run(second.Number).State; got != model.RunWaiting {
		t.Fatalf("waiting run with the root still held = %q, want waiting", got)
	}
	done := model.StatusDone
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, first.Number, model.Patch{Status: &done}); err != nil {
		t.Fatalf("set the first task done: %v", err)
	}
	r.AfterSet(f.ctx, first.Number)
	if got := f.run(second.Number); got.State != model.RunRunning || got.Pane == "" {
		t.Fatalf("waiting run after AfterSet = %#v, want running with a pane", got)
	}
}

func TestJobsStopsARunningRunPastTheTimeLimitAndLeavesYoungerOnesAlone(t *testing.T) {
	f := newFixture(t, "", "self")
	r := f.runner()
	old := f.armThread("old", "agent")
	f.startRun(r, old.Number)
	f.now = f.now.Add(9 * time.Minute)
	young := f.armThread("young", "agent")
	f.startRun(r, young.Number)
	r.Jobs(f.ctx)
	if f.run(old.Number).State != model.RunRunning {
		t.Fatalf("a run at 9 of 10 minutes was stopped: %#v", f.run(old.Number))
	}
	f.now = f.now.Add(2 * time.Minute)
	r.Jobs(f.ctx)
	if got := f.run(old.Number).State; got == model.RunRunning {
		t.Fatalf("run past max_run_minutes = %q, want it stopped", got)
	}
	if got := f.task(old.Number).Task.Status; got != model.StatusBlocked {
		t.Fatalf("task of the stopped run = %q, want blocked", got)
	}
	if got := f.run(young.Number).State; got != model.RunRunning {
		t.Fatalf("the younger run = %q, want still running", got)
	}
}

// A hand-back claims the run, then writes the task: a Jobs pass between the two must not call the task abandoned.
// The grace is a minute after the run ended.
func TestJobsBlocksATaskLeftStartedByAnEndedRunOnlyAfterTheGrace(t *testing.T) {
	f := newFixture(t, "", "self")
	task, run, r := f.start()
	if ok, err := f.store.UpdateRun(f.ctx, run.ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !ok {
		t.Fatalf("end the run behind the task's back = (%t, %v)", ok, err)
	}
	f.now = f.now.Add(30 * time.Second)
	r.Jobs(f.ctx)
	if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
		t.Fatalf("task 30 s after its run ended = %q, want started (inside the grace)", got)
	}
	f.now = f.now.Add(time.Minute)
	r.Jobs(f.ctx)
	detail := f.task(task.Number)
	if detail.Task.Status != model.StatusBlocked || !fireHasNote(detail.History, fmt.Sprintf("run %d is ended, but its task was left started", run.ID)) {
		t.Fatalf("task after the grace = %#v, history %#v; want blocked with the runner's note", detail.Task, detail.History)
	}
}

// A person who set the task started after the run ended meant it: the repair only undoes the run's own write.
func TestJobsLeavesATaskAPersonSetStartedAfterTheRunEnded(t *testing.T) {
	f := newFixture(t, "", "self")
	task, run, r := f.start()
	if ok, err := f.store.UpdateRun(f.ctx, run.ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !ok {
		t.Fatalf("end the run = (%t, %v)", ok, err)
	}
	blocked, started := model.StatusBlocked, model.StatusStarted
	for _, st := range []*model.Status{&blocked, &started} {
		if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: st}); err != nil {
			t.Fatalf("person sets %s: %v", *st, err)
		}
	}
	f.now = f.now.Add(5 * time.Minute)
	r.Jobs(f.ctx)
	if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
		t.Fatalf("task a person set started = %q, want it left alone", got)
	}
}

func TestJobsStartsWhatWaitsAndFailsStaleStartingRunsInOnePass(t *testing.T) {
	f := newFixture(t, "", "in-place")
	r := f.runner()
	holder := f.armThread("holder", "agent")
	waiter := f.armThread("waiter", "agent")
	f.startRun(r, holder.Number)
	f.startRun(r, waiter.Number)
	if ok, err := f.store.UpdateRun(f.ctx, f.run(holder.Number).ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !ok {
		t.Fatalf("end the holder's run = (%t, %v)", ok, err)
	}
	r.Jobs(f.ctx)
	if got := f.run(waiter.Number).State; got != model.RunRunning {
		t.Fatalf("waiting run after Jobs = %q, want running", got)
	}
	// A run another process left starting: the spawn happens within one call, so a minute on it is dead.
	stuck := f.armThread("stuck", "agent")
	f.config.Runner.Cap = 5
	if _, err := f.store.StartRun(f.ctx, stuck.Number, store.RunRoute{Root: f.root, Isolation: "self"}, store.RunCaps{Slots: 5, PerDay: 1000}); err != nil {
		t.Fatalf("leave a run starting: %v", err)
	}
	f.now = f.now.Add(2 * time.Minute)
	f.runner().Jobs(f.ctx)
	if got := f.run(stuck.Number).State; got != model.RunFailed {
		t.Fatalf("stale starting run = %q, want failed", got)
	}
}

// A run that was claimed starting but whose workspace could not be made must not leave a live run behind.
func TestStartFailingToSpawnLeavesAFailedRunAndABlockedTaskNotALiveOne(t *testing.T) {
	f := newFixture(t, "", "self")
	f.herdr.Fail("CreateWorkspace", fmt.Errorf("herdr is gone"))
	task := f.armThread("cannot spawn", "agent")
	run, err := f.runner().Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked {
		t.Fatalf("returned run = %#v, task = %#v; want the failed run and a blocked task", run, f.task(task.Number).Task)
	}
	live, err := f.store.LiveRuns(context.Background())
	if err != nil || len(live) != 0 {
		t.Fatalf("live runs = %#v, %v; want none", live, err)
	}
}
