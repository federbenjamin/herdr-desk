package cli_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestHerdrEventHookDoesNotCreateStateForDisabledOrInvalidEvents(t *testing.T) {
	for _, test := range []struct {
		name  string
		extra map[string]string
	}{
		{
			name: "disabled",
			extra: map[string]string{
				"DESK_HOOKS":              "off",
				"HERDR_PLUGIN_EVENT_JSON": `{"data":{"pane_id":"p-disabled"}}`,
			},
		},
		{
			name: "invalid event payload",
			extra: map[string]string{
				"HERDR_PLUGIN_EVENT_JSON": "not JSON",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := testutil.StartHome(t, testutil.HomeOptions{})
			fake := testutil.FakeHerdr(t)

			result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"hook", "herdr-event"}, "", test.extra)
			if result.exit != 0 || result.stdout != "" || result.stderr != "" {
				t.Fatalf("hook result = (%d, %q, %q), want silent success", result.exit, result.stdout, result.stderr)
			}
			if _, err := os.Stat(home.Paths.DB()); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("hook created a store for %s: %v", test.name, err)
			}
			assertNoHerdrCalls(t, fake)
		})
	}
}

func TestHerdrEventHookDoesNotCallHerdrForAnUnownedPane(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	fake := testutil.FakeHerdr(t)
	task := addTask(t, home, "unowned event pane")

	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"hook", "herdr-event"}, "", map[string]string{
		"HERDR_PLUGIN_EVENT_JSON": `{"data":{"pane_id":"p-unowned"}}`,
	})
	if result.exit != 0 || result.stdout != "" || result.stderr != "" {
		t.Fatalf("hook result = (%d, %q, %q), want silent success", result.exit, result.stdout, result.stderr)
	}
	assertNoHerdrCalls(t, fake)

	detail, err := home.Client().GetTask(context.Background(), task)
	if err != nil {
		t.Fatalf("read task after unowned event: %v", err)
	}
	if detail.Task.Status != model.StatusOpen || len(detail.History) != 1 {
		t.Fatalf("task after unowned event = %#v with %d history events, want unchanged open task", detail.Task, len(detail.History))
	}
}

func TestHerdrEventHookTracksAnOwnedPaneFromHerdrInsteadOfTheEventPayload(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	fake := testutil.FakeHerdr(t)
	ctx := context.Background()

	st, err := store.Open(home.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "tracked event pane"}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	run, err := st.StartRun(ctx, task.Number, store.RunRoute{}, 1)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if updated, err := st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{
		State:     model.RunRunning,
		Session:   "worker-session",
		Workspace: "workspace-owned",
		Pane:      "p-owned",
	}); err != nil || !updated {
		t.Fatalf("make run trackable = (%t, %v), want update", updated, err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close setup store: %v", err)
	}

	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"hook", "herdr-event"}, "", map[string]string{
		"HERDR_PLUGIN_EVENT_JSON": `{"data":{"pane_id":"p-owned","agent_status":"working"}}`,
	})
	if result.exit != 0 || result.stdout != "" || result.stderr != "" {
		t.Fatalf("hook result = (%d, %q, %q), want silent success", result.exit, result.stdout, result.stderr)
	}

	calls, err := os.ReadFile(filepath.Join(fake, "calls.log"))
	if err != nil {
		t.Fatalf("read Herdr calls: %v", err)
	}
	if strings.Count(string(calls), "pane get p-owned") != 1 {
		t.Fatalf("Herdr calls = %q, want one pane get for the owned pane", calls)
	}

	st, err = store.OpenReadOnly(home.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("reopen store read-only: %v", err)
	}
	defer st.Close()
	runs, err := st.ListRuns(ctx)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].State != model.RunEnded {
		t.Fatalf("runs after missing pane = %#v, want one ended run", runs)
	}
	detail, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("read tracked task: %v", err)
	}
	if detail.Task.Status != model.StatusReview {
		t.Fatalf("task status after missing pane = %q, want %q", detail.Task.Status, model.StatusReview)
	}
}

func assertNoHerdrCalls(t *testing.T, fake string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(fake, "calls.log")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("hook called Herdr: %v", err)
	}
}
