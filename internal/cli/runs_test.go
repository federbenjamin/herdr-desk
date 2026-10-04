package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/testutil"
)

func runnerHome(t *testing.T, listen bool, worker []string) (*testutil.Home, string) {
	t.Helper()
	root := t.TempDir()
	bin := t.TempDir()
	router := filepath.Join(bin, "router")
	if err := os.WriteFile(router, []byte("#!/bin/sh\nprintf '%s\\n' '{\"root\":\""+root+"\",\"isolation\":\"in-place\",\"reason\":\"test\"}'\n"), 0o700); err != nil {
		t.Fatalf("write router: %v", err)
	}
	testutil.FakeHerdr(t)
	cfg := config.Default()
	cfg.Runner.Enabled = true
	cfg.Runner.Cap = 2
	cfg.Runner.PollSeconds = 1
	cfg.Roots = []config.Root{{Path: root, About: "test root", Isolation: "in-place"}}
	cfg.Agent.Router = []string{router}
	cfg.Agent.Models = []string{"test-model"}
	cfg.Agent.Worker = worker
	return testutil.StartHome(t, testutil.HomeOptions{Config: cfg, Listen: listen}), root
}

func startLiveRun(t *testing.T, home *testutil.Home, root, title string) (int, model.Run) {
	t.Helper()
	task := addTask(t, home, title, "--thread", "agent")
	requireSuccess(t, runHomeDesk(t, home, "set", fmt.Sprintf("T%d", task), "ready", "--root", root, "--isolation", "in-place", "--model", "test-model"))
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		result := runHomeDesk(t, home, "runs", "--json")
		if result.exit == 0 {
			var runs []model.Run
			if err := json.Unmarshal([]byte(result.stdout), &runs); err != nil {
				t.Fatalf("decode runs JSON: %v; output=%q", err, result.stdout)
			}
			for _, run := range runs {
				if run.Task == task && run.State == model.RunRunning {
					return task, run
				}
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("T%d did not become live; last runs result = (%d, %q, %q)", task, result.exit, result.stdout, result.stderr)
		case <-tick.C:
		}
	}
}

func TestRunsPrintsEmptyTextAndJSONArray(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	text := runHomeDesk(t, home, "runs")
	requireSuccess(t, text)
	if text.stdout != "no live runs\n" {
		t.Fatalf("runs text = %q, want no-live-runs message", text.stdout)
	}
	jsonResult := runHomeDesk(t, home, "runs", "--json")
	requireSuccess(t, jsonResult)
	var runs []model.Run
	if err := json.Unmarshal([]byte(jsonResult.stdout), &runs); err != nil {
		t.Fatalf("decode empty runs JSON: %v; output=%q", err, jsonResult.stdout)
	}
	if runs == nil || len(runs) != 0 {
		t.Fatalf("empty runs JSON = %#v, want a non-nil empty array", runs)
	}
}

func TestRunsPrintsLiveRunsAndAllIncludesKilledRuns(t *testing.T) {
	home, root := runnerHome(t, false, []string{"worker"})
	task, run := startLiveRun(t, home, root, "live run")

	result := runHomeDesk(t, home, "runs")
	requireSuccess(t, result)
	wantLine := regexp.MustCompile(fmt.Sprintf(`^run %d  T%d  %s  %s  in-place  test-model  .+\n$`, run.ID, task, regexp.QuoteMeta(run.State), regexp.QuoteMeta(root)))
	if !wantLine.MatchString(result.stdout) {
		t.Fatalf("runs output = %q, want one formatted live-run line", result.stdout)
	}

	killed := runHomeDesk(t, home, "runs", "kill", fmt.Sprintf("T%d", task))
	requireSuccess(t, killed)
	if killed.stdout != fmt.Sprintf("T%d blocked\n", task) {
		t.Fatalf("runs kill output = %q, want blocked task", killed.stdout)
	}
	all := runHomeDesk(t, home, "runs", "--all", "--json")
	requireSuccess(t, all)
	var runs []model.Run
	if err := json.Unmarshal([]byte(all.stdout), &runs); err != nil {
		t.Fatalf("decode all runs JSON: %v; output=%q", err, all.stdout)
	}
	if len(runs) != 1 || runs[0].ID != run.ID || runs[0].State != model.RunKilled {
		t.Fatalf("all runs = %#v, want the killed run", runs)
	}
}

func TestRunsKillReportsEveryRefusalAndRejectsBadTaskIDs(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	addTask(t, home, "without a run")
	requireRefusal(t, runHomeDesk(t, home, "runs", "kill", "T1"), "runs kill", model.CodeNoRun, 1)
	requireRefusal(t, runHomeDesk(t, home, "runs", "kill", "T99"), "runs kill", model.CodeUnknownTask, 1)
	badID := runHomeDesk(t, home, "runs", "kill", "not-a-task")
	if badID.exit != 2 {
		t.Fatalf("bad task id exit = %d, want 2; stderr=%q", badID.exit, badID.stderr)
	}

	runner, root := runnerHome(t, false, []string{"worker"})
	task, _ := startLiveRun(t, runner, root, "agent cannot kill")
	denied := runDeskWithEnv(t, runner.Machine, t.TempDir(), []string{"runs", "kill", fmt.Sprintf("T%d", task)}, "", map[string]string{"DESK_SESSION": "agent-session"}, nil)
	requireRefusal(t, denied, "runs kill", model.CodeNotAllowed, 1)
}

func TestRunnerStatusAndControlShowLiveCapacityAndRejectAgents(t *testing.T) {
	home, root := runnerHome(t, false, []string{"worker"})
	_, _ = startLiveRun(t, home, root, "runner status")

	status := runHomeDesk(t, home, "runner")
	requireSuccess(t, status)
	if status.stdout != "runner on · 1/2 live\n" {
		t.Fatalf("runner status = %q, want on state and live capacity", status.stdout)
	}
	paused := runHomeDesk(t, home, "runner", "pause")
	requireSuccess(t, paused)
	if paused.stdout != "runner paused · 1/2 live\n" {
		t.Fatalf("runner pause = %q, want paused state and live capacity", paused.stdout)
	}
	pausedStatus := runHomeDesk(t, home, "runner", "status")
	requireSuccess(t, pausedStatus)
	if pausedStatus.stdout != "runner paused · 1/2 live\n" {
		t.Fatalf("paused runner status = %q, want paused state and live capacity", pausedStatus.stdout)
	}
	resumed := runHomeDesk(t, home, "runner", "resume")
	requireSuccess(t, resumed)
	if resumed.stdout != "runner on · 1/2 live\n" {
		t.Fatalf("runner resume = %q, want on state and live capacity", resumed.stdout)
	}
	for _, action := range []string{"pause", "resume"} {
		denied := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"runner", action}, "", map[string]string{"DESK_SESSION": "agent-session"}, nil)
		requireRefusal(t, denied, "runner "+action, model.CodeNotAllowed, 1)
	}
}

func TestRunsKillAndRunnerPauseReachTheHomeFromAClientMachine(t *testing.T) {
	home, root := runnerHome(t, true, []string{"worker"})
	task, _ := startLiveRun(t, home, root, "client controls run")
	client := testutil.NewClientMachine(t, home)

	paused := runDeskWithEnv(t, client, t.TempDir(), []string{"runner", "pause"}, "", nil, nil)
	requireSuccess(t, paused)
	if paused.stdout != "runner paused · 1/2 live\n" {
		t.Fatalf("client runner pause = %q, want paused state and live capacity", paused.stdout)
	}
	killed := runDeskWithEnv(t, client, t.TempDir(), []string{"runs", "kill", fmt.Sprintf("T%d", task)}, "", nil, nil)
	requireSuccess(t, killed)
	if killed.stdout != fmt.Sprintf("T%d blocked\n", task) {
		t.Fatalf("client runs kill = %q, want blocked task", killed.stdout)
	}
}

func TestRunsOnlyListsLiveRunsWithoutAll(t *testing.T) {
	home, root := runnerHome(t, false, []string{"worker"})
	task, _ := startLiveRun(t, home, root, "hide ended runs")
	requireSuccess(t, runHomeDesk(t, home, "runs", "kill", fmt.Sprintf("T%d", task)))
	result := runHomeDesk(t, home, "runs")
	requireSuccess(t, result)
	if strings.TrimSpace(result.stdout) != "no live runs" {
		t.Fatalf("runs after kill = %q, want no live runs without --all", result.stdout)
	}
}
