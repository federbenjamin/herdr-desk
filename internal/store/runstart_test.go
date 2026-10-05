package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestOpenMigratesRoutingRunsToStartingWithoutLeavingThemOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desk.db")
	db, err := sqlOpenSQLite(path)
	if err != nil {
		t.Fatalf("open version-1 database: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE runs(
		id INTEGER PRIMARY KEY,
		task INTEGER NOT NULL,
		state TEXT NOT NULL,
		root TEXT NOT NULL DEFAULT '',
		isolation TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		session TEXT NOT NULL DEFAULT '',
		workspace TEXT NOT NULL DEFAULT '',
		pane TEXT NOT NULL DEFAULT '',
		started_ts TEXT NOT NULL,
		ended_ts TEXT NULL,
		exit INTEGER NULL
	);
	INSERT INTO runs(id, task, state, started_ts) VALUES(1, 7, 'routing', '2026-10-04T12:00:00Z');
	PRAGMA user_version = 1;`); err != nil {
		db.Close()
		t.Fatalf("seed version-1 routing run: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close version-1 database: %v", err)
	}

	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("Open() migrates version 1: %v", err)
	}
	runs, err := st.ListRuns(context.Background())
	if err != nil {
		st.Close()
		t.Fatalf("list migrated runs: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}
	if len(runs) != 1 || runs[0].State != model.RunStarting {
		t.Fatalf("migrated runs = %#v, want one starting run", runs)
	}

	db, err = sqlOpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read migrated user_version: %v", err)
	}
	if version != 2 {
		t.Errorf("user_version = %d, want 2", version)
	}
	var leftOpen int
	if err := db.QueryRow("SELECT left_open FROM runs WHERE id = 1").Scan(&leftOpen); err != nil {
		t.Fatalf("read migrated left_open: %v", err)
	}
	if leftOpen != 0 {
		t.Errorf("runs.left_open = %d, want 0", leftOpen)
	}
}

func TestStartRunEndsAnIdleRunAndWaitsWhenTheCapIsFull(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	first, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "first", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add first task: %v", err)
	}
	idle, err := st.StartRun(ctx, first.Number, policyRoute, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if changed, err := st.UpdateRun(ctx, idle.ID, model.RunStarting, store.RunUpdate{State: model.RunIdle}); err != nil || !changed {
		t.Fatalf("make first run idle = (%t, %v)", changed, err)
	}

	now = now.Add(time.Minute)
	replacement, err := st.StartRun(ctx, first.Number, policyRoute, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start replacement run: %v", err)
	}
	if replacement.State != model.RunStarting {
		t.Errorf("replacement state = %q, want starting because idle takes no cap slot", replacement.State)
	}
	gotIdle, ok, err := st.CurrentRun(ctx, first.Number)
	if err != nil || !ok {
		t.Fatalf("read replacement run = (%t, %v)", ok, err)
	}
	if gotIdle.ID != replacement.ID {
		t.Fatalf("current run = %d, want replacement %d", gotIdle.ID, replacement.ID)
	}

	second, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "second", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add second task: %v", err)
	}
	waiting, err := st.StartRun(ctx, second.Number, policyRoute, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start capped run: %v", err)
	}
	if waiting.State != model.RunWaiting {
		t.Errorf("capped run state = %q, want waiting", waiting.State)
	}

	runs, err := st.ListRuns(ctx)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if runs[0].State != model.RunEnded || !runs[0].EndedTS.Equal(now) {
		t.Errorf("idle run after replacement = %#v, want ended at %v", runs[0], now)
	}
}

func TestClaimWaitingStartsTheOldestRunOnceASlotOpens(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	var waiting []model.Run
	for _, title := range []string{"first", "second"} {
		task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: title, Status: model.StatusReady}})
		if err != nil {
			t.Fatalf("add %s task: %v", title, err)
		}
		run, err := st.StartRun(ctx, task.Number, policyRoute, store.RunCaps{Slots: 0, PerDay: 1000})
		if err != nil {
			t.Fatalf("start %s run: %v", title, err)
		}
		if run.State != model.RunWaiting {
			t.Fatalf("%s run state = %q, want waiting", title, run.State)
		}
		waiting = append(waiting, run)
	}

	now = now.Add(time.Minute)
	claimed, ok, err := st.ClaimWaiting(ctx, 1)
	if err != nil {
		t.Fatalf("ClaimWaiting() error = %v", err)
	}
	if !ok || claimed.ID != waiting[0].ID || claimed.State != model.RunStarting || !claimed.StartedTS.Equal(now) {
		t.Errorf("ClaimWaiting() = (%#v, %t), want oldest run %d starting at %v", claimed, ok, waiting[0].ID, now)
	}
	persisted, ok, err := st.CurrentRun(ctx, waiting[0].Task)
	if err != nil || !ok {
		t.Fatalf("read claimed run = (%t, %v)", ok, err)
	}
	if persisted.State != model.RunStarting || !persisted.StartedTS.Equal(now) {
		t.Errorf("claimed run in store = %#v, want starting at %v", persisted, now)
	}
	second, ok, err := st.CurrentRun(ctx, waiting[1].Task)
	if err != nil || !ok {
		t.Fatalf("read second waiting run = (%t, %v)", ok, err)
	}
	if second.State != model.RunWaiting {
		t.Errorf("second waiting run state = %q, want waiting", second.State)
	}
}

func TestUpdateRunFromIdleKeepsTheOriginalStartTime(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	st := openRunPolicyStore(t, &now, false)
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "idle run", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	run, err := st.StartRun(ctx, task.Number, policyRoute, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	now = now.Add(time.Hour)
	if changed, err := st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{State: model.RunIdle}); err != nil || !changed {
		t.Fatalf("set run idle = (%t, %v)", changed, err)
	}
	now = now.Add(time.Hour)
	if changed, err := st.UpdateRun(ctx, run.ID, model.RunIdle, store.RunUpdate{State: model.RunRunning}); err != nil || !changed {
		t.Fatalf("resume idle run = (%t, %v)", changed, err)
	}
	got, ok, err := st.CurrentRun(ctx, task.Number)
	if err != nil || !ok {
		t.Fatalf("read resumed run = (%t, %v)", ok, err)
	}
	if !got.StartedTS.Equal(run.StartedTS) {
		t.Errorf("resumed run started at %v, want original start %v", got.StartedTS, run.StartedTS)
	}
}

func TestHandBackWithALostClaimWritesNothing(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "hand back", Status: model.StatusReady}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	run, err := st.StartRun(ctx, task.Number, policyRoute, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	before, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("read task before lost claim: %v", err)
	}

	updated, claimed, err := st.HandBack(ctx, run, store.HandBack{
		From:   model.RunWaiting,
		To:     model.RunEnded,
		Status: model.StatusReview,
		Note:   "this note must not appear",
	})
	if err != nil {
		t.Fatalf("HandBack() lost claim error = %v", err)
	}
	if claimed || updated.Number != 0 {
		t.Errorf("HandBack() lost claim = (%#v, %t), want (zero task, false)", updated, claimed)
	}
	after, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("read task after lost claim: %v", err)
	}
	if after.Task.Status != before.Task.Status || len(after.History) != len(before.History) {
		t.Errorf("task after lost claim = %#v with %d events, want status %q and %d events", after.Task, len(after.History), before.Task.Status, len(before.History))
	}
	current, ok, err := st.CurrentRun(ctx, task.Number)
	if err != nil || !ok {
		t.Fatalf("read run after lost claim = (%t, %v)", ok, err)
	}
	if current.State != model.RunStarting {
		t.Errorf("run state after lost claim = %q, want starting", current.State)
	}
}

// The day cap holds against processes that start runs at once: each opens its own store, as each `run start` does,
// and the count and the insert share one IMMEDIATE transaction, so one start past PerDay-1 wins and the rest are
// refused cap-reached with no run row.
func TestStartRunHoldsTheDayCapAgainstConcurrentProcesses(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "desk.db")
	seed := newStoreAt(t, path)
	caps := store.RunCaps{Slots: 100, PerDay: 3}
	for range 2 {
		task := mustAdd(t, seed, "spent", model.StatusReady, "")
		if _, err := seed.StartRun(ctx, task.Number, policyRoute, caps); err != nil {
			t.Fatalf("spend the day: %v", err)
		}
	}
	const racers = 12
	var tasks []int
	for range racers {
		tasks = append(tasks, mustAdd(t, seed, "racer", model.StatusReady, "").Number)
	}
	stores := make([]*store.Store, racers)
	for i := range stores {
		stores[i] = newStoreAt(t, path)
	}
	errs := make([]error, racers)
	var gate, done sync.WaitGroup
	gate.Add(1)
	for i := range racers {
		done.Go(func() {
			gate.Wait()
			_, errs[i] = stores[i].StartRun(ctx, tasks[i], policyRoute, caps)
		})
	}
	gate.Done()
	done.Wait()
	won, refused := 0, 0
	for i, err := range errs {
		switch r, ok := model.AsRefusal(err); {
		case err == nil:
			won++
		case ok && r.Code == model.CodeCapReached:
			refused++
		default:
			t.Errorf("racer %d: StartRun() error = %v, want a run or cap-reached", i, err)
		}
	}
	runs, err := seed.ListRuns(ctx)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if won != 1 || refused != racers-1 || len(runs) != caps.PerDay {
		t.Fatalf("%d racers won, %d refused, %d runs; want 1 won, %d refused, %d runs", won, refused, len(runs), racers-1, caps.PerDay)
	}
}

// A hand-back's own text that the scan refuses must not leave its task started behind an ended run: the claim, the
// status, and a fixed note land, and the refusal comes back wrapped for the runner to log.
func TestHandBackLandsWithAFixedNoteWhenTheScanRefusesItsText(t *testing.T) {
	for _, tc := range []struct {
		name string
		scan func(context.Context, string) (string, error)
		code string
	}{
		{"secret found", func(context.Context, string) (string, error) { return "token", nil }, model.CodeSecretDetected},
		{"scanner broken", func(context.Context, string) (string, error) { return "", errors.New("exit status 2") }, model.CodeScanFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			refuse := false
			st := openStore(t, store.Options{Scanner: func(ctx context.Context, text string) (string, error) {
				if refuse {
					return tc.scan(ctx, text)
				}
				return "", nil
			}})
			task := mustAdd(t, st, "hand back", model.StatusReady, "")
			run, err := st.StartRun(ctx, task.Number, policyRoute, store.RunCaps{Slots: 1, PerDay: 1000})
			if err != nil {
				t.Fatalf("start run: %v", err)
			}
			refuse = true
			got, claimed, err := st.HandBack(ctx, run, store.HandBack{From: model.RunStarting, To: model.RunFailed,
				Status: model.StatusBlocked, Tags: []string{model.TagRunner}, Note: "spawn: git said secret", Reason: "spawn: git said secret"})
			if r, ok := model.AsRefusal(err); !errors.Is(err, store.ErrNoteWithheld) || !ok || r.Code != tc.code {
				t.Fatalf("HandBack() error = %v, want ErrNoteWithheld wrapping %s", err, tc.code)
			}
			if !claimed || got.Status != model.StatusBlocked {
				t.Fatalf("HandBack() = (%#v, %t), want the task blocked and the claim held", got, claimed)
			}
			cur, _, err := st.CurrentRun(ctx, task.Number)
			if err != nil {
				t.Fatalf("read run: %v", err)
			}
			detail, err := st.GetTask(ctx, task.Number)
			if err != nil {
				t.Fatalf("read task: %v", err)
			}
			last := detail.History[len(detail.History)-2]
			if cur.State != model.RunFailed || strings.Contains(cur.Reason, "said") || !strings.Contains(cur.Reason, tc.code) ||
				last.Kind != model.KindNote || strings.Contains(string(last.Data), "said") || !strings.Contains(string(last.Data), "withheld") {
				t.Fatalf("run %#v, note %s; want the run failed and a fixed note and reason naming %s", cur, last.Data, tc.code)
			}
		})
	}
}

// A run's reason is what the runner said when it failed it: herdr-desk run start prints it.
func TestHandBackWritesItsReasonOnTheRun(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task := mustAdd(t, st, "fails", model.StatusReady, "")
	run, err := st.StartRun(ctx, task.Number, policyRoute, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if _, claimed, err := st.HandBack(ctx, run, store.HandBack{From: model.RunStarting, To: model.RunFailed, Reason: "spawn: no herdr"}); err != nil || !claimed {
		t.Fatalf("HandBack() = (%t, %v), want a claim", claimed, err)
	}
	cur, _, err := st.CurrentRun(ctx, task.Number)
	if err != nil || cur.Reason != "spawn: no herdr" {
		t.Fatalf("run = %#v, %v; want reason %q", cur, err, "spawn: no herdr")
	}
}
