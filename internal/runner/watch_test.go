package runner_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

func TestTickBlocksAReportedBlockedSession(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	f.herdr.Set(run.Pane, run.Session, "blocked")

	r.Tick(f.ctx)
	detail := f.task(task.Number)
	if detail.Task.Status != model.StatusBlocked || f.run(task.Number).State != model.RunEnded {
		t.Fatalf("task = %#v; run = %#v, want blocked task and ended run", detail.Task, f.run(task.Number))
	}
	watchAssertRunnerNote(t, detail.History, run.ID, "")
	watchAssertRunnerStatus(t, detail.History, run.ID, model.StatusBlocked)
}

func TestTickReviewsDoneOrIdleSessionsOnlyAfterTwoConsecutiveTicks(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      string
		workerWrote bool
		wantNote    string
	}{
		{name: "done without hand-back", status: "done", wantNote: "session ended without reporting"},
		{name: "idle after worker wrote", status: "idle", workerWrote: true, wantNote: "session went idle without handing back"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "", "in-place")
			task, run, r := f.start()
			if tc.workerWrote {
				if _, err := f.store.Note(f.ctx, store.Actor{Session: run.Session, Run: run.ID}, store.NoteInput{Task: task.Number, NoteData: model.NoteData{Text: "worker progress"}}); err != nil {
					t.Fatalf("write worker event: %v", err)
				}
			}
			f.herdr.Set(run.Pane, run.Session, tc.status)
			r.Tick(f.ctx)
			if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
				t.Fatalf("task after first %s tick = %q, want started", tc.status, got)
			}
			r.Tick(f.ctx)
			detail := f.task(task.Number)
			if detail.Task.Status != model.StatusReview || f.run(task.Number).State != model.RunEnded {
				t.Fatalf("task = %#v; run = %#v, want review task and ended run", detail.Task, f.run(task.Number))
			}
			watchAssertRunnerNote(t, detail.History, run.ID, tc.wantNote)
			watchAssertRunnerStatus(t, detail.History, run.ID, model.StatusReview)
		})
	}
}

func TestTickResetsTheIdleCountWhenTheSessionWorksOrIsUnknown(t *testing.T) {
	for _, reset := range []string{"working", "unknown"} {
		t.Run(reset, func(t *testing.T) {
			f := newFixture(t, "", "in-place")
			task, run, r := f.start()
			f.herdr.Set(run.Pane, run.Session, "idle")
			r.Tick(f.ctx)
			f.herdr.Set(run.Pane, run.Session, reset)
			r.Tick(f.ctx)
			f.herdr.Set(run.Pane, run.Session, "idle")
			r.Tick(f.ctx)
			if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
				t.Fatalf("task after idle, %s, idle = %q, want started", reset, got)
			}
			r.Tick(f.ctx)
			if got := f.task(task.Number).Task.Status; got != model.StatusReview {
				t.Fatalf("task after second post-reset idle = %q, want review", got)
			}
		})
	}
}

func TestTickDoesNotCountIdleForAPaneWithoutTheRunSession(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	f.herdr.Set(run.Pane, "", "idle")
	r.Tick(f.ctx)
	r.Tick(f.ctx)
	if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
		t.Fatalf("task found only by pane id = %q, want started", got)
	}
}

func TestTickReviewsWhenThePaneClosesWithoutAHandBack(t *testing.T) {
	for _, tc := range []struct {
		name        string
		workerWrote bool
		wantNote    string
	}{
		{name: "worker wrote", workerWrote: true, wantNote: "the pane closed without a hand-back"},
		{name: "worker wrote nothing", wantNote: "the pane closed without a hand-back, before the worker wrote anything"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "", "in-place")
			task, run, r := f.start()
			if tc.workerWrote {
				if _, err := f.store.Note(f.ctx, store.Actor{Session: run.Session, Run: run.ID}, store.NoteInput{Task: task.Number, NoteData: model.NoteData{Text: "worker progress"}}); err != nil {
					t.Fatalf("write worker event: %v", err)
				}
			}
			f.herdr.Remove(run.Pane)
			r.Tick(f.ctx)
			detail := f.task(task.Number)
			if detail.Task.Status != model.StatusReview || f.run(task.Number).State != model.RunEnded {
				t.Fatalf("task = %#v; run = %#v, want review task and ended run", detail.Task, f.run(task.Number))
			}
			watchAssertRunnerNote(t, detail.History, run.ID, tc.wantNote)
			watchAssertRunnerStatus(t, detail.History, run.ID, model.StatusReview)
		})
	}
}

func TestTickTimesARunFromItsSpawnNotFromTheWaitBeforeIt(t *testing.T) {
	f := newFixture(t, "", "in-place")
	first, firstRun, r := f.start()
	second := f.armRoute("waits for the root", f.root, "in-place")
	r.Tick(f.ctx)
	if got := f.run(second.Number).State; got != model.RunWaiting {
		t.Fatalf("second run = %q, want waiting while the in-place root is busy", got)
	}
	f.now = f.now.Add(time.Duration(f.config.Runner.MaxRunMinutes+5) * time.Minute)
	f.herdr.Set(firstRun.Pane, firstRun.Session, "working")
	review := model.StatusReview
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, first.Number, model.Patch{Status: &review}); err != nil {
		t.Fatalf("end the first run: %v", err)
	}
	r.Tick(f.ctx)
	run := f.run(second.Number)
	if run.State != model.RunRunning {
		t.Fatalf("second run after the root freed = %#v, want running", run)
	}
	f.herdr.Set(run.Pane, run.Session, "working")
	r.Tick(f.ctx)
	if got := f.run(second.Number).State; got != model.RunRunning {
		t.Fatalf("second run one tick after its spawn = %q, want running: its wait counted against max_run_minutes", got)
	}
	f.now = f.now.Add(time.Duration(f.config.Runner.MaxRunMinutes+1) * time.Minute)
	r.Tick(f.ctx)
	if got := f.run(second.Number).State; got != model.RunKilled {
		t.Fatalf("second run past the limit after its spawn = %q, want killed", got)
	}
}

func TestTickLeavesRunsAloneAfterPanesFailureAndRecoversOnTheNextTick(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	f.herdr.Set(run.Pane, run.Session, "blocked")
	f.herdr.Fail("Panes", errors.New("list failed"))
	before := f.task(task.Number)
	beforeRun := f.run(task.Number)
	r.Tick(f.ctx)
	if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
		t.Fatalf("task after Panes failure = %q, want started", got)
	}
	if got := f.run(task.Number); !reflect.DeepEqual(got, beforeRun) {
		t.Fatalf("run after Panes failure = %#v, want unchanged %#v", got, beforeRun)
	}
	if got := f.task(task.Number).History; !reflect.DeepEqual(got, before.History) {
		t.Fatalf("history after Panes failure = %#v, want unchanged %#v", got, before.History)
	}
	if got := f.logged(); !strings.Contains(got, "list failed") {
		t.Fatalf("watch logs = %q, want Panes failure", got)
	}
	f.herdr.Fail("Panes", nil)
	r.Tick(f.ctx)
	if got := f.task(task.Number).Task.Status; got != model.StatusBlocked {
		t.Fatalf("task after recovered Panes call = %q, want blocked", got)
	}
}

func TestTickDoesNotListPanesWithoutALiveRunAndStillStartsAnArmedTask(t *testing.T) {
	f := newFixture(t, "", "in-place")
	f.herdr.Fail("Panes", errors.New("must not be called"))
	task, err := f.store.AddTask(f.ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "start without watch", Thread: "agent"}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	ready := model.StatusReady
	root, isolation, modelName := f.config.Roots[0].Path, "in-place", "model-a"
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &ready, Root: &root, Isolation: &isolation, Model: &modelName}); err != nil {
		t.Fatalf("arm task: %v", err)
	}
	f.runner().Tick(f.ctx)
	if got := f.run(task.Number).State; got != model.RunRunning {
		t.Fatalf("run after tick with no prior live runs = %q, want running", got)
	}
	if got := f.logged(); strings.Contains(got, "must not be called") {
		t.Fatalf("watch called Panes without a live run: %q", got)
	}
}

func TestTickWatchesLiveRunsWhenTheRunnerIsDisabled(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, _ := f.start()
	f.herdr.Set(run.Pane, run.Session, "done")
	f.config.Runner.Enabled = false
	off := f.runner()
	off.Tick(f.ctx)
	off.Tick(f.ctx)
	if got := f.task(task.Number).Task.Status; got != model.StatusReview {
		t.Fatalf("task watched while disabled = %q, want review", got)
	}
}

func TestTickDoesNothingAfterTheWorkerHandsTheTaskBack(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	review := model.StatusReview
	if _, err := f.store.SetTask(f.ctx, store.Actor{Session: run.Session, Run: run.ID}, task.Number, model.Patch{Status: &review}); err != nil {
		t.Fatalf("worker hand-back: %v", err)
	}
	before := f.task(task.Number)
	r.Tick(f.ctx)
	after := f.task(task.Number)
	if f.run(task.Number).State != model.RunEnded || !reflect.DeepEqual(after.History, before.History) || after.Task.Status != model.StatusReview {
		t.Fatalf("task after hand-back and tick = %#v; run = %#v, want unchanged review task and ended run", after, f.run(task.Number))
	}
}

func TestTickStopsRunsPastTheConfiguredTimeLimit(t *testing.T) {
	f := newFixture(t, "", "in-place")
	f.config.Runner.MaxRunMinutes = 1
	task, run, r := f.start()
	f.herdr.Set(run.Pane, run.Session, "working")
	f.now = f.now.Add(2 * time.Minute)
	r.Tick(f.ctx)
	detail := f.task(task.Number)
	if detail.Task.Status != model.StatusBlocked || f.run(task.Number).State != model.RunKilled || !reflect.DeepEqual(f.herdr.Closed(), []string{run.Pane}) {
		t.Fatalf("task = %#v; run = %#v; closed = %#v, want blocked killed run and closed pane", detail.Task, f.run(task.Number), f.herdr.Closed())
	}
	watchAssertRunnerNote(t, detail.History, run.ID, "stopped after 1 minutes (runner.max_run_minutes)")
	watchAssertRunnerStatus(t, detail.History, run.ID, model.StatusBlocked)
}

func TestTickNamesWhatTheTimeLimitStopCouldNotDo(t *testing.T) {
	f := newFixture(t, "", "in-place")
	f.config.Runner.MaxRunMinutes = 1
	task, run, r := f.start()
	f.herdr.Set(run.Pane, run.Session, "working")
	f.herdr.Fail("ClosePane", errors.New("close denied"))
	f.now = f.now.Add(2 * time.Minute)
	r.Tick(f.ctx)
	detail := f.task(task.Number)
	if detail.Task.Status != model.StatusBlocked || f.run(task.Number).State != model.RunKilled {
		t.Fatalf("task = %#v; run = %#v, want the task blocked and the run killed", detail.Task, f.run(task.Number))
	}
	watchAssertRunnerNote(t, detail.History, run.ID, "stopped after 1 minutes (runner.max_run_minutes); pane "+run.Pane+" did not close")
}

func TestTickBlocksATaskLeftStartedByARunThatEnded(t *testing.T) {
	f := newFixture(t, "", "in-place")
	task, run, r := f.start()
	// A hand-back that claimed the run and then failed to write the task's status leaves this pair.
	if changed, err := f.store.UpdateRun(f.ctx, run.ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !changed {
		t.Fatalf("end the run alone = (%t, %v)", changed, err)
	}
	r.Tick(f.ctx)
	detail := f.task(task.Number)
	if detail.Task.Status != model.StatusBlocked {
		t.Fatalf("task = %q, want blocked: its run ended and nothing else would move it", detail.Task.Status)
	}
	watchAssertRunnerNote(t, detail.History, run.ID, fmt.Sprintf("run %d is ended, but its task was left started", run.ID))
	watchAssertRunnerStatus(t, detail.History, run.ID, model.StatusBlocked)

	started := model.StatusStarted
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &started}); err != nil {
		t.Fatalf("a person sets the task started: %v", err)
	}
	r.Tick(f.ctx)
	if got := f.task(task.Number).Task.Status; got != model.StatusStarted {
		t.Fatalf("task a person set started = %q, want it left started", got)
	}
}

func TestTickLeavesAloneARunWhoseKillIsStillCleaningUp(t *testing.T) {
	f := newFixture(t, "", "in-place")
	h := &hookedHerdr{Herdr: f.herdr}
	task, run, _ := f.start()
	r := f.runnerWith(h)
	inCleanup, release := make(chan struct{}), make(chan struct{})
	h.before("ClosePane", func() {
		close(inCleanup)
		<-release
	})
	killed := make(chan error, 1)
	go func() {
		_, err := r.Kill(f.ctx, store.Actor{}, task.Number)
		killed <- err
	}()
	<-inCleanup

	r.Tick(f.ctx)
	detail := f.task(task.Number)
	close(release)
	if err := <-killed; err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if detail.Task.Status != model.StatusStarted {
		t.Fatalf("task while its kill cleans up = %q, want started until the kill writes it", detail.Task.Status)
	}
	after := f.task(task.Number)
	notes := 0
	for _, event := range after.History {
		var note model.NoteData
		if event.Kind == model.KindNote && event.Run == run.ID && json.Unmarshal(event.Data, &note) == nil && !strings.HasPrefix(note.Text, fmt.Sprintf("run %d: workspace", run.ID)) {
			notes++
			if strings.Contains(note.Text, "left started") {
				t.Fatalf("history holds %q, want no repair of a run whose kill was in flight", note.Text)
			}
		}
	}
	if notes != 1 || after.Task.Status != model.StatusBlocked {
		t.Fatalf("task = %q with %d notes after the start note, want blocked with the kill's one note: %#v", after.Task.Status, notes, after.History)
	}
}

func watchAssertRunnerNote(t *testing.T, history []model.Event, run int64, want string) {
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

func watchAssertRunnerStatus(t *testing.T, history []model.Event, run int64, want model.Status) {
	t.Helper()
	for _, event := range history {
		var patch model.Patch
		if event.Kind == model.KindSet && event.Run == run && event.Session == "" && json.Unmarshal(event.Data, &patch) == nil && patch.Status != nil && *patch.Status == want {
			return
		}
	}
	t.Fatalf("history = %#v, want %s status write carrying run %d and no session", history, want, run)
}
