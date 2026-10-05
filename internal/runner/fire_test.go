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

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestStartRunsUpToTheCapThenWaitsUntilAWorkerHandsBack(t *testing.T) {
	f := newFixture(t, "", "self")
	f.config.Runner.Cap = 3
	r := f.runner()
	var tasks []model.Task
	for _, title := range []string{"first", "second", "third", "fourth"} {
		task := f.armThread(title, "agent")
		tasks = append(tasks, task)
		f.startRun(r, task.Number)
	}
	runs := f.runs()
	if len(runs) != 4 {
		t.Fatalf("runs = %#v, want four", runs)
	}
	for i, run := range runs {
		want := model.RunRunning
		if i == 3 {
			want = model.RunWaiting
		}
		if run.Task != tasks[i].Number || run.State != want {
			t.Fatalf("run %d = T%d %s, want T%d %s; runs = %#v; log:\n%s", i, run.Task, run.State, tasks[i].Number, want, runs, f.logged())
		}
	}
	if got := f.herdr.Workspaces(); len(got) != 3 || got[0].Cwd != f.root || got[0].Command == "" {
		t.Fatalf("workspaces = %#v, want one started workspace per running run rooted at %q", got, f.root)
	}
	if got := f.task(tasks[3].Number).Task.Status; got != model.StatusStarted {
		t.Fatalf("waiting task status = %q, want started", got)
	}

	review := model.StatusReview
	if _, err := f.store.SetTask(f.ctx, store.Actor{Session: runs[0].Session, Run: runs[0].ID}, tasks[0].Number, model.Patch{Status: &review}); err != nil {
		t.Fatalf("worker hands back: %v", err)
	}
	r.AfterSet(f.ctx, tasks[0].Number)
	runs = f.runs()
	if runs[0].State != model.RunEnded || runs[3].State != model.RunRunning || runs[3].Workspace == "" {
		t.Fatalf("runs after a hand-back = %#v, want the first ended and the fourth running", runs)
	}
}

func TestStartCountsOnlyRunsStartedSinceLocalMidnight(t *testing.T) {
	f := newFixture(t, "", "self")
	f.config.Runner.MaxRunsPerDay = 1
	r := f.runner()
	yesterday := f.armThread("yesterday", "agent")
	f.startRun(r, yesterday.Number)
	today := f.armThread("today", "agent")
	if _, err := r.Start(f.ctx, store.Actor{}, today.Number, store.RunRoute{}); fireCode(err) != model.CodeCapReached {
		t.Fatalf("second start the same day error = %v, want cap-reached", err)
	}
	f.now = f.now.AddDate(0, 0, 1)
	f.startRun(r, today.Number)
	runs := f.runs()
	if len(runs) != 2 || runs[0].Task != yesterday.Number || runs[1].Task != today.Number {
		t.Fatalf("runs across midnight = %#v, want yesterday then today", runs)
	}
}

func TestStartWritesOneRouteNoteAndRunsWithTheResolvedRoute(t *testing.T) {
	f := newFixture(t, "", "self")
	task := f.armThread("route me", "agent")
	run := f.startRun(f.runner(), task.Number)
	detail := f.task(task.Number)
	if run.State != model.RunRunning || run.Root != f.root || run.Isolation != "self" || run.Model != "model-a" {
		t.Fatalf("run = %#v, want running on the root with its isolation and the first model", run)
	}
	if detail.Task.Root != f.root || detail.Task.Isolation != "self" || detail.Task.Model != "model-a" {
		t.Fatalf("task = %#v, want the route recorded on it", detail.Task)
	}
	if want := fmt.Sprintf("run %d starting: %s (self, model-a)", run.ID, f.root); !fireHasNote(detail.History, want) {
		t.Fatalf("history = %#v, want the note %q", detail.History, want)
	}
}

func TestStartRefusesARouteThatFailsItsCheckAndWritesNothing(t *testing.T) {
	f := newFixture(t, "", "self")
	task := f.armThread("bad decided route", "agent")
	badRoot := "/not-listed"
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Root: &badRoot}); err != nil {
		t.Fatalf("set bad root: %v", err)
	}
	before := len(f.task(task.Number).History)
	if _, err := f.runner().Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{}); fireCode(err) != model.CodeBadInput {
		t.Fatalf("Start() error = %v, want bad-input", err)
	}
	if got := f.runs(); len(got) != 0 || len(f.task(task.Number).History) != before || len(f.herdr.Workspaces()) != 0 {
		t.Fatalf("runs = %#v, history grew, or a workspace opened; want nothing written", got)
	}
}

func TestStartRefusesARouteTheScannerRefusesAndOpensNoPane(t *testing.T) {
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
	if _, err := f.runner().Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{}); fireCode(err) != model.CodeSecretDetected {
		t.Fatalf("Start() error = %v, want secret-detected", err)
	}
	if len(f.runs()) != 0 || len(f.herdr.Workspaces()) != 0 {
		t.Fatalf("runs = %#v; workspaces = %#v, want none", f.runs(), f.herdr.Workspaces())
	}
}

func TestStartWaitsOnlyForConflictingInPlaceRoutes(t *testing.T) {
	f := newFixture(t, "", "in-place")
	r := f.runner()
	first := f.armThread("first", "agent")
	second := f.armThread("second", "agent")
	f.startRun(r, first.Number)
	f.startRun(r, second.Number)
	runs := f.runs()
	if len(runs) != 2 || runs[0].State != model.RunRunning || runs[1].State != model.RunWaiting || runs[1].Workspace != "" {
		t.Fatalf("in-place runs = %#v, want running first and workspace-less waiting second", runs)
	}
	if changed, err := f.store.UpdateRun(f.ctx, runs[0].ID, model.RunRunning, store.RunUpdate{State: model.RunEnded}); err != nil || !changed {
		t.Fatalf("end first run = (%t, %v)", changed, err)
	}
	r.Jobs(f.ctx)
	if run := f.runs()[1]; run.State != model.RunRunning || run.Workspace == "" {
		t.Fatalf("waiting run after the root frees = %#v, want running with a workspace", run)
	}
}

func TestStartDoesNotWaitForSelfOrWorktreeRoutes(t *testing.T) {
	for _, isolation := range []string{"self", "worktree"} {
		t.Run(isolation, func(t *testing.T) {
			f := newFixture(t, "", isolation)
			if isolation == "worktree" {
				fireGitRoot(t, f.root)
			}
			r := f.runner()
			f.startRun(r, f.armThread("first", "agent").Number)
			f.startRun(r, f.armThread("second", "agent").Number)
			runs := f.runs()
			if len(runs) != 2 || runs[0].State != model.RunRunning || runs[1].State != model.RunRunning {
				t.Fatalf("%s runs = %#v, want both running; log:\n%s", isolation, runs, f.logged())
			}
		})
	}
}

func TestJobsFailsARunLeftStartingForOverAMinute(t *testing.T) {
	f := newFixture(t, "", "self")
	task := f.armThread("interrupted start", "agent")
	if _, err := f.store.StartRun(f.ctx, task.Number, store.RunRoute{Root: f.root, Isolation: "self"}, 3); err != nil {
		t.Fatalf("start a run with no spawn: %v", err)
	}
	r := f.runner()
	f.now = f.now.Add(30 * time.Second)
	r.Jobs(f.ctx)
	if run := f.runs()[0]; run.State != model.RunStarting {
		t.Fatalf("run after 30 s = %#v, want still starting", run)
	}
	f.now = f.now.Add(time.Minute)
	r.Jobs(f.ctx)
	run := f.runs()[0]
	detail := f.task(task.Number)
	if run.State != model.RunFailed || detail.Task.Status != model.StatusBlocked || !fireHasNote(detail.History, "left starting") {
		t.Fatalf("run = %#v; task = %#v; want a failed run and a blocked task with a note", run, detail)
	}
}

func TestStateUsesTheDocumentedPrecedenceAndStartRefusesOutsideOn(t *testing.T) {
	cases := []struct {
		name   string
		adjust func(*fixture)
		want   string
		code   string
	}{
		{"off wins", func(f *fixture) { f.config.Runner.Enabled = false; f.herdr = nil }, runner.StateOff, model.CodeRunnerOff},
		{"paused beats missing herdr", func(f *fixture) { f.herdr = nil; firePauseFile(t, f.paths) }, runner.StatePaused, model.CodeRunnerPaused},
		{"no herdr", func(f *fixture) { f.herdr = nil }, runner.StateNoHerdr, model.CodeNoHerdr},
		{"on", func(f *fixture) {}, runner.StateOn, ""},
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
			task := f.armThread("not started outside on", "agent")
			r := f.runner()
			if got := r.State(); got != tc.want {
				t.Fatalf("State() = %q, want %q", got, tc.want)
			}
			_, err := r.Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{})
			if got := fireCode(err); got != tc.code {
				t.Fatalf("Start() error = %v, want code %q", err, tc.code)
			}
			if tc.code != "" && len(f.runs()) != 0 {
				t.Fatalf("runs = %#v, want none in %s", f.runs(), tc.want)
			}
		})
	}
}

func TestStartRefusesAnAgentThatIsNotTheRecordedCoordinator(t *testing.T) {
	f := newFixture(t, "", "self")
	r := f.runner()
	task := f.armThread("only a person or the coordinator", "agent")
	for _, a := range []store.Actor{{Session: "worker-session", Run: 1}, {Session: "other-agent"}} {
		if _, err := r.Start(f.ctx, a, task.Number, store.RunRoute{}); fireCode(err) != model.CodeNotAllowed {
			t.Fatalf("Start(%#v) error = %v, want not-allowed", a, err)
		}
	}
	if err := f.store.SetCoordinator(f.ctx, store.Actor{}, model.Coordinator{Session: "coordinator-session", Workspace: "w9", Pane: "w9-1"}); err != nil {
		t.Fatalf("record the coordinator: %v", err)
	}
	run, err := r.Start(f.ctx, store.Actor{Session: "coordinator-session"}, task.Number, store.RunRoute{})
	if err != nil || run.State != model.RunRunning {
		t.Fatalf("coordinator Start() = %#v, %v; want a running run", run, err)
	}
}

func TestPausePersistsPrivateFileRefusesAgentsAndResumesStarting(t *testing.T) {
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
	task := f.armThread("wait while paused", "agent")
	if _, err := r.Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{}); fireCode(err) != model.CodeRunnerPaused {
		t.Fatalf("Start() while paused error = %v, want runner-paused", err)
	}
	if err := r.Pause(f.ctx, store.Actor{}, false); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if r.Paused() {
		t.Fatal("Paused() = true after resume")
	}
	f.startRun(r, task.Number)
	if len(f.runs()) != 1 {
		t.Fatalf("runs after resume = %#v, want one", f.runs())
	}
}

func TestNotifyRunsOnceForEachSpawn(t *testing.T) {
	f := newFixture(t, "", "self")
	notice := filepath.Join(t.TempDir(), "notice")
	script := filepath.Join(t.TempDir(), "notify")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s|%s\\n' \"$1\" \"$2\" >> "+fmt.Sprintf("%q", notice)+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.config.Notify.Command = []string{script, "{title}", "{body}"}
	r := f.runner()
	first := f.armThread("first task", "agent")
	second := f.armThread("second task", "agent")
	f.startRun(r, first.Number)
	f.startRun(r, second.Number)
	f.startRun(r, first.Number)
	got, err := os.ReadFile(notice)
	if err != nil {
		t.Fatalf("read spawn notifications: %v", err)
	}
	want := "herdr-desk: T" + fmt.Sprint(first.Number) + " started|first task\n" + "herdr-desk: T" + fmt.Sprint(second.Number) + " started|second task\n"
	if string(got) != want {
		t.Fatalf("spawn notifications = %q, want %q: a start of a live run notifies nothing", got, want)
	}
}

func TestNotifyFailureDoesNotPreventTheSpawn(t *testing.T) {
	f := newFixture(t, "", "self")
	f.config.Notify.Command = []string{filepath.Join(t.TempDir(), "missing-notify"), "{title}", "{body}"}
	run := f.startRun(f.runner(), f.armThread("still starts", "agent").Number)
	if run.State != model.RunRunning {
		t.Fatalf("run after notify failure = %#v, want running", run)
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
	task := f.armThread("not while the pause is unreadable", "agent")
	r := f.runner()
	if !r.Paused() || r.State() != runner.StatePaused {
		t.Fatalf("paused = %t; state = %q, want paused when the pause file cannot be read", r.Paused(), r.State())
	}
	_, err := r.Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{})
	if fireCode(err) != model.CodeRunnerPaused || !strings.Contains(err.Error(), "pause file cannot be read") {
		t.Fatalf("Start() error = %v, want runner-paused naming the unreadable pause file", err)
	}
	if got := f.runs(); len(got) != 0 {
		t.Fatalf("runs = %#v, want none while the pause file cannot be read", got)
	}
}

func TestNoHerdrNamesWhyDeskHerdrCannotBeUsed(t *testing.T) {
	f := newFixture(t, "", "self")
	t.Setenv("DESK_HERDR", "herdr")
	r := f.runnerWith(nil)
	if got := r.State(); got != runner.StateNoHerdr {
		t.Fatalf("State() = %q, want no-herdr", got)
	}
	_, err := r.Start(f.ctx, store.Actor{}, f.armThread("no herdr", "agent").Number, store.RunRoute{})
	if fireCode(err) != model.CodeNoHerdr || !strings.Contains(err.Error(), `DESK_HERDR "herdr" is not an absolute path`) {
		t.Fatalf("Start() error = %v, want no-herdr naming the DESK_HERDR reason", err)
	}
}

func TestStartMatchesARootAndAProjectWrittenThroughASymlink(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, link, "self")
	r := f.runner()
	byProject, err := f.store.AddTask(f.ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "project at the real path", Project: real}})
	if err != nil {
		t.Fatal(err)
	}
	if run := f.startRun(r, byProject.Number); run.State != model.RunRunning || run.Root != link {
		t.Fatalf("run = %#v, want it running on the configured root %q", run, link)
	}
	byReal := f.armRoute("root written by its real path", real, "self")
	if run := f.startRun(r, byReal.Number); run.State != model.RunRunning || run.Root != link {
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

// fireCode is the refusal code of err, "" when err is nil or no refusal.
func fireCode(err error) string {
	if r, ok := model.AsRefusal(err); ok {
		return r.Code
	}
	return ""
}

func fireHasNote(history []model.Event, fragment string) bool {
	for _, event := range history {
		var note model.NoteData
		if event.Kind == model.KindNote && json.Unmarshal(event.Data, &note) == nil && strings.Contains(note.Text, fragment) &&
			reflect.DeepEqual(event.Tags, []string{model.TagRunner}) {
			return true
		}
	}
	return false
}
