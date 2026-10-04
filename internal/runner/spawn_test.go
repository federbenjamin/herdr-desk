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
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/herdr/herdrtest"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/runner"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
)

type spawnFixture struct {
	t      *testing.T
	ctx    context.Context
	paths  config.Paths
	store  *store.Store
	config config.Config
	herdr  *herdrtest.Herdr
	now    time.Time
	exe    string
}

func newSpawnFixture(t *testing.T, root, isolation string) *spawnFixture {
	t.Helper()
	machine := testutil.NewMachine(t)
	clock := time.Date(2026, time.October, 4, 15, 0, 0, 0, time.Local)
	st, err := store.Open(machine.Paths.DB(), store.Options{Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Default()
	cfg.Runner.Enabled = true
	cfg.Runner.Cap = 3
	cfg.Runner.MaxRunsPerDay = 20
	cfg.Roots = []config.Root{{Path: root, About: "test root", Isolation: isolation}}
	cfg.Agent.Router = []string{spawnRouterScript(t)}
	cfg.Agent.Models = []string{"model-a"}
	return &spawnFixture{
		t:      t,
		ctx:    context.Background(),
		paths:  machine.Paths,
		store:  st,
		config: cfg,
		herdr:  herdrtest.NewHerdr(),
		now:    clock,
		exe:    "/opt/desk/bin/desk",
	}
}

func (f *spawnFixture) runner() *runner.Runner {
	f.t.Helper()
	return runner.New(runner.Options{
		Store:         f.store,
		Config:        f.config,
		Paths:         f.paths,
		Herdr:         f.herdr,
		Exe:           f.exe,
		Now:           func() time.Time { return f.now },
		RouterTimeout: 100 * time.Millisecond,
		KillGrace:     10 * time.Millisecond,
		Logf:          f.t.Logf,
	})
}

func (f *spawnFixture) arm(title, root, isolation string) model.Task {
	f.t.Helper()
	task, err := f.store.AddTask(f.ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: title, Thread: "agent"}})
	if err != nil {
		f.t.Fatalf("add task: %v", err)
	}
	ready := model.StatusReady
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{
		Status:    &ready,
		Root:      &root,
		Isolation: &isolation,
		Model:     spawnString("model-a"),
	}); err != nil {
		f.t.Fatalf("arm T%d: %v", task.Number, err)
	}
	return task
}

func (f *spawnFixture) runFor(task int) model.Run {
	f.t.Helper()
	runs, err := f.store.ListRuns(f.ctx)
	if err != nil {
		f.t.Fatalf("list runs: %v", err)
	}
	for _, run := range runs {
		if run.Task == task {
			return run
		}
	}
	f.t.Fatalf("no run for T%d in %#v", task, runs)
	return model.Run{}
}

func (f *spawnFixture) task(task int) store.TaskDetail {
	f.t.Helper()
	detail, err := f.store.GetTask(f.ctx, task)
	if err != nil {
		f.t.Fatalf("get T%d: %v", task, err)
	}
	return detail
}

func spawnString(s string) *string { return &s }

func TestTickStartsInPlaceRunWithSessionWorkspaceEnvironmentNoteAndNotification(t *testing.T) {
	root := t.TempDir()
	f := newSpawnFixture(t, root, "in-place")
	notice := filepath.Join(t.TempDir(), "notice")
	f.config.Notify.Command = []string{spawnNotifyScript(t, notice)}
	task := f.arm("in place", root, "in-place")

	f.runner().Tick(f.ctx)
	run := f.runFor(task.Number)
	if run.State != model.RunRunning || run.Session == "" || run.Workspace == "" || run.Pane == "" {
		t.Fatalf("run = %#v, want running run with session, workspace, and pane", run)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(run.Session) {
		t.Fatalf("run session = %q, want lower-case UUID", run.Session)
	}
	other := newSpawnFixture(t, t.TempDir(), "self")
	otherTask := other.arm("another session", other.config.Roots[0].Path, "self")
	other.runner().Tick(other.ctx)
	if otherRun := other.runFor(otherTask.Number); otherRun.Session == run.Session {
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
			f := newSpawnFixture(t, root, "self")
			f.exe = tc.exe
			task := f.arm("quote command", root, "self")

			f.runner().Tick(f.ctx)
			run := f.runFor(task.Number)
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
	f := newSpawnFixture(t, root, "worktree")
	task := f.arm("Ship the Test", root, "worktree")
	worktree := filepath.Join(parent, "repo-T"+strconv.Itoa(task.Number))
	branch := "desk/T" + strconv.Itoa(task.Number) + "-ship-the-test"
	r := f.runner()

	r.Tick(f.ctx)
	first := f.runFor(task.Number)
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
	f := newSpawnFixture(t, root, "worktree")
	task := f.arm("!!!", root, "worktree")
	branch := "desk/T" + strconv.Itoa(task.Number)
	spawnGit(t, root, "branch", branch)

	f.runner().Tick(f.ctx)
	run := f.runFor(task.Number)
	worktree := filepath.Join(parent, "repo-T"+strconv.Itoa(task.Number))
	if run.State != model.RunRunning || f.herdr.Workspaces()[0].Cwd != worktree || spawnGitBranch(t, worktree) != branch {
		t.Fatalf("run = %#v; workspaces = %#v; branch = %q, want existing %q checked out at %q", run, f.herdr.Workspaces(), spawnGitBranch(t, worktree), branch, worktree)
	}
}

func TestTickBlocksTaskWhenWorktreeRootIsNotAGitRepository(t *testing.T) {
	root := t.TempDir()
	f := newSpawnFixture(t, root, "worktree")
	task := f.arm("not a repository", root, "worktree")

	f.runner().Tick(f.ctx)
	run := f.runFor(task.Number)
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
	f := newSpawnFixture(t, root, "in-place")
	f.paths = machine.Paths
	f.config.Roots = []config.Root{{Path: root, Isolation: "in-place"}}
	task := f.arm("scratch task", root, "in-place")

	f.runner().Tick(f.ctx)
	if run := f.runFor(task.Number); run.State != model.RunRunning {
		t.Fatalf("scratch run = %#v, want running", run)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("scratch root after tick = %v (%v), want directory", info, err)
	}
}

func TestTickUsesSelfRootAsTheWorkspaceDirectory(t *testing.T) {
	root := t.TempDir()
	f := newSpawnFixture(t, root, "self")
	task := f.arm("self task", root, "self")

	f.runner().Tick(f.ctx)
	workspaces := f.herdr.Workspaces()
	if run := f.runFor(task.Number); run.State != model.RunRunning || len(workspaces) != 1 || workspaces[0].Cwd != root {
		t.Fatalf("run = %#v; workspaces = %#v, want root %q used directly", run, workspaces, root)
	}
}

func TestTickFailsAndBlocksWhenCreatingTheWorkspaceFails(t *testing.T) {
	root := t.TempDir()
	f := newSpawnFixture(t, root, "self")
	f.herdr.Fail("CreateWorkspace", errors.New("create denied"))
	task := f.arm("workspace failure", root, "self")

	f.runner().Tick(f.ctx)
	run := f.runFor(task.Number)
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(f.herdr.Workspaces()) != 0 {
		t.Fatalf("run = %#v; task = %#v; workspaces = %#v, want failed blocked spawn without workspace", run, f.task(task.Number).Task, f.herdr.Workspaces())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "create denied")
}

func TestTickClosesOpenedPaneAndBlocksWhenStartingWorkerFails(t *testing.T) {
	root := t.TempDir()
	f := newSpawnFixture(t, root, "self")
	f.herdr.Fail("Run", errors.New("run denied"))
	task := f.arm("worker failure", root, "self")

	f.runner().Tick(f.ctx)
	run := f.runFor(task.Number)
	workspaces := f.herdr.Workspaces()
	if run.State != model.RunFailed || f.task(task.Number).Task.Status != model.StatusBlocked || len(workspaces) != 1 || run.Workspace != workspaces[0].ID || run.Pane != workspaces[0].Pane || !reflect.DeepEqual(f.herdr.Closed(), []string{run.Pane}) {
		t.Fatalf("run = %#v; task = %#v; closed panes = %#v, want failed blocked task and closed pane", run, f.task(task.Number).Task, f.herdr.Closed())
	}
	spawnAssertFailureNote(t, f.task(task.Number).History, run.ID, "run denied")
}

func spawnNotifyScript(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notify")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'notified\\n' >> "+fmt.Sprintf("%q", output)+"\n"), 0o700); err != nil {
		t.Fatalf("write notify script: %v", err)
	}
	return path
}

func spawnRouterScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "router")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write router script: %v", err)
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
