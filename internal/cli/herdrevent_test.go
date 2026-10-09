package cli_test

import (
	"context"
	"database/sql"
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

func TestHerdrEventHookDisabledDoesNotTrackAnOwnedPane(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	fake := testutil.FakeHerdr(t)
	task, run := trackedEventRun(t, home, "p-owned")

	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"hook", "herdr-event"}, "", map[string]string{
		"DESK_HOOKS":              "off",
		"HERDR_PLUGIN_EVENT_JSON": `{"data":{"pane_id":"p-owned"}}`,
	})
	if result.exit != 0 || result.stdout != "" || result.stderr != "" {
		t.Fatalf("hook result = (%d, %q, %q), want silent success", result.exit, result.stdout, result.stderr)
	}
	assertNoHerdrCalls(t, fake)
	assertTrackedEventRun(t, home, task, run, model.RunRunning, model.StatusStarted)
}

func TestHerdrEventHookDoesNotCreateStateForAnInvalidEvent(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	fake := testutil.FakeHerdr(t)

	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"hook", "herdr-event"}, "", map[string]string{
		"HERDR_PLUGIN_EVENT_JSON": "not JSON",
	})
	if result.exit != 0 || result.stdout != "" || result.stderr != "" {
		t.Fatalf("hook result = (%d, %q, %q), want silent success", result.exit, result.stdout, result.stderr)
	}
	if _, err := os.Stat(home.Paths.DB()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("hook created a store for an invalid event: %v", err)
	}
	assertNoHerdrCalls(t, fake)
}

// A hook that tracks nothing because of a fault must say why in the log, and still exit 0 with nothing on herdr's
// streams: a renamed payload field, a broken config, or a store another binary migrated would otherwise stop run
// tracking in silence.
func TestHerdrEventHookLogsWhyItTrackedNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event string
		spoil func(t *testing.T, home *testutil.Home)
		want  string
	}{
		{"no pane id", `{"data":{"pane":"p-owned"}}`, func(*testing.T, *testutil.Home) {}, "no data.pane_id"},
		{"config does not load", `{"data":{"pane_id":"p-owned"}}`, func(t *testing.T, home *testutil.Home) {
			if err := os.WriteFile(home.Paths.ConfigFile(), []byte("runner = ["), 0o600); err != nil {
				t.Fatalf("spoil the config: %v", err)
			}
		}, "the config does not load"},
		{"store of another schema", `{"data":{"pane_id":"p-owned"}}`, func(t *testing.T, home *testutil.Home) {
			db, err := sql.Open("sqlite", home.Paths.DB())
			if err != nil {
				t.Fatalf("open the store file: %v", err)
			}
			defer db.Close()
			if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
				t.Fatalf("set the schema version: %v", err)
			}
		}, "schema version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := testutil.StartHome(t, testutil.HomeOptions{})
			fake := testutil.FakeHerdr(t)
			trackedEventRun(t, home, "p-owned")
			tc.spoil(t, home)
			result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"hook", "herdr-event"}, "", map[string]string{
				"HERDR_PLUGIN_EVENT":      "pane.closed",
				"HERDR_PLUGIN_EVENT_JSON": tc.event,
			})
			if result.exit != 0 || result.stdout != "" || result.stderr != "" {
				t.Fatalf("hook result = (%d, %q, %q), want silent success", result.exit, result.stdout, result.stderr)
			}
			assertNoHerdrCalls(t, fake)
			logged, err := os.ReadFile(home.Paths.Log())
			if err != nil || !strings.Contains(string(logged), "hook herdr-event") || !strings.Contains(string(logged), tc.want) {
				t.Fatalf("log = %q (%v), want one line naming %q", logged, err, tc.want)
			}
		})
	}
}

func TestHerdrEventHookDoesNotCallHerdrForAnUnownedPane(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	fake := testutil.FakeHerdr(t)
	task := addTask(t, home, "unowned event pane", "--status", "open")

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
	task, run := trackedEventRun(t, home, "p-owned")

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

	assertTrackedEventRun(t, home, task, run, model.RunEnded, model.StatusReview)
}

// A worker whose process ends by itself makes herdr 0.9.1 send pane.exited, never pane.closed, with this payload in
// HERDR_PLUGIN_EVENT_JSON and nothing on stdin (probe log, 2026-10-05); the pane is gone by the time the hook asks.
func TestHerdrEventHookSendsAWorkerWhosePaneExitedToReview(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	testutil.FakeHerdr(t)
	task, run := trackedEventRun(t, home, "w3:p1")

	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"hook", "herdr-event"}, "", map[string]string{
		"HERDR_PLUGIN_EVENT":      "pane.exited",
		"HERDR_PLUGIN_EVENT_JSON": `{"event":"pane_exited","data":{"type":"pane_exited","pane_id":"w3:p1","workspace_id":"w3"}}`,
	})
	if result.exit != 0 || result.stdout != "" || result.stderr != "" {
		t.Fatalf("hook result = (%d, %q, %q), want silent success", result.exit, result.stdout, result.stderr)
	}
	assertTrackedEventRun(t, home, task, run, model.RunEnded, model.StatusReview)
}

func trackedEventRun(t *testing.T, home *testutil.Home, pane string) (model.Task, model.Run) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(home.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "tracked event pane"}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	run, err := st.StartRun(ctx, store.Actor{}, task.Number, store.RunRoute{}, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if updated, err := st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{
		State:     model.RunRunning,
		Session:   "worker-session",
		Workspace: "workspace-owned",
		Pane:      pane,
	}); err != nil || !updated {
		t.Fatalf("make run trackable = (%t, %v), want update", updated, err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close setup store: %v", err)
	}
	return task, run
}

func assertTrackedEventRun(t *testing.T, home *testutil.Home, task model.Task, run model.Run, wantRun string, wantTask model.Status) {
	t.Helper()
	st, err := store.OpenReadOnly(home.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("reopen store read-only: %v", err)
	}
	defer st.Close()
	runs, err := st.ListRuns(context.Background())
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != run.ID || runs[0].State != wantRun {
		t.Fatalf("runs = %#v, want run %d in state %q", runs, run.ID, wantRun)
	}
	detail, err := st.GetTask(context.Background(), task.Number)
	if err != nil {
		t.Fatalf("read tracked task: %v", err)
	}
	if detail.Task.Status != wantTask {
		t.Fatalf("task status = %q, want %q", detail.Task.Status, wantTask)
	}
}

func assertNoHerdrCalls(t *testing.T, fake string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(fake, "calls.log")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("hook called Herdr: %v", err)
	}
}
