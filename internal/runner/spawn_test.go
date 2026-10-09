package runner_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestStartStartsInPlaceRunWithSessionWorkspaceEnvironmentNoteAndNotification(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "in-place")
	notice := filepath.Join(t.TempDir(), "notice")
	f.config.Notify.Command = []string{spawnNotifyScript(t, notice)}
	task := f.armRoute("in place", root, "in-place")

	f.startRun(f.runner(), task.Number)
	run := f.run(task.Number)
	if run.State != model.RunRunning || run.Session == "" || run.Workspace == "" || run.Pane == "" {
		t.Fatalf("run = %#v, want running run with session, workspace, and pane", run)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(run.Session) {
		t.Fatalf("run session = %q, want lower-case UUID", run.Session)
	}
	other := newFixture(t, t.TempDir(), "self")
	otherTask := other.armRoute("another session", other.config.Roots[0].Path, "self")
	other.startRun(other.runner(), otherTask.Number)
	if otherRun := other.run(otherTask.Number); otherRun.Session == run.Session {
		t.Fatalf("sessions = %q and %q, want a fresh session for each spawn", run.Session, otherRun.Session)
	}
	workspaces := f.herdr.Workspaces()
	if len(workspaces) != 1 {
		t.Fatalf("workspaces = %#v, want one", workspaces)
	}
	workspace := workspaces[0]
	if workspace.ID != run.Workspace || workspace.Pane != run.Pane || workspace.Label != "desk T"+strconv.Itoa(task.Number) || workspace.Cwd != root {
		t.Fatalf("workspace = %#v; run = %#v, want recorded in-place desk workspace", workspace, run)
	}
	wantEnv := append([]string{
		"DESK_TASK=T" + strconv.Itoa(task.Number),
		"DESK_SESSION=" + run.Session,
		"DESK_RUN=" + strconv.FormatInt(run.ID, 10),
	}, f.paths.Env()...)
	if !reflect.DeepEqual(workspace.Env, wantEnv) {
		t.Fatalf("workspace environment = %#v, want %#v", workspace.Env, wantEnv)
	}
	if wantCommand := "exec " + f.exe + " worker"; workspace.Command != wantCommand {
		t.Fatalf("pane command = %q, want %q", workspace.Command, wantCommand)
	}
	spawnAssertRunnerNote(t, f.task(task.Number).History, run.ID, "run "+strconv.FormatInt(run.ID, 10)+": workspace "+run.Workspace+", pane "+run.Pane)
	if got, err := os.ReadFile(notice); err != nil || string(got) != "notified\n" {
		t.Fatalf("spawn notification = %q (%v), want one invocation", got, err)
	}
}

func TestStartQuotesWorkerExecutableAndRefusesSingleQuotes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		exe          string
		wantCommand  string
		wantSpawned  bool
		wantReasonIn string
	}{
		{
			name:        "spaces are single quoted",
			exe:         "/opt/desk/bin/herdr-desk worker",
			wantCommand: "exec '/opt/desk/bin/herdr-desk worker' worker",
			wantSpawned: true,
		},
		{
			name:         "single quote refuses spawn",
			exe:          "/opt/desk/bin/herdr-desk'worker",
			wantSpawned:  false,
			wantReasonIn: "'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			f := newFixture(t, root, "self")
			f.exe = tc.exe
			task := f.armRoute("quote command", root, "self")

			f.startRun(f.runner(), task.Number)
			run := f.run(task.Number)
			if tc.wantSpawned {
				workspaces := f.herdr.Workspaces()
				if run.State != model.RunRunning || len(workspaces) != 1 || workspaces[0].Command != tc.wantCommand {
					t.Fatalf("run = %#v; workspaces = %#v, want quoted worker command %q", run, workspaces, tc.wantCommand)
				}
				return
			}
			if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked {
				t.Fatalf("run = %#v; task = %#v, want failed run and blocked task", run, f.task(task.Number).Task)
			}
			spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, tc.wantReasonIn)
		})
	}
}

func TestStartCreatesWorktreeAndReusesItOnTheNextRun(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	spawnGitRoot(t, root)
	f := newFixture(t, root, "worktree")
	task := f.armRoute("Ship the Test", root, "worktree")
	worktree := filepath.Join(root, ".claude", "worktrees", "T"+strconv.Itoa(task.Number))
	branch := "desk/T" + strconv.Itoa(task.Number) + "-ship-the-test"
	r := f.runner()

	f.startRun(r, task.Number)
	first := f.run(task.Number)
	if first.State != model.RunRunning || f.herdr.Workspaces()[0].Cwd != worktree || spawnGitBranch(t, worktree) != branch {
		t.Fatalf("first worktree run = %#v; workspaces = %#v; branch = %q, want %q at %q", first, f.herdr.Workspaces(), spawnGitBranch(t, worktree), branch, worktree)
	}
	blocked := model.StatusBlocked
	if _, err := f.store.SetTask(f.ctx, store.Actor{Session: first.Session, Run: first.ID}, task.Number, model.Patch{Status: &blocked}); err != nil {
		t.Fatalf("the first run's worker hands back blocked: %v", err)
	}
	f.startRun(r, task.Number)
	runs, err := f.store.ListRuns(f.ctx)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	workspaces := f.herdr.Workspaces()
	if len(runs) != 2 || runs[1].State != model.RunRunning || len(workspaces) != 2 || workspaces[1].Cwd != worktree || spawnGitBranch(t, worktree) != branch {
		t.Fatalf("rerun = %#v; workspaces = %#v; branch = %q, want reused %q on %q", runs, workspaces, spawnGitBranch(t, worktree), branch, worktree)
	}
}

func TestStartChecksOutExistingWorktreeBranchAndUsesBareNumberForEmptySlug(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	spawnGitRoot(t, root)
	f := newFixture(t, root, "worktree")
	task := f.armRoute("!!!", root, "worktree")
	branch := "desk/T" + strconv.Itoa(task.Number)
	spawnGit(t, root, "branch", branch)

	f.startRun(f.runner(), task.Number)
	run := f.run(task.Number)
	worktree := filepath.Join(root, ".claude", "worktrees", "T"+strconv.Itoa(task.Number))
	if run.State != model.RunRunning || f.herdr.Workspaces()[0].Cwd != worktree || spawnGitBranch(t, worktree) != branch {
		t.Fatalf("run = %#v; workspaces = %#v; branch = %q, want existing %q checked out at %q", run, f.herdr.Workspaces(), spawnGitBranch(t, worktree), branch, worktree)
	}
}

func TestStartBlocksTaskWhenWorktreeRootIsNotAGitRepository(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "worktree")
	task := f.armRoute("not a repository", root, "worktree")

	f.startRun(f.runner(), task.Number)
	run := f.run(task.Number)
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(f.herdr.Workspaces()) != 0 {
		t.Fatalf("run = %#v; task = %#v; workspaces = %#v, want failed blocked task without a workspace", run, f.task(task.Number).Task, f.herdr.Workspaces())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "")
}

func TestStartMakesScratchRootBeforeStartingTheWorkspace(t *testing.T) {
	machine := testutil.NewMachine(t)
	root := machine.Paths.ScratchRoot()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("scratch root before the start = %v, want missing", err)
	}
	f := newFixture(t, root, "in-place")
	f.paths = machine.Paths
	f.config.Roots = []config.Root{{Path: root, Isolation: "in-place"}}
	task := f.armRoute("scratch task", root, "in-place")

	f.startRun(f.runner(), task.Number)
	if run := f.run(task.Number); run.State != model.RunRunning {
		t.Fatalf("scratch run = %#v, want running", run)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("scratch root after the start = %v (%v), want directory", info, err)
	}
}

func TestStartUsesSelfRootAsTheWorkspaceDirectory(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "self")
	task := f.armRoute("self task", root, "self")

	f.startRun(f.runner(), task.Number)
	workspaces := f.herdr.Workspaces()
	if run := f.run(task.Number); run.State != model.RunRunning || len(workspaces) != 1 || workspaces[0].Cwd != root {
		t.Fatalf("run = %#v; workspaces = %#v, want root %q used directly", run, workspaces, root)
	}
}

func TestStartFailsAndBlocksWhenCreatingTheWorkspaceFails(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "self")
	f.herdr.Fail("CreateWorkspace", errors.New("create denied"))
	task := f.armRoute("workspace failure", root, "self")

	f.startRun(f.runner(), task.Number)
	run := f.run(task.Number)
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(f.herdr.Workspaces()) != 0 {
		t.Fatalf("run = %#v; task = %#v; workspaces = %#v, want failed blocked spawn without workspace", run, f.task(task.Number).Task, f.herdr.Workspaces())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "create denied")
}

func TestStartClosesOpenedPaneAndBlocksWhenStartingWorkerFails(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "self")
	f.herdr.Fail("Run", errors.New("run denied"))
	task := f.armRoute("worker failure", root, "self")

	f.startRun(f.runner(), task.Number)
	run := f.run(task.Number)
	workspaces := f.herdr.Workspaces()
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(workspaces) != 1 || run.Workspace != workspaces[0].ID || run.Pane != workspaces[0].Pane || !reflect.DeepEqual(f.herdr.Closed(), []string{run.Pane}) {
		t.Fatalf("run = %#v; task = %#v; closed panes = %#v, want failed blocked task and closed pane", run, f.task(task.Number).Task, f.herdr.Closed())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "run denied")
}

func TestStartStartsNoWorkerForARunKilledDuringItsSpawn(t *testing.T) {
	for _, when := range []string{"after CreateWorkspace", "before Run", "after Run"} {
		t.Run(when, func(t *testing.T) {
			root := t.TempDir()
			f := newFixture(t, root, "self")
			notice := filepath.Join(t.TempDir(), "notice")
			f.config.Notify.Command = []string{spawnNotifyScript(t, notice)}
			task := f.armRoute("killed while it starts", root, "self")
			h := &hookedHerdr{Herdr: f.herdr}
			r := f.runnerWith(h)
			hook := h.after
			if strings.HasPrefix(when, "before") {
				hook = h.before
			}
			hook(strings.Fields(when)[1], func() {
				if _, err := r.Kill(f.ctx, store.Actor{}, task.Number); err != nil {
					t.Errorf("Kill during the spawn: %v", err)
				}
			})

			f.startRun(r, task.Number)
			run := f.run(task.Number)
			workspaces := f.herdr.Workspaces()
			if run.State != model.RunKilled || f.task(task.Number).Task.Status != model.StatusBlocked || len(workspaces) != 1 {
				t.Fatalf("run = %#v; task = %#v; workspaces = %#v, want a killed run and a blocked task", run, f.task(task.Number).Task, workspaces)
			}
			if got := f.herdr.Closed(); !reflect.DeepEqual(got, []string{workspaces[0].Pane}) {
				t.Fatalf("closed panes = %#v, want the spawned pane closed", got)
			}
			if when != "after Run" && workspaces[0].Command != "" {
				t.Fatalf("pane command = %q, want none typed into the pane of a killed run", workspaces[0].Command)
			}
			for _, event := range f.task(task.Number).History {
				var note model.NoteData
				if event.Kind == model.KindNote && json.Unmarshal(event.Data, &note) == nil && strings.HasPrefix(note.Text, fmt.Sprintf("run %d: workspace", run.ID)) {
					t.Fatalf("history holds the started note %q for a killed run", note.Text)
				}
			}
			if _, err := os.Stat(notice); !os.IsNotExist(err) {
				t.Fatalf("notify ran (%v), want no started notification for a killed run", err)
			}
		})
	}
}

func TestStartFailsTheRunWhenItsPaneCannotBeRecorded(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "self")
	task := f.armRoute("pane not recorded", root, "self")
	// The store refuses the one write that records a pane on a run; every other write goes through.
	db, err := sql.Open("sqlite", f.paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER refuse_pane BEFORE UPDATE OF pane ON runs WHEN NEW.pane != ''
		BEGIN SELECT RAISE(ABORT, 'pane write refused'); END`); err != nil {
		t.Fatalf("add the trigger: %v", err)
	}

	f.startRun(f.runner(), task.Number)
	run := f.run(task.Number)
	workspaces := f.herdr.Workspaces()
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(workspaces) != 1 || workspaces[0].Command != "" || !reflect.DeepEqual(f.herdr.Closed(), []string{workspaces[0].Pane}) {
		t.Fatalf("run = %#v; workspaces = %#v; closed = %#v, want a failed run, no command, and the pane closed", run, workspaces, f.herdr.Closed())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "could not record the pane")
}

func TestStartReportsNoStartWhenItCannotConfirmTheRunAfterTheCommand(t *testing.T) {
	t.Run("the run row cannot be read", func(t *testing.T) {
		root := t.TempDir()
		f := newFixture(t, root, "self")
		task := f.armRoute("unconfirmed start", root, "self")
		h := &hookedHerdr{Herdr: f.herdr}
		db, err := sql.Open("sqlite", f.paths.DB())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		// Once the command is typed, the run row cannot be read back: its start time no longer parses.
		var started string
		h.after("Run", func() {
			if err := db.QueryRow(`SELECT started_ts FROM runs WHERE task = ?`, task.Number).Scan(&started); err != nil {
				t.Errorf("read the start time: %v", err)
			}
			if _, err := db.Exec(`UPDATE runs SET started_ts = 'unreadable' WHERE task = ?`, task.Number); err != nil {
				t.Errorf("spoil the start time: %v", err)
			}
		})
		spawnAssertUndone(t, f.ctx, f, task, h, "could not confirm the run", func() {
			if _, err := db.Exec(`UPDATE runs SET started_ts = ? WHERE task = ?`, started, task.Number); err != nil {
				t.Fatalf("restore the start time: %v", err)
			}
		})
	})
}

func TestStartUndoesASpawnWhoseContextEndsOnceThePaneExists(t *testing.T) {
	for _, tc := range []struct {
		name, hook, reason string
	}{
		{"before the pane is recorded", "after CreateWorkspace", "could not record the pane"},
		{"while the command is typed", "before Run", "context canceled"},
		{"before the run is confirmed", "after Run", "could not confirm the run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			f := newFixture(t, root, "self")
			task := f.armRoute("canceled start", root, "self")
			h := &ctxHerdr{hookedHerdr: &hookedHerdr{Herdr: f.herdr}}
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			// The process stops while the spawn runs: every call on ctx from here on fails.
			h.set(tc.hook, cancel)
			spawnAssertUndone(t, ctx, f, task, h, tc.reason, func() {})
		})
	}
}

// ctxHerdr is the hooked herdr whose Run and ClosePane fail once their context has ended, as the real client's do.
type ctxHerdr struct{ *hookedHerdr }

func (h *ctxHerdr) Run(ctx context.Context, pane, command string) error {
	h.fire("before Run")
	defer h.fire("after Run")
	if err := ctx.Err(); err != nil {
		return err
	}
	return h.Herdr.Run(ctx, pane, command)
}

func (h *ctxHerdr) ClosePane(ctx context.Context, pane string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return h.hookedHerdr.ClosePane(ctx, pane)
}

// spawnAssertUndone starts a run of the task through a runner on h with ctx, runs restore, and checks the run failed with reason in its
// spawn note, the task blocked, and the pane closed, with no started note and no notification.
func spawnAssertUndone(t *testing.T, ctx context.Context, f *fixture, task model.Task, h runner.Herdr, reason string, restore func()) {
	t.Helper()
	notice := filepath.Join(t.TempDir(), "notice")
	f.config.Notify.Command = []string{spawnNotifyScript(t, notice)}
	_, _ = f.runnerWith(h).Start(ctx, store.Actor{}, task.Number, store.RunRoute{})
	restore()
	run := f.run(task.Number)
	workspaces := f.herdr.Workspaces()
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(workspaces) != 1 || !reflect.DeepEqual(f.herdr.Closed(), []string{workspaces[0].Pane}) {
		t.Fatalf("run = %#v; task = %#v; closed = %#v, want a failed run, a blocked task, and the pane closed", run, f.task(task.Number).Task, f.herdr.Closed())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, reason)
	for _, event := range f.task(task.Number).History {
		var note model.NoteData
		if event.Kind == model.KindNote && json.Unmarshal(event.Data, &note) == nil && strings.HasPrefix(note.Text, fmt.Sprintf("run %d: workspace", run.ID)) {
			t.Fatalf("history holds the started note %q for a run it could not confirm", note.Text)
		}
	}
	if _, err := os.Stat(notice); !os.IsNotExist(err) {
		t.Fatalf("notify ran (%v), want no started notification for a run it could not confirm", err)
	}
}

func TestStartNamesAPaneItCouldNotCloseAfterAFailedSpawnAndJobsClosesItLater(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "self")
	f.herdr.Fail("Run", errors.New("run denied"))
	f.herdr.Fail("ClosePane", errors.New("close denied"))
	task := f.armRoute("pane left open", root, "self")
	r := f.runner()

	f.startRun(r, task.Number)
	run := f.run(task.Number)
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked {
		t.Fatalf("run = %#v; task = %#v, want a failed run and a blocked task", run, f.task(task.Number).Task)
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "pane "+run.Pane+" was left open")
	f.herdr.Fail("ClosePane", nil)
	r.Jobs(f.ctx)
	if got := f.herdr.Closed(); !reflect.DeepEqual(got, []string{run.Pane}) {
		t.Fatalf("closed panes after the next jobs = %#v, want the pane left open closed", got)
	}
}

func spawnNotifyScript(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notify")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'notified\\n' >> "+fmt.Sprintf("%q", output)+"\n"), 0o700); err != nil {
		t.Fatalf("write notify script: %v", err)
	}
	return path
}

func spawnGitRoot(t *testing.T, root string) {
	t.Helper()
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("make git root: %v", err)
	}
	spawnGit(t, root, "init")
	spawnGit(t, root, "-c", "user.name=desk test", "-c", "user.email=desk@example.test", "commit", "--allow-empty", "-m", "initial")
}

func spawnGitBranch(t *testing.T, root string) string {
	t.Helper()
	return strings.TrimSpace(spawnGit(t, root, "branch", "--show-current"))
}

func spawnGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func spawnAssertRunnerNote(t *testing.T, history []model.Event, run int64, want string) {
	t.Helper()
	for _, event := range history {
		var note model.NoteData
		if event.Kind == model.KindNote && event.Run == run && event.Session == "" && reflect.DeepEqual(event.Tags, []string{model.TagRunner}) && json.Unmarshal(event.Data, &note) == nil && note.Text == want {
			return
		}
	}
	t.Fatalf("history = %#v, want runner note %q for run %d without session", history, want, run)
}

func spawnAssertFailureNote(t *testing.T, history []model.Event, run int64, reason string) {
	t.Helper()
	for _, event := range history {
		var note model.NoteData
		if event.Kind == model.KindNote && event.Run == run && event.Session == "" && reflect.DeepEqual(event.Tags, []string{model.TagRunner}) && json.Unmarshal(event.Data, &note) == nil && strings.HasPrefix(note.Text, "spawn: ") && (reason == "" || strings.Contains(note.Text, reason)) {
			return
		}
	}
	t.Fatalf("history = %#v, want runner spawn failure note containing %q for run %d", history, reason, run)
}
