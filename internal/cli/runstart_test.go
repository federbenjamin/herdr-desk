package cli_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

// startOnlyHome is a home with the runner on and no ticker, so only the commands under test start or free runs.
func startOnlyHome(t *testing.T, mutate func(*config.Config)) (*testutil.Home, string) {
	t.Helper()
	root := t.TempDir()
	testutil.FakeHerdr(t)
	cfg := config.Default()
	cfg.Runner.Enabled = true
	cfg.Runner.Cap = 2
	cfg.Roots = []config.Root{{Path: root, About: "test root", Isolation: "in-place"}}
	cfg.Agent.Models = []string{"test-model"}
	if mutate != nil {
		mutate(&cfg)
	}
	return testutil.StartHome(t, testutil.HomeOptions{Config: cfg}), root
}

func runStartLine(id int64, task int, state, root, isolation, mdl string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`^run %d  T%d  %s  %s  %s  %s\n$`, id, task, state, regexp.QuoteMeta(root), isolation, mdl))
}

func TestRunStartPrintsTheRunsLineAndExitsZero(t *testing.T) {
	home, root := startOnlyHome(t, nil)
	task := addTask(t, home, "start me")
	result := runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", task), "--root", root, "--isolation", "in-place", "--model", "test-model")
	requireSuccess(t, result)
	if !regexp.MustCompile(fmt.Sprintf(`^run \d+  T%d  running  %s  in-place  test-model\n$`, task, regexp.QuoteMeta(root))).MatchString(result.stdout) {
		t.Fatalf("run start stdout = %q, want the runs line of a running run", result.stdout)
	}
}

// With no flags the route comes from the task, the root, and the defaults; the line shows the resolved route.
func TestRunStartWithNoFlagsPrintsTheResolvedRoute(t *testing.T) {
	home, root := startOnlyHome(t, nil)
	task := addTask(t, home, "defaults")
	requireSuccess(t, runHomeDesk(t, home, "set", fmt.Sprintf("T%d", task), "--root", root))
	result := runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", task))
	requireSuccess(t, result)
	if !regexp.MustCompile(fmt.Sprintf(`^run \d+  T%d  running  %s  in-place  test-model\n$`, task, regexp.QuoteMeta(root))).MatchString(result.stdout) {
		t.Fatalf("run start stdout = %q, want the root's isolation and the first model", result.stdout)
	}
}

func TestRunStartOfALiveRunPrintsThatRunAndExitsZero(t *testing.T) {
	home, root := startOnlyHome(t, nil)
	task := addTask(t, home, "twice")
	args := []string{"run", "start", fmt.Sprintf("T%d", task), "--root", root, "--json"}
	var first, second model.Run
	for i, into := range []*model.Run{&first, &second} {
		result := runHomeDesk(t, home, args...)
		if result.exit != 0 {
			t.Fatalf("start #%d exit = %d, stderr = %q; want 0 also for a live run", i+1, result.exit, result.stderr)
		}
		if err := json.Unmarshal([]byte(result.stdout), into); err != nil {
			t.Fatalf("start #%d stdout = %q: %v", i+1, result.stdout, err)
		}
	}
	if first.ID == 0 || first.ID != second.ID || second.State != model.RunRunning {
		t.Fatalf("runs = %#v then %#v, want the same running run both times", first, second)
	}
	all := runHomeDesk(t, home, "runs", "--all", "--json")
	var runs []model.Run
	if err := json.Unmarshal([]byte(all.stdout), &runs); err != nil || len(runs) != 1 {
		t.Fatalf("runs --all = %q (%v), want exactly one run", all.stdout, err)
	}
}

// A spawn that fails in the call is not a start: the coordinator reads the exit code, so it is 1, with the reason.
func TestRunStartWhoseSpawnFailsExitsOneWithTheReason(t *testing.T) {
	home, root := startOnlyHome(t, nil)
	task := addTask(t, home, "no git here")
	for _, json := range []bool{false, true} {
		args := []string{"run", "start", fmt.Sprintf("T%d", task), "--root", root, "--isolation", "worktree"}
		if json {
			args = append(args, "--json")
		}
		result := runHomeDesk(t, home, args...)
		if result.exit != 1 || !strings.Contains(result.stdout, "failed") || !regexp.MustCompile(`^herdr-desk run start: run-failed: run \d+ failed: spawn: \S`).MatchString(result.stderr) {
			t.Fatalf("run start (json %t) = exit %d, stdout %q, stderr %q; want exit 1, the failed run, and its reason", json, result.exit, result.stdout, result.stderr)
		}
	}
}

func TestRunStartJSONPrintsTheRunObject(t *testing.T) {
	home, root := startOnlyHome(t, nil)
	task := addTask(t, home, "json")
	result := runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", task), "--root", root, "--json")
	requireSuccess(t, result)
	var run model.Run
	if err := json.Unmarshal([]byte(result.stdout), &run); err != nil {
		t.Fatalf("stdout = %q: %v", result.stdout, err)
	}
	if run.Task != task || run.State != model.RunRunning || run.Root != root || run.Isolation != "in-place" || run.Model != "test-model" {
		t.Fatalf("run = %#v, want a running run of the task on the resolved route", run)
	}
}

// Cap 2 and one in-place root: the second run has to wait for the root, and says so with exit 0.
func TestRunStartQueuesARunWhoseRootIsBusyAsWaiting(t *testing.T) {
	home, root := startOnlyHome(t, nil)
	first := addTask(t, home, "first")
	second := addTask(t, home, "second")
	requireSuccess(t, runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", first), "--root", root))
	result := runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", second), "--root", root)
	requireSuccess(t, result)
	if !regexp.MustCompile(fmt.Sprintf(`^run \d+  T%d  waiting  `, second)).MatchString(result.stdout) {
		t.Fatalf("second start = %q, want a waiting run", result.stdout)
	}
}

// tasks.set calls AfterSet: finishing the first run's task frees the root and the waiting run is spawned with no ticker.
func TestSetThatEndsARunStartsTheRunThatWaitedForIt(t *testing.T) {
	home, root := startOnlyHome(t, nil)
	first := addTask(t, home, "first")
	second := addTask(t, home, "second")
	requireSuccess(t, runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", first), "--root", root))
	requireSuccess(t, runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", second), "--root", root))

	requireSuccess(t, runHomeDesk(t, home, "set", fmt.Sprintf("T%d", first), "done"))

	all := runHomeDesk(t, home, "runs", "--all", "--json")
	requireSuccess(t, all)
	var runs []model.Run
	if err := json.Unmarshal([]byte(all.stdout), &runs); err != nil || len(runs) != 2 {
		t.Fatalf("runs --all = %q (%v), want two runs", all.stdout, err)
	}
	for _, r := range runs {
		switch r.Task {
		case first:
			if r.State != model.RunEnded {
				t.Errorf("first run = %#v, want ended", r)
			}
		case second:
			if r.State != model.RunRunning || r.Pane == "" {
				t.Errorf("second run = %#v, want running with a pane", r)
			}
		}
	}
}

func TestRunStartRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.Config)
		args   func(task int, root string) []string
		extra  map[string]string
		code   string
		exit   int // bad-input is a usage error (2); the others are refusals (1)
	}{
		{"runner off", func(c *config.Config) { c.Runner.Enabled = false }, nil, nil, model.CodeRunnerOff, 1},
		{"a worker session may not start", nil, nil, map[string]string{"DESK_SESSION": "worker-session"}, model.CodeNotAllowed, 1},
		{"an unlisted root", nil, func(task int, _ string) []string {
			return []string{"run", "start", fmt.Sprintf("T%d", task), "--root", "/not/a/root"}
		}, nil, model.CodeBadInput, 2},
		{"an unknown model", nil, func(task int, root string) []string {
			return []string{"run", "start", fmt.Sprintf("T%d", task), "--root", root, "--model", "nope"}
		}, nil, model.CodeBadInput, 2},
		{"a bad isolation", nil, func(task int, root string) []string {
			return []string{"run", "start", fmt.Sprintf("T%d", task), "--root", root, "--isolation", "teleport"}
		}, nil, model.CodeBadInput, 2},
		{"an unknown task", nil, func(int, string) []string { return []string{"run", "start", "T99"} }, nil, model.CodeUnknownTask, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, root := startOnlyHome(t, tc.mutate)
			task := addTask(t, home, "refused")
			args := []string{"run", "start", fmt.Sprintf("T%d", task), "--root", root}
			if tc.args != nil {
				args = tc.args(task, root)
			}
			result := runDeskWithEnv(t, home.Machine, t.TempDir(), args, "", tc.extra)
			requireRefusal(t, result, "run start", tc.code, tc.exit)
			if all := runHomeDesk(t, home, "runs", "--all", "--json"); strings.TrimSpace(all.stdout) != "[]" {
				t.Fatalf("runs after a refused start = %q, want none", all.stdout)
			}
		})
	}
}

func TestRunStartRefusesOnceTodaysRunsReachTheDailyCap(t *testing.T) {
	home, root := startOnlyHome(t, func(c *config.Config) { c.Runner.MaxRunsPerDay = 1 })
	first := addTask(t, home, "first")
	second := addTask(t, home, "second")
	requireSuccess(t, runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", first), "--root", root))
	requireRefusal(t, runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", second), "--root", root), "run start", model.CodeCapReached, 1)
}

func TestRunStartRejectsBadUsageWithExitTwo(t *testing.T) {
	home, _ := startOnlyHome(t, nil)
	for _, args := range [][]string{{"run", "start"}, {"run", "start", "not-a-task"}, {"run", "start", "T1", "T2"}} {
		if result := runHomeDesk(t, home, args...); result.exit != 2 {
			t.Errorf("%v exit = %d, want 2; stderr = %q", args, result.exit, result.stderr)
		}
	}
}

func TestRunStartReachesTheHomeFromAClientMachine(t *testing.T) {
	home, root := startOnlyHome(t, nil)
	task := addTask(t, home, "from a client")
	client := testutil.NewClientMachine(t, home)
	result := runDeskWithEnv(t, client, t.TempDir(), []string{"run", "start", fmt.Sprintf("T%d", task), "--root", root}, "", nil)
	requireSuccess(t, result)
	if !regexp.MustCompile(fmt.Sprintf(`^run \d+  T%d  running  `, task)).MatchString(result.stdout) {
		t.Fatalf("client run start = %q, want a running run", result.stdout)
	}
}

// add --start adds the task and starts its run on the route the flags give, printing what add and run start print.
func TestAddStartAddsTheTaskAndStartsItsRun(t *testing.T) {
	const first = "Read {task_file} and do it."
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json %t", asJSON), func(t *testing.T) {
			home, root := startOnlyHome(t, nil)
			args := []string{"add", "-t", "started", "-n", "the prompt", "--start",
				"--root", root, "--isolation", "in-place", "--model", "test-model", "--first-message", first}
			if !asJSON {
				result := runHomeDesk(t, home, args...)
				requireSuccess(t, result)
				if !regexp.MustCompile(fmt.Sprintf(`^T1\nrun \d+  T1  running  %s  in-place  test-model\n$`, regexp.QuoteMeta(root))).MatchString(result.stdout) {
					t.Fatalf("add --start stdout = %q, want T1, then the runs line of a running run", result.stdout)
				}
				return
			}
			result := runHomeDesk(t, home, append(args, "--json")...)
			requireSuccess(t, result)
			var out struct {
				Task model.Task `json:"task"`
				Run  model.Run  `json:"run"`
			}
			if err := json.Unmarshal([]byte(result.stdout), &out); err != nil {
				t.Fatalf("add --start --json stdout = %q: %v", result.stdout, err)
			}
			if out.Task.Number != 1 || out.Task.Notes != "the prompt" || out.Run.Task != 1 || out.Run.State != model.RunRunning ||
				out.Run.Root != root || out.Run.Isolation != "in-place" || out.Run.Model != "test-model" || out.Run.FirstMessage != first {
				t.Fatalf("add --start --json = %#v, want task T1 and its running run on the given route", out)
			}
		})
	}
}

// With no --root, a task whose project is a folder in no git repo and no listed root runs in the scratch root, in-place.
func TestAddStartOfAProjectUnderNoRootRunsInTheScratchRoot(t *testing.T) {
	home, _ := startOnlyHome(t, nil)
	result := runHomeDesk(t, home, "add", "-t", "outside", "-p", t.TempDir(), "--start", "--json")
	requireSuccess(t, result)
	var out struct {
		Run model.Run `json:"run"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &out); err != nil {
		t.Fatalf("stdout = %q: %v", result.stdout, err)
	}
	if out.Run.Root != home.Paths.ScratchRoot() || out.Run.Isolation != "in-place" {
		t.Fatalf("run = %#v, want the scratch root %s, in-place", out.Run, home.Paths.ScratchRoot())
	}
}

func TestAddRouteFlagWithoutStartIsAUsageErrorAndAddsNothing(t *testing.T) {
	home, root := startOnlyHome(t, nil)
	for _, flag := range [][]string{{"--root", root}, {"--isolation", "in-place"}, {"--model", "test-model"}, {"--first-message", "{task_file}"}} {
		result := runHomeDesk(t, home, append([]string{"add", "-t", "no start"}, flag...)...)
		if result.exit != 2 || result.stdout != "" || !strings.Contains(result.stderr, flag[0]+" picks the run's route, so it needs --start") {
			t.Errorf("add %v = exit %d, stdout %q, stderr %q; want exit 2 naming the flag and --start", flag, result.exit, result.stdout, result.stderr)
		}
	}
	if list := runHomeDesk(t, home, "list", "--all", "--json"); !strings.Contains(list.stdout, `"tasks":[]`) && !strings.Contains(list.stdout, `"tasks": []`) {
		t.Fatalf("list after refused adds = %q, want no tasks", list.stdout)
	}
}

// A start refused after the add keeps the task, exits with the refusal's code, and names the task so the caller can
// retry with run start. A spawn that fails prints the task and the failed run first, as run start does.
func TestAddStartWhoseStartIsRefusedKeepsTheTaskAndNamesIt(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.Config)
		args   func(root string) []string
		extra  map[string]string
		code   string
		exit   int
		stdout string // a regexp
	}{
		{"runner off", func(c *config.Config) { c.Runner.Enabled = false }, nil, nil, model.CodeRunnerOff, 1, `^$`},
		{"an agent session in the scratch root", nil, func(string) []string { return nil }, map[string]string{"DESK_SESSION": "agent-session"}, model.CodeNotAllowed, 1, `^$`},
		{"an unknown model", nil, func(root string) []string { return []string{"--root", root, "--model", "nope"} }, nil, model.CodeBadInput, 2, `^$`},
		{"a spawn that fails", nil, func(root string) []string { return []string{"--root", root, "--isolation", "worktree"} }, nil, model.CodeRunFailed, 1, `^T1\nrun \d+  T1  failed  `},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, root := startOnlyHome(t, tc.mutate)
			args := []string{"add", "-t", "refused", "--start", "--root", root}
			if tc.args != nil {
				args = append([]string{"add", "-t", "refused", "--start"}, tc.args(root)...)
			}
			result := runDeskWithEnv(t, home.Machine, t.TempDir(), args, "", tc.extra)
			want := "herdr-desk add: " + tc.code + ": T1 was added, but its run did not start: "
			if result.exit != tc.exit || !strings.HasPrefix(result.stderr, want) || !regexp.MustCompile(tc.stdout).MatchString(result.stdout) {
				t.Fatalf("add --start = exit %d, stdout %q, stderr %q; want exit %d, stdout matching %q, stderr starting %q",
					result.exit, result.stdout, result.stderr, tc.exit, tc.stdout, want)
			}
			requireSuccess(t, runHomeDesk(t, home, "show", "T1"))
		})
	}
}
