package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
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

func newRunsAPIHarness(t *testing.T, runner api.RunnerControl) apiHarness {
	t.Helper()

	root := t.TempDir()
	paths := config.Paths{
		ConfigDir: filepath.Join(root, "config", "desk"),
		DataDir:   filepath.Join(root, "data", "desk"),
	}
	if err := os.MkdirAll(paths.ConfigDir, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := config.WriteToken(paths, firstToken); err != nil {
		t.Fatalf("write token: %v", err)
	}

	st, err := store.Open(paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Default()
	cfg.Home.Listen = "127.0.0.1:48123"
	cfg.Runner.Cap = 3
	server := api.NewServer(api.ServerOptions{
		Store:  st,
		Config: cfg,
		Paths:  paths,
		Runner: runner,
	})
	return apiHarness{
		paths:     paths,
		trusted:   server.Handler(true),
		untrusted: server.Handler(false),
	}
}

func decodeRunStatus(t *testing.T, body []byte) api.Status {
	t.Helper()

	var status api.Status
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return status
}

func requireRunRefusal(t *testing.T, body []byte, want string) {
	t.Helper()

	var refusal model.Refusal
	if err := json.Unmarshal(body, &refusal); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if refusal.Code != want {
		t.Fatalf("refusal code = %q, want %q", refusal.Code, want)
	}
}

func TestRunsKillServesBothHandlersAndPassesTheActorAndTask(t *testing.T) {
	runner := &runnerControl{
		state:    "on",
		killTask: model.Task{Number: 71, Title: "stop this run"},
	}
	h := newRunsAPIHarness(t, runner)
	actor := store.Actor{Session: "operator-session"}

	for _, tc := range []struct {
		name    string
		handler http.Handler
		token   string
	}{
		{name: "trusted", handler: h.trusted},
		{name: "untrusted", handler: h.untrusted, token: firstToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postAPI(t, tc.handler, tc.token, api.MethodRunsKill, mustJSON(t, map[string]any{
				"actor": actor,
				"task":  71,
			}))
			requireStatus(t, rec, http.StatusOK)

			var got model.Task
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode killed task: %v", err)
			}
			if got.Number != runner.killTask.Number || got.Title != runner.killTask.Title {
				t.Fatalf("killed task = %#v, want %#v", got, runner.killTask)
			}
		})
	}

	if len(runner.killCalls) != 2 {
		t.Fatalf("Kill calls = %d, want 2", len(runner.killCalls))
	}
	for i, call := range runner.killCalls {
		if call.actor != actor || call.task != 71 {
			t.Fatalf("Kill call %d = %#v, want actor %#v and task 71", i, call, actor)
		}
	}
}

func TestRunsKillMapsRunnerRefusalsToConflictWithTheirCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
	}{
		{name: "no live run", code: model.CodeNoRun},
		{name: "agent actor", code: model.CodeNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRunsAPIHarness(t, &runnerControl{killErr: &model.Refusal{Code: tc.code, Msg: "runner refused"}})
			rec := postAPI(t, h.trusted, "", api.MethodRunsKill, []byte(`{"actor":{},"task":71}`))
			requireStatus(t, rec, http.StatusConflict)
			requireRunRefusal(t, rec.Body.Bytes(), tc.code)
		})
	}
}

func TestRunnerPauseServesBothHandlersAndReturnsRunnerStatus(t *testing.T) {
	runner := &runnerControl{state: "on"}
	h := newRunsAPIHarness(t, runner)
	actor := store.Actor{Session: "operator-session"}

	for _, tc := range []struct {
		name    string
		handler http.Handler
		token   string
	}{
		{name: "trusted", handler: h.trusted},
		{name: "untrusted", handler: h.untrusted, token: firstToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postAPI(t, tc.handler, tc.token, api.MethodRunnerPause, mustJSON(t, map[string]any{
				"actor":  actor,
				"paused": true,
			}))
			requireStatus(t, rec, http.StatusOK)
			status := decodeRunStatus(t, rec.Body.Bytes())
			if status.RunnerState != runner.state || !status.RunnerPaused || status.RunnerCap != 3 {
				t.Fatalf("pause status = %#v, want runner state %q, paused true, cap 3", status, runner.state)
			}
		})
	}

	if len(runner.pauseCalls) != 2 {
		t.Fatalf("Pause calls = %d, want 2", len(runner.pauseCalls))
	}
	for i, call := range runner.pauseCalls {
		if call.actor != actor || !call.paused {
			t.Fatalf("Pause call %d = %#v, want actor %#v and paused true", i, call, actor)
		}
	}
}

func TestRunnerMethodsRequireATokenOnTheUntrustedHandler(t *testing.T) {
	h := newRunsAPIHarness(t, &runnerControl{state: "on"})

	for _, tc := range []struct {
		name   string
		method string
		body   []byte
	}{
		{name: "kill", method: api.MethodRunsKill, body: []byte(`{"actor":{},"task":71}`)},
		{name: "pause", method: api.MethodRunnerPause, body: []byte(`{"actor":{},"paused":true}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postAPI(t, h.untrusted, "", tc.method, tc.body)
			requireStatus(t, rec, http.StatusUnauthorized)
		})
	}
}

func TestRunnerMethodsAreUnknownWithoutARunnerAndStatusIsOff(t *testing.T) {
	h := newRunsAPIHarness(t, nil)

	for _, tc := range []struct {
		name   string
		method string
		body   []byte
	}{
		{name: "kill", method: api.MethodRunsKill, body: []byte(`{"actor":{},"task":71}`)},
		{name: "pause", method: api.MethodRunnerPause, body: []byte(`{"actor":{},"paused":true}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postAPI(t, h.trusted, "", tc.method, tc.body)
			requireStatus(t, rec, http.StatusBadRequest)
		})
	}

	rec := postAPI(t, h.trusted, "", api.MethodStatus, []byte(`{}`))
	requireStatus(t, rec, http.StatusOK)
	status := decodeRunStatus(t, rec.Body.Bytes())
	if status.RunnerState != "off" || status.RunnerPaused || status.RunnerCap != 3 {
		t.Fatalf("status without runner = %#v, want state off, paused false, cap 3", status)
	}
}

func TestRunnerMethodsRejectMalformedBodies(t *testing.T) {
	h := newRunsAPIHarness(t, &runnerControl{state: "on"})

	for _, method := range []string{api.MethodRunsKill, api.MethodRunnerPause} {
		t.Run(method, func(t *testing.T) {
			rec := postAPI(t, h.trusted, "", method, []byte(`{"unterminated"`))
			requireStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func startRunnerHome(t *testing.T) *testutil.Home {
	t.Helper()

	fakeDir := t.TempDir()
	fakeHerdr, err := filepath.Abs(filepath.Join("..", "..", "scripts", "e2e", "fake-herdr.py"))
	if err != nil {
		t.Fatalf("make fake herdr path absolute: %v", err)
	}
	if err := os.Symlink(fakeHerdr, filepath.Join(fakeDir, "herdr")); err != nil {
		t.Fatalf("symlink fake herdr: %v", err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_HERDR_DIR", fakeDir)

	cfg := config.Default()
	cfg.Runner.Enabled = true
	cfg.Agent.Router = []string{"true"}
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
	if _, err := st.StartRun(ctx, task.Number); err != nil {
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
	home.Stop()

	_, err := home.Client().PauseRunner(context.Background(), store.Actor{}, true)
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
	home.Stop()

	_, err := home.Client().KillRun(context.Background(), store.Actor{}, 71)
	refusal, ok := err.(*model.Refusal)
	if !ok {
		t.Fatalf("KillRun error = %T %v, want *model.Refusal", err, err)
	}
	if refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("KillRun refusal = %q, want %q", refusal.Code, model.CodeHomeUnreachable)
	}
}
