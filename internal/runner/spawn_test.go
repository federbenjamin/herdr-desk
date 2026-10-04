package runner_test

import (
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

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
)

func TestTickStartsInPlaceRunWithSessionWorkspaceEnvironmentNoteAndNotification(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "in-place")
	notice := filepath.Join(t.TempDir(), "notice")
	f.config.Notify.Command = []string{spawnNotifyScript(t, notice)}
	task := f.armRoute("in place", root, "in-place")

	f.runner().Tick(f.ctx)
	run := f.run(task.Number)
	if run.State != model.RunRunning || run.Session == "" || run.Workspace == "" || run.Pane == "" {
		t.Fatalf("run = %#v, want running run with session, workspace, and pane", run)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(run.Session) {
		t.Fatalf("run session = %q, want lower-case UUID", run.Session)
	}
	other := newFixture(t, t.TempDir(), "self")
	otherTask := other.armRoute("another session", other.config.Roots[0].Path, "self")
	other.runner().Tick(other.ctx)
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

func TestTickQuotesWorkerExecutableAndRefusesSingleQuotes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		exe          string
		wantCommand  string
		wantSpawned  bool
		wantReasonIn string
	}{
		{
			name:        "spaces are single quoted",
			exe:         "/opt/desk/bin/desk worker",
			wantCommand: "exec '/opt/desk/bin/desk worker' worker",
			wantSpawned: true,
		},
		{
			name:         "single quote refuses spawn",
			exe:          "/opt/desk/bin/desk'worker",
			wantSpawned:  false,
			wantReasonIn: "'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			f := newFixture(t, root, "self")
			f.exe = tc.exe
			task := f.armRoute("quote command", root, "self")

			f.runner().Tick(f.ctx)
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

func TestTickCreatesWorktreeAndReusesItAfterTheTaskIsRearmed(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	spawnGitRoot(t, root)
	f := newFixture(t, root, "worktree")
	task := f.armRoute("Ship the Test", root, "worktree")
	worktree := filepath.Join(parent, "repo-T"+strconv.Itoa(task.Number))
	branch := "desk/T" + strconv.Itoa(task.Number) + "-ship-the-test"
	r := f.runner()

	r.Tick(f.ctx)
	first := f.run(task.Number)
	if first.State != model.RunRunning || f.herdr.Workspaces()[0].Cwd != worktree || spawnGitBranch(t, worktree) != branch {
		t.Fatalf("first worktree run = %#v; workspaces = %#v; branch = %q, want %q at %q", first, f.herdr.Workspaces(), spawnGitBranch(t, worktree), branch, worktree)
	}
	blocked := model.StatusBlocked
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &blocked}); err != nil {
		t.Fatalf("block first run: %v", err)
	}
	ready := model.StatusReady
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &ready}); err != nil {
		t.Fatalf("re-arm task: %v", err)
	}
	r.Tick(f.ctx)
	runs, err := f.store.ListRuns(f.ctx)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	workspaces := f.herdr.Workspaces()
	if len(runs) != 2 || runs[1].State != model.RunRunning || len(workspaces) != 2 || workspaces[1].Cwd != worktree || spawnGitBranch(t, worktree) != branch {
		t.Fatalf("rerun = %#v; workspaces = %#v; branch = %q, want reused %q on %q", runs, workspaces, spawnGitBranch(t, worktree), branch, worktree)
	}
}

func TestTickChecksOutExistingWorktreeBranchAndUsesBareNumberForEmptySlug(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	spawnGitRoot(t, root)
	f := newFixture(t, root, "worktree")
	task := f.armRoute("!!!", root, "worktree")
	branch := "desk/T" + strconv.Itoa(task.Number)
	spawnGit(t, root, "branch", branch)

	f.runner().Tick(f.ctx)
	run := f.run(task.Number)
	worktree := filepath.Join(parent, "repo-T"+strconv.Itoa(task.Number))
	if run.State != model.RunRunning || f.herdr.Workspaces()[0].Cwd != worktree || spawnGitBranch(t, worktree) != branch {
		t.Fatalf("run = %#v; workspaces = %#v; branch = %q, want existing %q checked out at %q", run, f.herdr.Workspaces(), spawnGitBranch(t, worktree), branch, worktree)
	}
}

func TestTickBlocksTaskWhenWorktreeRootIsNotAGitRepository(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "worktree")
	task := f.armRoute("not a repository", root, "worktree")

	f.runner().Tick(f.ctx)
	run := f.run(task.Number)
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(f.herdr.Workspaces()) != 0 {
		t.Fatalf("run = %#v; task = %#v; workspaces = %#v, want failed blocked task without a workspace", run, f.task(task.Number).Task, f.herdr.Workspaces())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "")
}

func TestTickMakesScratchRootBeforeStartingTheWorkspace(t *testing.T) {
	machine := testutil.NewMachine(t)
	root := machine.Paths.ScratchRoot()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("scratch root before tick = %v, want missing", err)
	}
	f := newFixture(t, root, "in-place")
	f.paths = machine.Paths
	f.config.Roots = []config.Root{{Path: root, Isolation: "in-place"}}
	task := f.armRoute("scratch task", root, "in-place")

	f.runner().Tick(f.ctx)
	if run := f.run(task.Number); run.State != model.RunRunning {
		t.Fatalf("scratch run = %#v, want running", run)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("scratch root after tick = %v (%v), want directory", info, err)
	}
}

func TestTickUsesSelfRootAsTheWorkspaceDirectory(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "self")
	task := f.armRoute("self task", root, "self")

	f.runner().Tick(f.ctx)
	workspaces := f.herdr.Workspaces()
	if run := f.run(task.Number); run.State != model.RunRunning || len(workspaces) != 1 || workspaces[0].Cwd != root {
		t.Fatalf("run = %#v; workspaces = %#v, want root %q used directly", run, workspaces, root)
	}
}

func TestTickFailsAndBlocksWhenCreatingTheWorkspaceFails(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "self")
	f.herdr.Fail("CreateWorkspace", errors.New("create denied"))
	task := f.armRoute("workspace failure", root, "self")

	f.runner().Tick(f.ctx)
	run := f.run(task.Number)
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(f.herdr.Workspaces()) != 0 {
		t.Fatalf("run = %#v; task = %#v; workspaces = %#v, want failed blocked spawn without workspace", run, f.task(task.Number).Task, f.herdr.Workspaces())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "create denied")
}

func TestTickClosesOpenedPaneAndBlocksWhenStartingWorkerFails(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "self")
	f.herdr.Fail("Run", errors.New("run denied"))
	task := f.armRoute("worker failure", root, "self")

	f.runner().Tick(f.ctx)
	run := f.run(task.Number)
	workspaces := f.herdr.Workspaces()
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(workspaces) != 1 || run.Workspace != workspaces[0].ID || run.Pane != workspaces[0].Pane || !reflect.DeepEqual(f.herdr.Closed(), []string{run.Pane}) {
		t.Fatalf("run = %#v; task = %#v; closed panes = %#v, want failed blocked task and closed pane", run, f.task(task.Number).Task, f.herdr.Closed())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "run denied")
}

func TestTickStartsNoWorkerForARunKilledDuringItsSpawn(t *testing.T) {
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

			r.Tick(f.ctx)
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

func TestTickFailsTheRunWhenItsPaneCannotBeRecorded(t *testing.T) {
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

	f.runner().Tick(f.ctx)
	run := f.run(task.Number)
	workspaces := f.herdr.Workspaces()
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(workspaces) != 1 || workspaces[0].Command != "" || !reflect.DeepEqual(f.herdr.Closed(), []string{workspaces[0].Pane}) {
		t.Fatalf("run = %#v; workspaces = %#v; closed = %#v, want a failed run, no command, and the pane closed", run, workspaces, f.herdr.Closed())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "could not record the pane")
}

func TestTickReportsNoStartWhenItCannotConfirmTheRunAfterTheCommand(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "self")
	notice := filepath.Join(t.TempDir(), "notice")
	f.config.Notify.Command = []string{spawnNotifyScript(t, notice)}
	task := f.armRoute("unconfirmed start", root, "self")
	h := &hookedHerdr{Herdr: f.herdr}
	r := f.runnerWith(h)
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

	r.Tick(f.ctx)
	if _, err := db.Exec(`UPDATE runs SET started_ts = ? WHERE task = ?`, started, task.Number); err != nil {
		t.Fatalf("restore the start time: %v", err)
	}
	run := f.run(task.Number)
	workspaces := f.herdr.Workspaces()
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(workspaces) != 1 || !reflect.DeepEqual(f.herdr.Closed(), []string{workspaces[0].Pane}) {
		t.Fatalf("run = %#v; task = %#v; closed = %#v, want a failed run, a blocked task, and the pane closed", run, f.task(task.Number).Task, f.herdr.Closed())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "could not confirm the run")
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

func TestTickNamesAPaneItCouldNotCloseAfterAFailedSpawnAndClosesItLater(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root, "self")
	f.herdr.Fail("Run", errors.New("run denied"))
	f.herdr.Fail("ClosePane", errors.New("close denied"))
	task := f.armRoute("pane left open", root, "self")
	r := f.runner()

	r.Tick(f.ctx)
	run := f.run(task.Number)
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked {
		t.Fatalf("run = %#v; task = %#v, want a failed run and a blocked task", run, f.task(task.Number).Task)
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "pane "+run.Pane+" was left open")
	f.herdr.Fail("ClosePane", nil)
	r.Tick(f.ctx)
	if got := f.herdr.Closed(); !reflect.DeepEqual(got, []string{run.Pane}) {
		t.Fatalf("closed panes on the next tick = %#v, want the pane left open closed", got)
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
