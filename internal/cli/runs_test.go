package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
	"github.com/federbenjamin/herdr-desk/internal/ticker"
)

// runnerHome is a home with the runner on and a ticker that ticks every 50 ms.
func runnerHome(t *testing.T, worker []string) (*testutil.Home, string) {
	t.Helper()
	root := t.TempDir()
	testutil.FakeHerdr(t)
	cfg := config.Default()
	cfg.Runner.Enabled = true
	cfg.Runner.Cap = 2
	cfg.Roots = []config.Root{{Path: root, About: "test root", Isolation: "in-place"}}
	cfg.Agent.Models = []string{"test-model"}
	cfg.Agent.Worker = worker
	home := testutil.StartHome(t, testutil.HomeOptions{Config: cfg})
	tk, err := ticker.Start(context.Background(), ticker.Options{Paths: home.Paths, Every: 50 * time.Millisecond, Logf: t.Logf})
	if err != nil {
		t.Fatalf("start the ticker: %v", err)
	}
	t.Cleanup(func() { tk.Close() })
	return home, root
}

func startLiveRun(t *testing.T, home *testutil.Home, root, title string) (int, model.Run) {
	t.Helper()
	task := addTask(t, home, title, "--thread", "agent")
	requireSuccess(t, runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", task), "--root", root, "--isolation", "in-place", "--model", "test-model"))
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
	home, root := runnerHome(t, []string{"worker"})
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

	runner, root := runnerHome(t, []string{"worker"})
	task, _ := startLiveRun(t, runner, root, "agent cannot kill")
	denied := runDeskWithEnv(t, runner.Machine, t.TempDir(), []string{"runs", "kill", fmt.Sprintf("T%d", task)}, "", map[string]string{"DESK_SESSION": "agent-session"})
	requireRefusal(t, denied, "runs kill", model.CodeNotAllowed, 1)
}

func TestRunnerStatusAndControlShowLiveCapacityAndRejectAgents(t *testing.T) {
	home, root := runnerHome(t, []string{"worker"})
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
		denied := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"runner", action}, "", map[string]string{"DESK_SESSION": "agent-session"})
		requireRefusal(t, denied, "runner "+action, model.CodeNotAllowed, 1)
	}
}

// With no ticker, the checks of live runs wait; runner status and context must say so rather
// than read as all well.
func TestRunnerStatusAndContextSayWhenNoTickerRuns(t *testing.T) {
	home, _ := startOnlyHome(t, nil)
	status := runHomeDesk(t, home, "runner", "status")
	requireSuccess(t, status)
	if status.stdout != "runner on · 0/2 live · "+api.NoTickerText+"\n" {
		t.Fatalf("runner status with no ticker = %q, want the live count then that no ticker runs", status.stdout)
	}
	ctx := runHomeDesk(t, home, "context")
	requireSuccess(t, ctx)
	if !strings.Contains(ctx.stdout, "\n"+api.NoTickerText+"\n") {
		t.Fatalf("context with no ticker = %q, want the no-ticker line", ctx.stdout)
	}
	asJSON := runHomeDesk(t, home, "context", "--json")
	requireSuccess(t, asJSON)
	if !strings.Contains(asJSON.stdout, `"no_ticker":true`) {
		t.Fatalf("context --json with no ticker = %q, want no_ticker true", asJSON.stdout)
	}
}

func TestRunsKillAndRunnerPauseReachTheHomeFromAClientMachine(t *testing.T) {
	home, root := runnerHome(t, []string{"worker"})
	task, _ := startLiveRun(t, home, root, "client controls run")
	client := testutil.NewClientMachine(t, home)

	paused := runDeskWithEnv(t, client, t.TempDir(), []string{"runner", "pause"}, "", nil)
	requireSuccess(t, paused)
	if paused.stdout != "runner paused · 1/2 live\n" {
		t.Fatalf("client runner pause = %q, want paused state and live capacity", paused.stdout)
	}
	killed := runDeskWithEnv(t, client, t.TempDir(), []string{"runs", "kill", fmt.Sprintf("T%d", task)}, "", nil)
	requireSuccess(t, killed)
	if killed.stdout != fmt.Sprintf("T%d blocked\n", task) {
		t.Fatalf("client runs kill = %q, want blocked task", killed.stdout)
	}
}

func TestRunsOnlyListsLiveRunsWithoutAll(t *testing.T) {
	home, root := runnerHome(t, []string{"worker"})
	task, _ := startLiveRun(t, home, root, "hide ended runs")
	requireSuccess(t, runHomeDesk(t, home, "runs", "kill", fmt.Sprintf("T%d", task)))
	result := runHomeDesk(t, home, "runs")
	requireSuccess(t, result)
	if strings.TrimSpace(result.stdout) != "no live runs" {
		t.Fatalf("runs after kill = %q, want no live runs without --all", result.stdout)
	}
}
