package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestW1OpenMigratesVersionTwoFirstMessagesWithoutChangingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desk.db")
	db, err := sqlOpenSQLite(path)
	if err != nil {
		t.Fatalf("open version-2 database: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE events(
		id INTEGER PRIMARY KEY,
		ts TEXT NOT NULL,
		session TEXT NOT NULL DEFAULT '',
		who TEXT NOT NULL,
		kind TEXT NOT NULL,
		task INTEGER NULL,
		data JSON NOT NULL,
		tags JSON NULL,
		run INTEGER NULL,
		v INTEGER NOT NULL
	);
	CREATE TABLE tasks(
		number INTEGER PRIMARY KEY,
		title TEXT NOT NULL,
		notes TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		project TEXT NOT NULL DEFAULT '',
		thread TEXT NOT NULL DEFAULT '',
		archived INTEGER NOT NULL DEFAULT 0,
		root TEXT NOT NULL DEFAULT '',
		isolation TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		created_ts TEXT NOT NULL,
		updated_ts TEXT NOT NULL
	);
	CREATE TABLE steps(
		task INTEGER NOT NULL,
		short_id TEXT NOT NULL,
		text TEXT NOT NULL,
		done INTEGER NOT NULL DEFAULT 0,
		pos INTEGER NOT NULL,
		PRIMARY KEY(task, short_id)
	);
	CREATE TABLE runs(
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
		exit INTEGER NULL,
		left_open INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE sessions(id TEXT PRIMARY KEY, continues TEXT NULL);
	CREATE TABLE coordinator(
		id INTEGER PRIMARY KEY CHECK (id = 1),
		session TEXT NOT NULL,
		workspace TEXT NOT NULL,
		pane TEXT NOT NULL,
		cursor INTEGER NOT NULL DEFAULT 0
	);
	INSERT INTO tasks(number, title, notes, status, root, isolation, model, created_ts, updated_ts)
		VALUES(12, 'keep task', 'keep notes', 'ready', '/repo', 'worktree', 'model-a', '2026-10-04T12:00:00Z', '2026-10-04T12:00:00Z');
	INSERT INTO runs(id, task, state, root, isolation, model, started_ts)
		VALUES(4, 12, 'ended', '/repo', 'worktree', 'model-a', '2026-10-04T12:01:00Z');
	PRAGMA user_version = 2;`); err != nil {
		db.Close()
		t.Fatalf("seed version-2 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close version-2 database: %v", err)
	}

	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("Open() migrates version 2: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	detail, err := st.GetTask(context.Background(), 12)
	if err != nil {
		t.Fatalf("GetTask() after migration: %v", err)
	}
	if got, want := detail.Task, (model.Task{Number: 12, Title: "keep task", Notes: "keep notes", Status: model.StatusReady, Root: "/repo", Isolation: "worktree", Model: "model-a"}); got.Number != want.Number || got.Title != want.Title || got.Notes != want.Notes || got.Status != want.Status || got.Root != want.Root || got.Isolation != want.Isolation || got.Model != want.Model || got.FirstMessage != "" {
		t.Errorf("migrated task = %#v, want preserved row with empty first message", got)
	}
	runs, err := st.ListRuns(context.Background())
	if err != nil {
		t.Fatalf("ListRuns() after migration: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != 4 || runs[0].Task != 12 || runs[0].State != model.RunEnded || runs[0].Root != "/repo" || runs[0].FirstMessage != "" {
		t.Errorf("migrated runs = %#v, want the preserved row with empty first message", runs)
	}

	db = w1OpenDatabase(t, path)
	defer db.Close()
	if got := pragmaInt(t, db, "user_version"); got != 3 {
		t.Errorf("user_version = %d, want 3", got)
	}
	for _, table := range []string{"tasks", "runs"} {
		var value string
		if err := db.QueryRow("SELECT first_message FROM " + table + " LIMIT 1").Scan(&value); err != nil {
			t.Errorf("read %s.first_message: %v", table, err)
			continue
		}
		if value != "" {
			t.Errorf("%s.first_message = %q, want migration default empty", table, value)
		}
	}
}

func TestW1SetTaskRecordsAndClearsFirstMessageAndRejectsInvalidTemplates(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task := addOpenTask(t, st)
	message := "Read {" + model.TaskFile + "} before you begin."

	updated, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{FirstMessage: &message})
	if err != nil {
		t.Fatalf("SetTask(valid first message) error = %v", err)
	}
	if updated.FirstMessage != message {
		t.Errorf("SetTask(valid first message) = %q, want %q", updated.FirstMessage, message)
	}
	detail, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("GetTask() after setting first message: %v", err)
	}
	if detail.Task.FirstMessage != message {
		t.Errorf("persisted first message = %q, want %q", detail.Task.FirstMessage, message)
	}
	var recorded model.Patch
	if err := json.Unmarshal(detail.History[len(detail.History)-1].Data, &recorded); err != nil {
		t.Fatalf("unmarshal first-message patch: %v", err)
	}
	if recorded.FirstMessage == nil || *recorded.FirstMessage != message {
		t.Errorf("first-message patch = %#v, want the new value", recorded)
	}

	empty := ""
	cleared, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{FirstMessage: &empty})
	if err != nil {
		t.Fatalf("SetTask(clear first message) error = %v", err)
	}
	if cleared.FirstMessage != "" {
		t.Errorf("SetTask(clear first message) = %q, want empty", cleared.FirstMessage)
	}

	before := eventCount(t, st)
	invalid := "No task-file placeholder"
	_, err = st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{FirstMessage: &invalid})
	assertRefusalCode(t, err, model.CodeBadInput)
	if got := eventCount(t, st); got != before {
		t.Errorf("event count after invalid first message = %d, want unchanged %d", got, before)
	}
	afterInvalid, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("GetTask() after invalid first message: %v", err)
	}
	if afterInvalid.Task.FirstMessage != "" {
		t.Errorf("first message after invalid patch = %q, want cleared value", afterInvalid.Task.FirstMessage)
	}
}

func TestW1StartRunStoresRouteFirstMessageWithoutChangingTheTask(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task := addOpenTask(t, st)
	message := "Begin with {" + model.TaskFile + "}."
	route := store.RunRoute{Root: "/repo", Isolation: "worktree", Model: "model-a", FirstMessage: message}

	run, err := st.StartRun(ctx, task.Number, route, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	if run.FirstMessage != message {
		t.Errorf("run first message = %q, want route value %q", run.FirstMessage, message)
	}
	detail, err := st.GetTask(ctx, task.Number)
	if err != nil {
		t.Fatalf("GetTask() after StartRun: %v", err)
	}
	if detail.Task.FirstMessage != "" {
		t.Errorf("task first message after StartRun = %q, want unchanged empty", detail.Task.FirstMessage)
	}
	var patch model.Patch
	if err := json.Unmarshal(detail.History[len(detail.History)-1].Data, &patch); err != nil {
		t.Fatalf("unmarshal start patch: %v", err)
	}
	if patch.FirstMessage != nil {
		t.Errorf("start patch first message = %q, want no task first-message patch", *patch.FirstMessage)
	}
}

func w1OpenDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sqlOpenSQLite(path)
	if err != nil {
		t.Fatalf("open database %q: %v", path, err)
	}
	return db
}
