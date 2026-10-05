package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/cli"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

type commandResult struct {
	exit   int
	stdout string
	stderr string
}

func runDesk(t *testing.T, getenv func(string) string, cwd string, stdin string, args ...string) commandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return commandResult{
		exit: cli.Run(context.Background(), args, cli.Env{
			Stdin:    strings.NewReader(stdin),
			Stdout:   &stdout,
			Stderr:   &stderr,
			Getenv:   getenv,
			Cwd:      cwd,
			StdinTTY: false,
		}),
		stdout: stdout.String(),
		stderr: stderr.String(),
	}
}

func runHomeDesk(t *testing.T, home *testutil.Home, args ...string) commandResult {
	t.Helper()
	return runDesk(t, home.Getenv(nil), t.TempDir(), "", args...)
}

func requireSuccess(t *testing.T, result commandResult) {
	t.Helper()
	if result.exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%q stderr=%q", result.exit, result.stdout, result.stderr)
	}
}

func requireRefusal(t *testing.T, result commandResult, command, code string, exit int) {
	t.Helper()
	if result.exit != exit {
		t.Errorf("exit = %d, want %d; stderr=%q", result.exit, exit, result.stderr)
	}
	if result.stdout != "" {
		t.Errorf("stdout = %q, want empty on refusal", result.stdout)
	}
	wantPrefix := "herdr-desk " + command + ": " + code + ": "
	if !strings.HasPrefix(result.stderr, wantPrefix) {
		t.Errorf("stderr = %q, want refusal beginning %q", result.stderr, wantPrefix)
	}
}

func addTask(t *testing.T, home *testutil.Home, title string, flags ...string) int {
	t.Helper()
	args := append([]string{"add", "-t", title}, flags...)
	result := runHomeDesk(t, home, args...)
	requireSuccess(t, result)
	var number int
	if _, err := fmt.Sscanf(strings.TrimSpace(result.stdout), "T%d", &number); err != nil {
		t.Fatalf("add stdout = %q, want T<number>: %v", result.stdout, err)
	}
	return number
}

func TestAddWritesTaskFieldsAndHasTextAndJSONShapes(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	result := runHomeDesk(t, home,
		"add", "-t", "ship the board", "-n", "before Friday", "--status", "ready",
		"--thread", "release", "--tag", "urgent", "--branch", "feature/board")
	requireSuccess(t, result)
	if result.stdout != "T1\n" {
		t.Fatalf("add stdout = %q, want %q", result.stdout, "T1\\n")
	}

	result = runHomeDesk(t, home, "show", "T1", "--json")
	requireSuccess(t, result)
	var detail store.TaskDetail
	if err := json.Unmarshal([]byte(result.stdout), &detail); err != nil {
		t.Fatalf("decode show JSON: %v; output=%q", err, result.stdout)
	}
	if detail.Task.Title != "ship the board" || detail.Task.Notes != "before Friday" || detail.Task.Status != model.StatusReady || detail.Task.Thread != "release" {
		t.Errorf("stored task = %#v, want title, notes, ready status, and thread from add", detail.Task)
	}
	if len(detail.History) != 1 || !contains(detail.History[0].Tags, "urgent") || !contains(detail.History[0].Tags, "branch:feature/board") {
		t.Errorf("add history = %#v, want both explicit and branch tags", detail.History)
	}

	result = runHomeDesk(t, home, "add", "-t", "json task", "--json")
	requireSuccess(t, result)
	var task model.Task
	if err := json.Unmarshal([]byte(result.stdout), &task); err != nil {
		t.Fatalf("decode add JSON: %v; output=%q", err, result.stdout)
	}
	if task.Number != 2 || task.Title != "json task" {
		t.Errorf("add JSON task = %#v, want T2 json task", task)
	}
}

func TestTaskCommandsReportStableRefusalCodes(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	addTask(t, home, "task without steps")
	tests := []struct {
		name    string
		args    []string
		command string
		code    string
		exit    int
	}{
		{"empty title", []string{"add", "-t", ""}, "add", model.CodeEmptyTitle, 1},
		{"unknown project", []string{"add", "-t", "task", "-p", "not-known"}, "add", model.CodeUnknownProject, 1},
		{"unknown task", []string{"show", "T99"}, "show", model.CodeUnknownTask, 1},
		{"unknown status", []string{"add", "-t", "task", "--status", "later"}, "add", model.CodeBadInput, 2},
		{"unknown step", []string{"steps", "T1", "toggle", "s1"}, "steps", model.CodeUnknownStep, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireRefusal(t, runHomeDesk(t, home, test.args...), test.command, test.code, test.exit)
		})
	}
}

func TestListAndShowRenderTheirContractShapes(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	addTask(t, home, "open task")
	addTask(t, home, "ready task", "--status", "ready", "--thread", "agent")
	addTask(t, home, "done task", "--status", "done")

	result := runHomeDesk(t, home, "list", "--ready")
	requireSuccess(t, result)
	if result.stdout != "T2  ready  ready task  #agent\n" {
		t.Errorf("ready list = %q, want its one formatted task line", result.stdout)
	}

	result = runHomeDesk(t, home, "list", "--done", "--json")
	requireSuccess(t, result)
	var list api.TaskList
	if err := json.Unmarshal([]byte(result.stdout), &list); err != nil {
		t.Fatalf("decode list JSON: %v; output=%q", err, result.stdout)
	}
	if list.Offline || len(list.Tasks) != 1 || list.Tasks[0].Title != "done task" {
		t.Errorf("done list JSON = %#v, want one online done task", list)
	}

	result = runHomeDesk(t, home, "show", "2")
	requireSuccess(t, result)
	if !strings.Contains(result.stdout, "T2") || !strings.Contains(result.stdout, "ready task") {
		t.Errorf("show output = %q, want task number and title", result.stdout)
	}
}

func TestListFiltersProjectsAndDeskTasksAndShowsTaskDetails(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	addTask(t, home, "desk task", "--desk")
	addTask(t, home, "project task", "-p", "/projects/alpha", "-n", "details")

	result := runHomeDesk(t, home, "list", "-p", "alpha")
	requireSuccess(t, result)
	if result.stdout != "T2  open  project task  alpha\n" {
		t.Errorf("project list = %q, want its absolute project's task", result.stdout)
	}
	result = runHomeDesk(t, home, "list", "--desk", "--json")
	requireSuccess(t, result)
	var list api.TaskList
	if err := json.Unmarshal([]byte(result.stdout), &list); err != nil {
		t.Fatalf("decode herdr-desk list: %v", err)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].Title != "desk task" {
		t.Errorf("herdr-desk list = %#v, want only the task without a project", list.Tasks)
	}

	requireSuccess(t, runHomeDesk(t, home, "set", "T2", "--root", "/repo", "--isolation", "worktree", "--model", "fast", "--archive"))
	requireSuccess(t, runHomeDesk(t, home, "steps", "T2", "add", "verify output"))
	result = runHomeDesk(t, home, "show", "T2")
	requireSuccess(t, result)
	for _, text := range []string{"project task", "alpha", "project: /projects/alpha", "root: /repo", "isolation: worktree", "model: fast", "archived", "details", "steps:", "s1 [ ] verify output", "history:"} {
		if !strings.Contains(result.stdout, text) {
			t.Errorf("show output missing %q: %q", text, result.stdout)
		}
	}
}

func TestSetAndEditUpdateTaskAndRenderExpectedOutput(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	addTask(t, home, "before")

	result := runHomeDesk(t, home, "set", "t1", "review", "--thread", "reviewer", "--root", "/repo", "--isolation", "worktree", "--model", "fast")
	requireSuccess(t, result)
	if result.stdout != "T1 review\n" {
		t.Errorf("set stdout = %q, want %q", result.stdout, "T1 review\\n")
	}
	result = runHomeDesk(t, home, "edit", "1", "--title", "after", "--notes", "details")
	requireSuccess(t, result)
	if result.stdout != "T1\n" {
		t.Errorf("edit stdout = %q, want %q", result.stdout, "T1\\n")
	}

	result = runHomeDesk(t, home, "show", "T1", "--json")
	requireSuccess(t, result)
	var detail store.TaskDetail
	if err := json.Unmarshal([]byte(result.stdout), &detail); err != nil {
		t.Fatalf("decode edited task: %v", err)
	}
	if detail.Task.Title != "after" || detail.Task.Notes != "details" || detail.Task.Status != model.StatusReview || detail.Task.Thread != "reviewer" || detail.Task.Root != "/repo" || detail.Task.Isolation != "worktree" || detail.Task.Model != "fast" {
		t.Errorf("edited task = %#v, want all set and edit fields", detail.Task)
	}
}

func TestSetMergedUsesConfiguredOnMergedStatusAndArchiveFlags(t *testing.T) {
	cfg := config.Default()
	cfg.Runner.OnMerged = "done"
	home := testutil.StartHome(t, testutil.HomeOptions{Config: cfg})
	addTask(t, home, "merge me")

	requireSuccess(t, runHomeDesk(t, home, "set", "T1", "review", "--merged", "--archive"))
	result := runHomeDesk(t, home, "show", "T1", "--json")
	requireSuccess(t, result)
	var detail store.TaskDetail
	if err := json.Unmarshal([]byte(result.stdout), &detail); err != nil {
		t.Fatalf("decode merged task: %v", err)
	}
	if detail.Task.Status != model.StatusDone || !detail.Task.Archived {
		t.Errorf("merged archived task = %#v, want done and archived", detail.Task)
	}
	result = runHomeDesk(t, home, "list", "--archived")
	requireSuccess(t, result)
	if result.stdout != "T1  done  merge me\n" {
		t.Errorf("archived list = %q, want the archived merged task", result.stdout)
	}
	requireSuccess(t, runHomeDesk(t, home, "set", "T1", "--unarchive"))
	result = runHomeDesk(t, home, "show", "T1", "--json")
	if err := json.Unmarshal([]byte(result.stdout), &detail); err != nil {
		t.Fatalf("decode unarchived task: %v", err)
	}
	if detail.Task.Archived {
		t.Error("--unarchive left the task archived")
	}
}

func TestStepsKeepShortIDsStableAndNeverReuseThem(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	addTask(t, home, "checklist")
	for _, args := range [][]string{
		{"steps", "T1", "add", "first"},
		{"steps", "1", "add", "second"},
		{"steps", "T1", "toggle", "s1"},
		{"steps", "T1", "rename", "s2", "renamed"},
		{"steps", "T1", "remove", "s1"},
		{"steps", "T1", "add", "third"},
	} {
		requireSuccess(t, runHomeDesk(t, home, args...))
	}
	result := runHomeDesk(t, home, "show", "T1", "--json")
	requireSuccess(t, result)
	var detail store.TaskDetail
	if err := json.Unmarshal([]byte(result.stdout), &detail); err != nil {
		t.Fatalf("decode task steps: %v", err)
	}
	if len(detail.Task.Steps) != 2 || detail.Task.Steps[0].ShortID != "s2" || detail.Task.Steps[0].Text != "renamed" || detail.Task.Steps[0].Done || detail.Task.Steps[1].ShortID != "s3" || detail.Task.Steps[1].Text != "third" {
		t.Errorf("steps = %#v, want remaining s2 and newly allocated s3", detail.Task.Steps)
	}
}

func TestCaptureParsesTitleThreadAndBareProjectAndIgnoresEmptyInput(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	addTask(t, home, "seed the known project", "-p", "/projects/desk")
	result := runDesk(t, home.Getenv(nil), t.TempDir(), "fix release #ops @desk\n", "capture")
	requireSuccess(t, result)
	if result.stdout != "T2\n" {
		t.Errorf("capture stdout = %q, want T2", result.stdout)
	}
	result = runHomeDesk(t, home, "show", "T2", "--json")
	requireSuccess(t, result)
	var detail store.TaskDetail
	if err := json.Unmarshal([]byte(result.stdout), &detail); err != nil {
		t.Fatalf("decode captured task: %v", err)
	}
	if detail.Task.Title != "fix release" || detail.Task.Thread != "ops" || detail.Task.Project != "/projects/desk" {
		t.Errorf("captured task = %#v, want title, thread, and resolved bare project", detail.Task)
	}
	result = runDesk(t, home.Getenv(nil), t.TempDir(), "\n", "capture")
	requireSuccess(t, result)
	if result.stdout != "" {
		t.Errorf("empty capture stdout = %q, want no task", result.stdout)
	}
}

func TestBareDeskGroupsLiveTasksInBoardOrder(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	addTask(t, home, "review", "--status", "review")
	addTask(t, home, "blocked", "--status", "blocked")
	addTask(t, home, "started", "--status", "started")
	addTask(t, home, "ready", "--status", "ready")
	addTask(t, home, "open")

	result := runHomeDesk(t, home)
	requireSuccess(t, result)
	for _, text := range []string{"herdr-desk · home · runner off", "NEEDS YOU", "IN MOTION", "ON DECK"} {
		if !strings.Contains(result.stdout, text) {
			t.Errorf("board output missing %q: %q", text, result.stdout)
		}
	}
	assertOrder(t, result.stdout, "blocked", "review", "started", "ready", "open")
	result = runHomeDesk(t, home, "--json")
	requireSuccess(t, result)
	var list api.TaskList
	if err := json.Unmarshal([]byte(result.stdout), &list); err != nil {
		t.Fatalf("decode board JSON: %v", err)
	}
	if list.Offline || len(list.Tasks) != 5 {
		t.Errorf("board JSON = %#v, want five online live tasks", list)
	}

	cfg := config.Default()
	cfg.Runner.Enabled = true
	runnerHome := testutil.StartHome(t, testutil.HomeOptions{Config: cfg})
	result = runHomeDesk(t, runnerHome)
	requireSuccess(t, result)
	if !strings.HasPrefix(result.stdout, "herdr-desk · home · runner on\n") {
		t.Errorf("runner board = %q, want runner-on header", result.stdout)
	}
}

func TestAddDefaultsProjectToTheMainCheckoutWhenCwdIsAWorktree(t *testing.T) {
	main, worktree := newGitWorktree(t)
	home := testutil.StartHome(t, testutil.HomeOptions{})
	result := runDesk(t, home.Getenv(nil), worktree, "", "add", "-t", "from worktree", "--json")
	requireSuccess(t, result)
	var task model.Task
	if err := json.Unmarshal([]byte(result.stdout), &task); err != nil {
		t.Fatalf("decode add JSON: %v", err)
	}
	want, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatalf("evaluate main checkout: %v", err)
	}
	if task.Project != want {
		t.Errorf("task project = %q, want main checkout %q", task.Project, want)
	}
	result = runDesk(t, home.Getenv(nil), t.TempDir(), "", "add", "-t", "explicit worktree", "-p", worktree, "--json")
	requireSuccess(t, result)
	if err := json.Unmarshal([]byte(result.stdout), &task); err != nil {
		t.Fatalf("decode explicit project JSON: %v", err)
	}
	if task.Project != want {
		t.Errorf("explicit project = %q, want main checkout %q", task.Project, want)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertOrder(t *testing.T, text string, values ...string) {
	t.Helper()
	last := -1
	for _, value := range values {
		at := strings.Index(text, value)
		if at < 0 {
			t.Errorf("board output missing task %q: %q", value, text)
			continue
		}
		if at < last {
			t.Errorf("board output orders %q before %q: %q", value, values[0], text)
		}
		last = at
	}
}

func newGitWorktree(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	main := filepath.Join(root, "main")
	worktree := filepath.Join(root, "worktree")
	runGit(t, root, "init", "-b", "main", main)
	runGit(t, main, "config", "user.email", "test@example.com")
	runGit(t, main, "config", "user.name", "desk test")
	if err := os.WriteFile(filepath.Join(main, "README"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write repository file: %v", err)
	}
	runGit(t, main, "add", "README")
	runGit(t, main, "commit", "-m", "initial")
	runGit(t, main, "worktree", "add", "-b", "feature", worktree)
	return main, worktree
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
