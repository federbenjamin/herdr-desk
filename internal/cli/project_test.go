package cli_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestAddProjectIgnoresARepositoryTheEnvironmentNames(t *testing.T) {
	main, _ := newGitWorktree(t)
	other, _ := newGitWorktree(t)
	home := testutil.StartHome(t, testutil.HomeOptions{})
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)

	for _, args := range [][]string{
		{"add", "-t", "from cwd", "--json"},
		{"add", "-t", "from -p", "-p", main, "--json"},
	} {
		result := runDesk(t, home.Getenv(nil), main, "", args...)
		requireSuccess(t, result)
		var task model.Task
		if err := json.Unmarshal([]byte(result.stdout), &task); err != nil {
			t.Fatalf("decode %v: %v", args, err)
		}
		want, _ := filepath.EvalSymlinks(main)
		if task.Project != want {
			t.Errorf("%v: project = %q, want the repository of the directory, %q", args, task.Project, want)
		}
	}
}

func TestAddStopsWhenGitFailsAndKeepsNoProjectOnlyOutsideARepository(t *testing.T) {
	main, _ := newGitWorktree(t)
	home := testutil.StartHome(t, testutil.HomeOptions{})

	plain := runDesk(t, home.Getenv(nil), t.TempDir(), "", "add", "-t", "outside any repository", "--json")
	requireSuccess(t, plain)
	var task model.Task
	if err := json.Unmarshal([]byte(plain.stdout), &task); err != nil || task.Project != "" {
		t.Fatalf("add outside a repository = %q (%v), want no project", plain.stdout, err)
	}

	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{
		{"add", "-t", "from cwd without git"},
		{"add", "-t", "from -p without git", "-p", main},
	} {
		result := runDesk(t, home.Getenv(nil), main, "", args...)
		if result.exit != 3 || result.stdout != "" {
			t.Errorf("%v = (%d, %q, %q), want exit 3 and no task", args, result.exit, result.stdout, result.stderr)
		}
		if !strings.Contains(result.stderr, "git") || !strings.Contains(result.stderr, "--desk") {
			t.Errorf("%v stderr = %q, want the git failure and the way around it", args, result.stderr)
		}
	}
	tasks, err := home.Client().ListTasks(context.Background(), store.Filter{All: true})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks.Tasks) != 1 {
		t.Fatalf("tasks = %#v, want only the one added outside a repository", tasks.Tasks)
	}
}

func TestListProjectNarrowsEveryFilterAndResolvesNamesLikeAdd(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	addTask(t, home, "alpha open", "-p", "/projects/alpha")
	addTask(t, home, "beta open", "-p", "/projects/beta")
	addTask(t, home, "alpha done", "-p", "/projects/alpha", "--status", "done")
	addTask(t, home, "desk only", "--desk")
	addTask(t, home, "gamma done", "-p", "/projects/gamma", "--status", "done")

	result := runHomeDesk(t, home, "list", "--all", "-p", "alpha")
	requireSuccess(t, result)
	if want := "T1  open  alpha open  alpha\nT3  done  alpha done  alpha\n"; result.stdout != want {
		t.Errorf("list --all -p alpha = %q, want %q", result.stdout, want)
	}
	result = runHomeDesk(t, home, "list", "--all", "--desk")
	requireSuccess(t, result)
	if want := "T4  open  desk only\n"; result.stdout != want {
		t.Errorf("list --all --desk = %q, want %q", result.stdout, want)
	}

	requireRefusal(t, runHomeDesk(t, home, "list", "-p", "nosuchname"), "list", model.CodeUnknownProject, 1)
	requireRefusal(t, runHomeDesk(t, home, "add", "-t", "x", "-p", "nosuchname"), "add", model.CodeUnknownProject, 1)

	result = runHomeDesk(t, home, "list", "-p", "gamma")
	requireSuccess(t, result)
	if result.stdout != "" {
		t.Errorf("list -p gamma = %q, want no live task of a project only done tasks name", result.stdout)
	}
}

func TestOfflineListProjectResolvesNamesOverTheWholeSnapshotAndNeverRefusesWhatItCannotSee(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	getenv := testutil.NewClientMachine(t, home).Getenv(nil)
	cwd := t.TempDir()
	for _, args := range [][]string{
		{"add", "-t", "alpha ready", "-p", "/projects/alpha", "--status", "ready"},
		{"add", "-t", "beta open", "-p", "/projects/beta"},
		{"add", "-t", "gamma done", "-p", "/projects/gamma", "--status", "done"},
		{"add", "-t", "delta open", "-p", "/projects/delta"},
		{"add", "-t", "delta ready", "-p", "/other/delta", "--status", "ready"},
		{"list"},
	} {
		requireSuccess(t, runDesk(t, getenv, cwd, "", args...))
	}
	home.Stop()

	requireRefusal(t, runDesk(t, getenv, cwd, "", "list", "--ready", "-p", "delta"), "list", model.CodeUnknownProject, 1)
	for _, args := range [][]string{{"list", "-p", "projects/alpha"}, {"list", "--ready", "-p", "no/such"}} {
		requireRefusal(t, runDesk(t, getenv, cwd, "", args...), "list", model.CodeUnknownProject, 1)
	}

	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"list", "--ready", "-p", "alpha"}, "T1  ready  alpha ready  alpha\n"},
		{[]string{"list", "--ready", "-p", "beta"}, ""},
		{[]string{"list", "--open", "-p", "beta"}, "T2  open  beta open  beta\n"},
		{[]string{"list", "-p", "gamma"}, ""},
	} {
		result := runDesk(t, getenv, cwd, "", test.args...)
		if result.exit != 0 || result.stdout != test.want || !strings.Contains(result.stderr, "showing the snapshot") {
			t.Errorf("offline %v = (%d, %q, %q), want exit 0, %q, and the snapshot notice", test.args, result.exit, result.stdout, result.stderr, test.want)
		}
	}
}
