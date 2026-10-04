package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/secretscan"
	"github.com/federbenjamin/desk/internal/store"
)

func openStore(t *testing.T, options store.Options) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state", "desk.db"), options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return st
}

func statusPtr(status model.Status) *model.Status {
	return &status
}

func stringPtr(value string) *string {
	return &value
}

func refusalCode(t *testing.T, err error) string {
	t.Helper()
	refusal, ok := model.AsRefusal(err)
	if !ok {
		t.Fatalf("error %v is not a model.Refusal", err)
	}
	return refusal.Code
}

func addOpenTask(t *testing.T, st *store.Store) model.Task {
	t.Helper()
	task, err := st.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{
		TaskData: model.TaskData{Title: "seed task"},
	})
	if err != nil {
		t.Fatalf("add seed task: %v", err)
	}
	return task
}

func eventCount(t *testing.T, st *store.Store) int {
	t.Helper()
	var count int
	_, err := st.ExportEvents(context.Background(), countingWriter{count: &count})
	if err != nil {
		t.Fatalf("count exported events: %v", err)
	}
	return count
}

func allTasks(t *testing.T, st *store.Store) []model.Task {
	t.Helper()
	tasks, err := st.ListTasks(context.Background(), store.Filter{All: true})
	if err != nil {
		t.Fatalf("list all tasks: %v", err)
	}
	return tasks
}

type countingWriter struct {
	count *int
}

func (w countingWriter) Write(p []byte) (int, error) {
	*w.count += strings.Count(string(p), "\n")
	return len(p), nil
}

func TestCountByStatusIncludesEveryLiveStatusAndExcludesArchivedAndDoneTasks(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	for _, status := range []model.Status{
		model.StatusOpen,
		model.StatusReady,
		model.StatusReady,
		model.StatusStarted,
		model.StatusBlocked,
		model.StatusReview,
		model.StatusDone,
	} {
		if _, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: string(status), Status: status}}); err != nil {
			t.Fatalf("add %q task: %v", status, err)
		}
	}
	archived, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "archived", Status: model.StatusOpen}})
	if err != nil {
		t.Fatalf("add archived task: %v", err)
	}
	archivedValue := true
	if _, err := st.SetTask(ctx, store.Actor{}, archived.Number, model.Patch{Archived: &archivedValue}); err != nil {
		t.Fatalf("archive task: %v", err)
	}

	got, err := st.CountByStatus(ctx)
	if err != nil {
		t.Fatalf("count statuses: %v", err)
	}
	want := map[model.Status]int{
		model.StatusOpen:    1,
		model.StatusReady:   2,
		model.StatusStarted: 1,
		model.StatusBlocked: 1,
		model.StatusReview:  1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status counts = %#v, want %#v", got, want)
	}
}

func TestOpenRejectsInvalidOptionsAndUnusableOrNewerDatabases(t *testing.T) {
	t.Run("on merged status other than review or done", func(t *testing.T) {
		_, err := store.Open(filepath.Join(t.TempDir(), "desk.db"), store.Options{OnMerged: model.StatusStarted})
		if err == nil || !strings.Contains(err.Error(), "on_merged must be review or done") {
			t.Fatalf("Open invalid OnMerged error = %v, want the allowed statuses", err)
		}
	})

	t.Run("parent path is a regular file", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(parent, []byte("block directory creation"), 0o600); err != nil {
			t.Fatalf("write blocking parent: %v", err)
		}
		_, err := store.Open(filepath.Join(parent, "desk.db"), store.Options{})
		if err == nil {
			t.Fatal("Open unexpectedly accepted a database below a regular file")
		}
	})

	t.Run("database schema is newer than this build", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "desk.db")
		db, err := sqlOpenSQLite(path)
		if err != nil {
			t.Fatalf("open future database: %v", err)
		}
		if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
			db.Close()
			t.Fatalf("set future schema version: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close future database: %v", err)
		}

		_, err = store.Open(path, store.Options{})
		if err == nil || !strings.Contains(err.Error(), "schema version 2") {
			t.Fatalf("Open future database error = %v, want it to name version 2", err)
		}
		db, err = sqlOpenSQLite(path)
		if err != nil {
			t.Fatalf("reopen future database: %v", err)
		}
		defer db.Close()
		var version int
		if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			t.Fatalf("read schema version: %v", err)
		}
		if version != 2 {
			t.Fatalf("schema version after refused Open = %d, want 2", version)
		}
		var tables int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'").Scan(&tables); err != nil {
			t.Fatalf("count tables: %v", err)
		}
		if tables != 0 {
			t.Fatalf("tables after refused Open = %d, want none", tables)
		}
	})
}

func TestClosedStoreReturnsErrorsForEveryReadAndWrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "desk.db")
	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	cases := []struct {
		name string
		call func() error
	}{
		{"add task", func() error {
			_, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "closed"}})
			return err
		}},
		{"set task", func() error { _, err := st.SetTask(ctx, store.Actor{}, 1, model.Patch{Ref: "ref"}); return err }},
		{"change step", func() error {
			_, err := st.Step(ctx, store.Actor{}, 1, model.StepOp{Op: "add", Text: "closed"})
			return err
		}},
		{"write note", func() error {
			_, err := st.Note(ctx, store.Actor{}, store.NoteInput{NoteData: model.NoteData{Text: "closed"}})
			return err
		}},
		{"write decision", func() error {
			_, err := st.Decide(ctx, store.Actor{}, store.DecisionInput{DecisionData: model.DecisionData{Text: "closed"}})
			return err
		}},
		{"write merged", func() error { _, err := st.Merged(ctx, store.Actor{}, model.MergedData{Branch: "closed"}); return err }},
		{"write compacted", func() error { _, err := st.Compacted(ctx, store.Actor{}); return err }},
		{"write continuation", func() error { _, err := st.Continues(ctx, store.Actor{Session: "next"}, "prior"); return err }},
		{"list tasks", func() error { _, err := st.ListTasks(ctx, store.Filter{}); return err }},
		{"get task", func() error { _, err := st.GetTask(ctx, 1); return err }},
		{"list session events", func() error { _, err := st.SessionEvents(ctx, "session"); return err }},
		{"list runs", func() error { _, err := st.ListRuns(ctx); return err }},
		{"count statuses", func() error { _, err := st.CountByStatus(ctx); return err }},
		{"export events", func() error { _, err := st.ExportEvents(ctx, countingWriter{}); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("closed store panicked: %v", recovered)
				}
			}()
			if err := tc.call(); err == nil {
				t.Fatal("closed store returned no error")
			}
		})
	}
}

func TestBareProjectAmbiguityAndAbsoluteProjectAreKeptDistinct(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	for _, project := range []string{"/projects/alpha", "/archive/alpha"} {
		if _, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: project, Project: project}}); err != nil {
			t.Fatalf("seed %q: %v", project, err)
		}
	}
	_, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "ambiguous", Project: "alpha"}})
	if got := refusalCode(t, err); got != model.CodeUnknownProject {
		t.Fatalf("ambiguous project refusal = %q, want %q", got, model.CodeUnknownProject)
	}
	absolute := "/new/location/alpha"
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "absolute", Project: absolute}})
	if err != nil {
		t.Fatalf("add absolute project: %v", err)
	}
	if task.Project != absolute {
		t.Fatalf("absolute project = %q, want %q", task.Project, absolute)
	}
}

func TestRemovedStepsRejectRenameAndToggleAndBlankAddsWriteNothing(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	task := addOpenTask(t, st)
	withStep, err := st.Step(ctx, store.Actor{}, task.Number, model.StepOp{Op: "add", Text: "remove me"})
	if err != nil {
		t.Fatalf("add step: %v", err)
	}
	if _, err := st.Step(ctx, store.Actor{}, task.Number, model.StepOp{Op: "remove", ShortID: withStep.Steps[0].ShortID}); err != nil {
		t.Fatalf("remove step: %v", err)
	}
	before := eventCount(t, st)
	cases := []struct {
		name string
		op   model.StepOp
		code string
	}{
		{"rename removed step", model.StepOp{Op: "rename", ShortID: "s1", Text: "again"}, model.CodeUnknownStep},
		{"toggle removed step", model.StepOp{Op: "toggle", ShortID: "s1"}, model.CodeUnknownStep},
		{"add blank step", model.StepOp{Op: "add", Text: " \t "}, model.CodeEmptyText},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.Step(ctx, store.Actor{}, task.Number, tc.op)
			if got := refusalCode(t, err); got != tc.code {
				t.Fatalf("refusal code = %q, want %q", got, tc.code)
			}
			if got := eventCount(t, st); got != before {
				t.Fatalf("event count after refused step = %d, want %d", got, before)
			}
		})
	}
}

func sqlOpenSQLite(path string) (*sql.DB, error) {
	return sql.Open("sqlite", fmt.Sprintf("file:%s", path))
}

func TestAgentCannotSetOrAddReadyOrDone(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		status    model.Status
		needsTask bool
		write     func(*store.Store, model.Status, int) error
	}{
		{
			name:   "adds ready",
			status: model.StatusReady,
			write: func(st *store.Store, status model.Status, _ int) error {
				_, err := st.AddTask(ctx, store.Actor{Session: "agent"}, store.AddTaskInput{
					TaskData: model.TaskData{Title: "agent task", Status: status},
				})
				return err
			},
		},
		{
			name:   "adds done",
			status: model.StatusDone,
			write: func(st *store.Store, status model.Status, _ int) error {
				_, err := st.AddTask(ctx, store.Actor{Session: "agent"}, store.AddTaskInput{
					TaskData: model.TaskData{Title: "agent task", Status: status},
				})
				return err
			},
		},
		{
			name:      "sets ready",
			status:    model.StatusReady,
			needsTask: true,
			write: func(st *store.Store, status model.Status, number int) error {
				_, err := st.SetTask(ctx, store.Actor{Session: "agent"}, number, model.Patch{Status: statusPtr(status)})
				return err
			},
		},
		{
			name:      "sets done",
			status:    model.StatusDone,
			needsTask: true,
			write: func(st *store.Store, status model.Status, number int) error {
				_, err := st.SetTask(ctx, store.Actor{Session: "agent"}, number, model.Patch{Status: statusPtr(status)})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t, store.Options{})
			var number int
			if tc.needsTask {
				number = addOpenTask(t, st).Number
			}
			before := eventCount(t, st)
			beforeTasks := allTasks(t, st)
			err := tc.write(st, tc.status, number)
			if got := refusalCode(t, err); got != model.CodeNotAllowed {
				t.Fatalf("refusal code = %q, want %q", got, model.CodeNotAllowed)
			}
			if got := eventCount(t, st); got != before {
				t.Fatalf("event count after refused write = %d, want %d", got, before)
			}
			if got := allTasks(t, st); !reflect.DeepEqual(got, beforeTasks) {
				t.Fatalf("tasks after refused write = %#v, want %#v", got, beforeTasks)
			}
		})
	}
}

func TestAgentsMayArmPermitsReadyButNotDone(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		status model.Status
		write  func(*store.Store, model.Status) (model.Task, error)
	}{
		{
			name:   "adds ready",
			status: model.StatusReady,
			write: func(st *store.Store, status model.Status) (model.Task, error) {
				return st.AddTask(ctx, store.Actor{Session: "agent"}, store.AddTaskInput{
					TaskData: model.TaskData{Title: "agent task", Status: status},
				})
			},
		},
		{
			name:   "sets ready",
			status: model.StatusReady,
			write: func(st *store.Store, status model.Status) (model.Task, error) {
				task := addOpenTask(t, st)
				return st.SetTask(ctx, store.Actor{Session: "agent"}, task.Number, model.Patch{Status: statusPtr(status)})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t, store.Options{AgentsMayArm: true})
			task, err := tc.write(st, tc.status)
			if err != nil {
				t.Fatalf("agent write: %v", err)
			}
			if task.Status != model.StatusReady {
				t.Fatalf("status = %q, want %q", task.Status, model.StatusReady)
			}
		})
	}

	for _, operation := range []string{"add", "set"} {
		t.Run(operation+"s done", func(t *testing.T) {
			st := openStore(t, store.Options{AgentsMayArm: true})
			var err error
			switch operation {
			case "add":
				_, err = st.AddTask(ctx, store.Actor{Session: "agent"}, store.AddTaskInput{
					TaskData: model.TaskData{Title: "agent task", Status: model.StatusDone},
				})
			case "set":
				task := addOpenTask(t, st)
				_, err = st.SetTask(ctx, store.Actor{Session: "agent"}, task.Number, model.Patch{Status: statusPtr(model.StatusDone)})
			}
			if got := refusalCode(t, err); got != model.CodeNotAllowed {
				t.Fatalf("refusal code = %q, want %q", got, model.CodeNotAllowed)
			}
		})
	}
}

func TestMergedReviewUsesConfiguredOnMergedStatus(t *testing.T) {
	st := openStore(t, store.Options{OnMerged: model.StatusDone})
	task := addOpenTask(t, st)
	updated, err := st.SetTask(context.Background(), store.Actor{}, task.Number, model.Patch{
		Status: statusPtr(model.StatusReview),
		Merged: true,
	})
	if err != nil {
		t.Fatalf("mark merged review: %v", err)
	}
	if updated.Status != model.StatusDone {
		t.Fatalf("status after merged review = %q, want %q", updated.Status, model.StatusDone)
	}
}

func TestEachTextualWriteIsScannedOnceAsOneJoinedInput(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		fragments []string
		write     func(*store.Store, int) error
	}{
		{
			name:      "add task title notes thread and tags",
			fragments: []string{"title marker", "notes marker", "thread marker", "tag marker"},
			write: func(st *store.Store, _ int) error {
				_, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{
					Title: "title marker", Notes: "notes marker", Thread: "thread marker",
				}, Tags: []string{"tag marker"}})
				return err
			},
		},
		{
			name:      "set title notes thread and ref",
			fragments: []string{"title marker", "notes marker", "thread marker", "ref marker"},
			write: func(st *store.Store, task int) error {
				_, err := st.SetTask(ctx, store.Actor{}, task, model.Patch{
					Title: stringPtr("title marker"), Notes: stringPtr("notes marker"),
					Thread: stringPtr("thread marker"), Ref: "ref marker",
				})
				return err
			},
		},
		{
			name:      "add step text",
			fragments: []string{"step marker"},
			write: func(st *store.Store, task int) error {
				_, err := st.Step(ctx, store.Actor{}, task, model.StepOp{Op: "add", Text: "step marker"})
				return err
			},
		},
		{
			name:      "write note text ref and tags",
			fragments: []string{"note marker", "ref marker", "tag marker"},
			write: func(st *store.Store, task int) error {
				_, err := st.Note(ctx, store.Actor{}, store.NoteInput{NoteData: model.NoteData{
					Text: "note marker", Ref: "ref marker",
				}, Task: task, Tags: []string{"tag marker"}})
				return err
			},
		},
		{
			name:      "write decision text and tags",
			fragments: []string{"decision marker", "tag marker"},
			write: func(st *store.Store, task int) error {
				_, err := st.Decide(ctx, store.Actor{}, store.DecisionInput{DecisionData: model.DecisionData{
					Text: "decision marker",
				}, Task: task, Tags: []string{"tag marker"}})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var inputs []string
			scanning := false
			st := openStore(t, store.Options{Scanner: func(_ context.Context, text string) (string, error) {
				if scanning {
					inputs = append(inputs, text)
				}
				return "", nil
			}})
			task := addOpenTask(t, st)
			scanning = true
			if err := tc.write(st, task.Number); err != nil {
				t.Fatalf("write: %v", err)
			}
			if len(inputs) != 1 {
				t.Fatalf("scanner calls = %d, want 1 (%q)", len(inputs), inputs)
			}
			for _, fragment := range tc.fragments {
				if !strings.Contains(inputs[0], fragment) {
					t.Errorf("scanner input %q does not include %q", inputs[0], fragment)
				}
			}
		})
	}
}

func TestSecretDetectionPreventsEveryTextualWrite(t *testing.T) {
	ctx := context.Background()
	cases := guardedWriteCases(ctx)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, task, enable := guardedStore(t, func(context.Context, string) (string, error) {
				return "test-secret", nil
			})
			beforeEvents := eventCount(t, st)
			beforeTasks := allTasks(t, st)
			enable()
			err := tc.write(st, task.Number)
			if got := refusalCode(t, err); got != model.CodeSecretDetected {
				t.Fatalf("refusal code = %q, want %q", got, model.CodeSecretDetected)
			}
			if got := eventCount(t, st); got != beforeEvents {
				t.Fatalf("event count after refused write = %d, want %d", got, beforeEvents)
			}
			if got := allTasks(t, st); !reflect.DeepEqual(got, beforeTasks) {
				t.Fatalf("tasks after refused write = %#v, want %#v", got, beforeTasks)
			}
			if got := secretPattern(t, err); !strings.Contains(got, "test-secret") {
				t.Fatalf("secret refusal message = %q, want it to name %q", got, "test-secret")
			}
		})
	}
}

func TestScannerFailurePreventsEveryTextualWrite(t *testing.T) {
	ctx := context.Background()
	cases := guardedWriteCases(ctx)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, task, enable := guardedStore(t, func(context.Context, string) (string, error) {
				return "", errors.New("scanner unavailable")
			})
			beforeEvents := eventCount(t, st)
			beforeTasks := allTasks(t, st)
			enable()
			err := tc.write(st, task.Number)
			if got := refusalCode(t, err); got != model.CodeScanFailed {
				t.Fatalf("refusal code = %q, want %q", got, model.CodeScanFailed)
			}
			if got := eventCount(t, st); got != beforeEvents {
				t.Fatalf("event count after failed scan = %d, want %d", got, beforeEvents)
			}
			if got := allTasks(t, st); !reflect.DeepEqual(got, beforeTasks) {
				t.Fatalf("tasks after failed scan = %#v, want %#v", got, beforeTasks)
			}
		})
	}
}

type guardedWriteCase struct {
	name  string
	write func(*store.Store, int) error
}

func guardedWriteCases(ctx context.Context) []guardedWriteCase {
	return []guardedWriteCase{
		{
			name: "add task",
			write: func(st *store.Store, _ int) error {
				_, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "secret candidate"}})
				return err
			},
		},
		{
			name: "set task",
			write: func(st *store.Store, task int) error {
				_, err := st.SetTask(ctx, store.Actor{}, task, model.Patch{Title: stringPtr("secret candidate")})
				return err
			},
		},
		{
			name: "add step",
			write: func(st *store.Store, task int) error {
				_, err := st.Step(ctx, store.Actor{}, task, model.StepOp{Op: "add", Text: "secret candidate"})
				return err
			},
		},
		{
			name: "write note",
			write: func(st *store.Store, task int) error {
				_, err := st.Note(ctx, store.Actor{}, store.NoteInput{NoteData: model.NoteData{Text: "secret candidate"}, Task: task})
				return err
			},
		},
		{
			name: "write decision",
			write: func(st *store.Store, task int) error {
				_, err := st.Decide(ctx, store.Actor{}, store.DecisionInput{DecisionData: model.DecisionData{Text: "secret candidate"}, Task: task})
				return err
			},
		},
	}
}

func guardedStore(t *testing.T, result secretscan.Scanner) (*store.Store, model.Task, func()) {
	t.Helper()
	enabled := false
	st := openStore(t, store.Options{Scanner: func(ctx context.Context, text string) (string, error) {
		if !enabled {
			return "", nil
		}
		return result(ctx, text)
	}})
	task := addOpenTask(t, st)
	return st, task, func() { enabled = true }
}

func secretPattern(t *testing.T, err error) string {
	t.Helper()
	refusal, ok := model.AsRefusal(err)
	if !ok {
		t.Fatalf("error %v is not a model.Refusal", err)
	}
	return refusal.Msg
}
