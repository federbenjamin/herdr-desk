package runner_test

import (
	"context"
	"os"
	"path/filepath"
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
	c.Runner.AgentsMayArm = true
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

func refusalCode(err error) string {
	r, ok := model.AsRefusal(err)
	if !ok {
		return ""
	}
	return r.Code
}
