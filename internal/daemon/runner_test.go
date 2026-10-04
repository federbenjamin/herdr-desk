package daemon_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/daemon"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
)

func TestStartWritesRunnerStateForItsConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, cfg *config.Config)
		want  string
	}{
		{name: "disabled", want: "off"},
		{
			// The seal internal/testutil sets names a herdr that does not exist.
			name: "herdr missing",
			setup: func(t *testing.T, cfg *config.Config) {
				cfg.Runner.Enabled = true
			},
			want: "no-herdr",
		},
		{
			name: "router missing",
			setup: func(t *testing.T, cfg *config.Config) {
				cfg.Runner.Enabled = true
				setFakeHerdr(t)
			},
			want: "no-router",
		},
		{
			name: "herdr and executable router",
			setup: func(t *testing.T, cfg *config.Config) {
				cfg.Runner.Enabled = true
				cfg.Agent.Router = []string{setFakeHerdr(t)}
			},
			want: "on",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			if tc.setup != nil {
				tc.setup(t, &cfg)
			}
			instance, paths := startRunnerDaemon(t, cfg)

			info, err := daemon.ReadInfo(paths)
			if err != nil {
				t.Fatalf("ReadInfo() error = %v", err)
			}
			if info.Runner != tc.want {
				t.Fatalf("runner state = %q, want %q", info.Runner, tc.want)
			}
			if err := instance.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		})
	}
}

func TestStartRunsTheRunnerLoopForAnArmedTask(t *testing.T) {
	cfg := config.Default()
	cfg.Runner.Enabled = true
	cfg.Runner.PollSeconds = 1
	cfg.Agent.Router = []string{setFakeHerdr(t)}
	cfg.Agent.Models = []string{"test-model"}
	root := t.TempDir()
	cfg.Roots = []config.Root{{Path: root, Isolation: "in-place"}}

	_, paths := startRunnerDaemon(t, cfg)
	client := api.NewClient(api.ClientOptions{Paths: paths, Config: cfg})
	task, err := client.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "the runner must start this task", Thread: "agent",
	}})
	if err != nil {
		t.Fatalf("AddTask() error = %v", err)
	}
	ready := model.StatusReady
	if _, err := client.SetTask(context.Background(), store.Actor{}, task.Number, model.Patch{
		Status:    &ready,
		Root:      stringPointer(root),
		Isolation: stringPointer("in-place"),
		Model:     stringPointer("test-model"),
	}); err != nil {
		t.Fatalf("SetTask() to arm T%d: %v", task.Number, err)
	}

	pollUntil(t, 5*time.Second, func() bool {
		runs, err := client.ListRuns(context.Background())
		return err == nil && len(runs) == 1 && runs[0].Task == task.Number && runs[0].State == model.RunRunning
	})
}

func TestPauseRunnerRewritesTheDaemonRunnerState(t *testing.T) {
	cfg := config.Default()
	cfg.Runner.Enabled = true
	cfg.Agent.Router = []string{setFakeHerdr(t)}

	_, paths := startRunnerDaemon(t, cfg)
	client := api.NewClient(api.ClientOptions{Paths: paths, Config: cfg})
	if _, err := client.PauseRunner(context.Background(), store.Actor{}, true); err != nil {
		t.Fatalf("PauseRunner(true) error = %v", err)
	}
	pollUntil(t, time.Second, func() bool {
		info, err := daemon.ReadInfo(paths)
		return err == nil && info.Runner == "paused"
	})
}

func TestCloseStopsTheRunnerLoopAndRemovesDaemonInfo(t *testing.T) {
	cfg := config.Default()
	cfg.Runner.Enabled = true
	cfg.Runner.PollSeconds = 1
	cfg.Agent.Router = []string{setFakeHerdr(t)}

	instance, paths := startRunnerDaemon(t, cfg)
	client := api.NewClient(api.ClientOptions{Paths: paths, Config: cfg})
	if _, err := client.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
		Title: "must not start after close", Thread: "agent", Status: model.StatusReady,
	}}); err != nil {
		t.Fatalf("AddTask() error = %v", err)
	}
	if err := instance.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := daemon.ReadInfo(paths); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadInfo() after Close() error = %v, want daemon info removed", err)
	}
}

func startRunnerDaemon(t *testing.T, cfg config.Config) (*daemon.Instance, config.Paths) {
	t.Helper()
	paths := testutil.NewMachine(t).Paths
	instance, err := daemon.Start(context.Background(), paths, cfg)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	return instance, paths
}

func setFakeHerdr(t *testing.T) string {
	t.Helper()
	testutil.FakeHerdr(t)
	return os.Getenv("DESK_HERDR")
}

func stringPointer(s string) *string { return &s }
