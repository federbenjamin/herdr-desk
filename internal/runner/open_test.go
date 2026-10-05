package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestOpenAppliesStoreConfigAndCloseOwnsItsStore(t *testing.T) {
	m := testutil.NewMachine(t)
	scanner := filepath.Join(t.TempDir(), "scanner")
	if err := os.WriteFile(scanner, []byte("#!/bin/sh\ncase \"$(cat)\" in *blocked-by-open-test*) exit 1;; esac\n"), 0o700); err != nil {
		t.Fatalf("write scanner: %v", err)
	}
	c := config.Default()
	c.Coordinator.StartRuns = config.StartRunsAuto
	c.Runner.OnMerged = "done"
	c.SecretScan.Command = []string{scanner}

	r, err := runner.Open(m.Paths, c)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = r.Close()
		}
	}()

	ctx := context.Background()
	ready := model.StatusReady
	task, err := r.Store().AddTask(ctx, store.Actor{Session: "agent"}, store.AddTaskInput{
		TaskData: model.TaskData{Title: "agent may arm this", Status: ready},
	})
	if err != nil {
		t.Fatalf("AddTask(agent ready) error = %v; want config to allow arming", err)
	}
	review := model.StatusReview
	updated, err := r.Store().SetTask(ctx, store.Actor{}, task.Number, model.Patch{Status: &review, Merged: true})
	if err != nil {
		t.Fatalf("SetTask(review merged) error = %v", err)
	}
	if updated.Status != model.StatusDone {
		t.Fatalf("merged task status = %q; want %q from runner.on_merged", updated.Status, model.StatusDone)
	}
	if _, err := r.Store().AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "blocked-by-open-test"}}); refusalCode(err) != model.CodeSecretDetected {
		t.Fatalf("AddTask(configured scanner hit) error = %v; want %q", err, model.CodeSecretDetected)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	closed = true
	if _, err := r.Store().AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "after close"}}); err == nil {
		t.Fatal("store write after Close() error = nil; want closed store")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close() error = %v; want idempotent close", err)
	}
}

// The runner a process opens logs to the home's herdr-desk.log, whichever process it is: an rpc request's stderr is
// thrown away when it succeeds, and a hook's goes to herdr.
func TestOpenLogsTheRunnersLinesToTheHomesLog(t *testing.T) {
	m := testutil.NewMachine(t)
	t.Setenv("DESK_HERDR", filepath.Join(t.TempDir(), "no-herdr"))
	r, err := runner.Open(m.Paths, config.Default())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()
	task, err := r.Store().AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "live"}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	run, err := r.Store().StartRun(ctx, task.Number, store.RunRoute{}, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if ok, err := r.Store().UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{State: model.RunRunning, Pane: "p1"}); err != nil || !ok {
		t.Fatalf("make the run running = (%t, %v)", ok, err)
	}
	if err := r.Reconcile(ctx); err == nil {
		t.Fatal("Reconcile() with no herdr error = nil, want one")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	logged, err := os.ReadFile(m.Paths.Log())
	if err != nil || !strings.Contains(string(logged), "herdr-desk runner: find herdr") {
		t.Fatalf("herdr-desk.log = %q (%v), want the runner's find herdr line", logged, err)
	}
	if info, err := os.Stat(m.Paths.Log()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("herdr-desk.log mode = %v (%v), want 0600", info, err)
	}
}

func refusalCode(err error) string {
	r, ok := model.AsRefusal(err)
	if !ok {
		return ""
	}
	return r.Code
}
