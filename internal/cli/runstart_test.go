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
		if result.exit != 1 || !strings.Contains(result.stdout, "failed") || !regexp.MustCompile(`run \d+ failed: spawn: \S`).MatchString(result.stderr) {
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
