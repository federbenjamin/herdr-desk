package api_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

type runnerCall struct {
	actor  store.Actor
	task   int
	paused bool
}

type runnerControl struct {
	state      string
	paused     bool
	killTask   model.Task
	killErr    error
	pauseErr   error
	killCalls  []runnerCall
	pauseCalls []runnerCall
}

func (r *runnerControl) State() string { return r.state }

func (r *runnerControl) Paused() bool { return r.paused }

func (r *runnerControl) Pause(_ context.Context, actor store.Actor, paused bool) error {
	r.pauseCalls = append(r.pauseCalls, runnerCall{actor: actor, paused: paused})
	if r.pauseErr == nil {
		r.paused = paused
	}
	return r.pauseErr
}

func (r *runnerControl) Kill(_ context.Context, actor store.Actor, task int) (model.Task, error) {
	r.killCalls = append(r.killCalls, runnerCall{actor: actor, task: task})
	return r.killTask, r.killErr
}

func (r *runnerControl) Start(context.Context, store.Actor, int, store.RunRoute) (model.Run, error) {
	return model.Run{}, nil
}

func (r *runnerControl) AfterSet(context.Context, int) {}

func newRunsServer(t *testing.T, runner api.RunnerControl) *api.Server {
	t.Helper()

	root := t.TempDir()
	paths := config.Paths{
		ConfigDir: filepath.Join(root, "config", "herdr-desk"),
		StateDir:  filepath.Join(root, "state", "herdr-desk"),
		DataDir:   filepath.Join(root, "data", "herdr-desk"),
	}
	st, err := store.Open(paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Default()
	cfg.Runner.Cap = 3
	return api.NewServer(api.ServerOptions{Store: st, Config: cfg, Paths: paths, Runner: runner})
}

func decodeRunStatus(t *testing.T, resp api.RPCResponse) api.Status {
	t.Helper()

	requireResult(t, resp)
	var status api.Status
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return status
}

func TestRunsKillPassesTheActorAndTask(t *testing.T) {
	runner := &runnerControl{
		state:    "on",
		killTask: model.Task{Number: 71, Title: "stop this run"},
	}
	srv := newRunsServer(t, runner)
	actor := store.Actor{Session: "operator-session"}

	resp := answer(t, srv, api.MethodRunsKill, mustJSON(t, map[string]any{"actor": actor, "task": 71}))
	requireResult(t, resp)
	var got model.Task
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode killed task: %v", err)
	}
	if got.Number != runner.killTask.Number || got.Title != runner.killTask.Title {
		t.Fatalf("killed task = %#v, want %#v", got, runner.killTask)
	}
	if len(runner.killCalls) != 1 || runner.killCalls[0].actor != actor || runner.killCalls[0].task != 71 {
		t.Fatalf("Kill calls = %#v, want one with actor %#v and task 71", runner.killCalls, actor)
	}
}

func TestRunsKillAnswersRunnerRefusalsWithTheirCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
	}{
		{name: "no live run", code: model.CodeNoRun},
		{name: "agent actor", code: model.CodeNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newRunsServer(t, &runnerControl{killErr: &model.Refusal{Code: tc.code, Msg: "runner refused"}})
			requireRefusal(t, answer(t, srv, api.MethodRunsKill, []byte(`{"actor":{},"task":71}`)), tc.code)
		})
	}
}

func TestRunnerPauseReturnsRunnerStatus(t *testing.T) {
	runner := &runnerControl{state: "on"}
	srv := newRunsServer(t, runner)
	actor := store.Actor{Session: "operator-session"}

	status := decodeRunStatus(t, answer(t, srv, api.MethodRunnerPause, mustJSON(t, map[string]any{"actor": actor, "paused": true})))
	if status.RunnerState != runner.state || !status.RunnerPaused || status.RunnerCap != 3 {
		t.Fatalf("pause status = %#v, want runner state %q, paused true, cap 3", status, runner.state)
	}
	if len(runner.pauseCalls) != 1 || runner.pauseCalls[0].actor != actor || !runner.pauseCalls[0].paused {
		t.Fatalf("Pause calls = %#v, want one with actor %#v and paused true", runner.pauseCalls, actor)
	}
}

func TestRunnerMethodsAreUnknownWithoutARunnerAndStatusIsOff(t *testing.T) {
	srv := newRunsServer(t, nil)

	for _, tc := range []struct {
		name   string
		method string
		params []byte
	}{
		{name: "kill", method: api.MethodRunsKill, params: []byte(`{"actor":{},"task":71}`)},
		{name: "pause", method: api.MethodRunnerPause, params: []byte(`{"actor":{},"paused":true}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireBadRequest(t, answer(t, srv, tc.method, tc.params))
		})
	}

	status := decodeRunStatus(t, answer(t, srv, api.MethodStatus, []byte(`{}`)))
	if status.RunnerState != "off" || status.RunnerPaused || status.RunnerCap != 3 {
		t.Fatalf("status without runner = %#v, want state off, paused false, cap 3", status)
	}
}

func TestRunnerMethodsRejectMalformedParams(t *testing.T) {
	srv := newRunsServer(t, &runnerControl{state: "on"})

	for _, method := range []string{api.MethodRunsKill, api.MethodRunnerPause} {
		t.Run(method, func(t *testing.T) {
			requireBadRequest(t, answer(t, srv, method, []byte(`{"unterminated"`)))
		})
	}
}

func startRunnerHome(t *testing.T) *testutil.Home {
	t.Helper()

	testutil.FakeHerdr(t)

	cfg := config.Default()
	cfg.Runner.Enabled = true
	return testutil.StartHome(t, testutil.HomeOptions{Config: cfg})
}

func TestClientPauseRunnerReturnsStatusAndStatusShowsThePause(t *testing.T) {
	home := startRunnerHome(t)

	paused, err := home.Client().PauseRunner(context.Background(), store.Actor{}, true)
	if err != nil {
		t.Fatalf("PauseRunner: %v", err)
	}
	if !paused.RunnerPaused {
		t.Fatalf("PauseRunner status = %#v, want RunnerPaused true", paused)
	}

	status, err := home.Client().Status(context.Background())
	if err != nil {
		t.Fatalf("Status after PauseRunner: %v", err)
	}
	if !status.RunnerPaused {
		t.Fatalf("Status after PauseRunner = %#v, want RunnerPaused true", status)
	}
}

func TestClientKillRunReturnsTheTaskTheRunnerKilled(t *testing.T) {
	home := startRunnerHome(t)
	ctx := context.Background()
	task, err := home.Client().AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "live run to kill", Status: model.StatusReady, Thread: "agent",
	}})
	if err != nil {
		t.Fatalf("add armed task: %v", err)
	}

	st, err := store.Open(home.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store to make a live run: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.StartRun(ctx, task.Number, store.RunRoute{Root: t.TempDir(), Isolation: "in-place"}, 1); err != nil {
		t.Fatalf("start run: %v", err)
	}

	got, err := home.Client().KillRun(ctx, store.Actor{}, task.Number)
	if err != nil {
		t.Fatalf("KillRun: %v", err)
	}
	if got.Number != task.Number {
		t.Fatalf("KillRun task number = %d, want %d", got.Number, task.Number)
	}
}

func TestClientKillRunReturnsRunnerRefusals(t *testing.T) {
	home := startRunnerHome(t)
	ctx := context.Background()
	task, err := home.Client().AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "no live run"}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}

	for _, tc := range []struct {
		name  string
		actor store.Actor
		code  string
	}{
		{name: "task has no live run", code: model.CodeNoRun},
		{name: "agent may not kill", actor: store.Actor{Session: "agent-session"}, code: model.CodeNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := home.Client().KillRun(ctx, tc.actor, task.Number)
			refusal, ok := err.(*model.Refusal)
			if !ok {
				t.Fatalf("KillRun error = %T %v, want *model.Refusal", err, err)
			}
			if refusal.Code != tc.code {
				t.Fatalf("KillRun refusal = %q, want %q", refusal.Code, tc.code)
			}
		})
	}
}

func TestClientPauseRunnerReturnsHomeUnreachableWhenHomeIsDown(t *testing.T) {
	home := startRunnerHome(t)
	client := testutil.ClientFor(testutil.NewClientMachine(t, home))
	home.Stop()

	_, err := client.PauseRunner(context.Background(), store.Actor{}, true)
	refusal, ok := err.(*model.Refusal)
	if !ok {
		t.Fatalf("PauseRunner error = %T %v, want *model.Refusal", err, err)
	}
	if refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("PauseRunner refusal = %q, want %q", refusal.Code, model.CodeHomeUnreachable)
	}
}

func TestClientKillRunReturnsHomeUnreachableWhenHomeIsDown(t *testing.T) {
	home := startRunnerHome(t)
	client := testutil.ClientFor(testutil.NewClientMachine(t, home))
	home.Stop()

	_, err := client.KillRun(context.Background(), store.Actor{}, 71)
	refusal, ok := err.(*model.Refusal)
	if !ok {
		t.Fatalf("KillRun error = %T %v, want *model.Refusal", err, err)
	}
	if refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("KillRun refusal = %q, want %q", refusal.Code, model.CodeHomeUnreachable)
	}
}
