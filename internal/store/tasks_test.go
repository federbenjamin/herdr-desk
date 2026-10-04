package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

func TestOpenCreatesPrivateWALDatabaseWithExpectedSchema(t *testing.T) {
	t.Parallel()

	parent := filepath.Join(t.TempDir(), "state", "nested")
	path := filepath.Join(parent, "desk.db")
	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	parentInfo, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if got := parentInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("parent mode = %04o, want 0700", got)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("database mode = %04o, want 0600", got)
	}

	db := openDatabase(t, path)
	defer db.Close()
	if got := pragmaText(t, db, "journal_mode"); strings.ToLower(got) != "wal" {
		t.Errorf("journal_mode = %q, want WAL", got)
	}
	if got := userTableNames(t, db); !reflect.DeepEqual(got, []string{"events", "runs", "sessions", "steps", "tasks"}) {
		t.Errorf("tables = %v, want the five store tables", got)
	}
	assertColumns(t, db, "events", []string{"id", "ts", "session", "who", "kind", "task", "data", "tags", "run", "v"})
	assertColumns(t, db, "tasks", []string{"number", "title", "notes", "status", "project", "thread", "archived", "root", "isolation", "model", "created_ts", "updated_ts"})
	assertColumns(t, db, "steps", []string{"task", "short_id", "text", "done", "pos"})
	assertColumns(t, db, "runs", []string{"id", "task", "state", "root", "isolation", "model", "reason", "session", "workspace", "pane", "started_ts", "ended_ts", "exit"})
	assertColumns(t, db, "sessions", []string{"id", "continues"})
	if got := pragmaInt(t, db, "user_version"); got < 1 {
		t.Errorf("user_version = %d, want a numbered migration version", got)
	}
}

func TestOpenExistingDatabaseLeavesTaskStateAndMigrationVersionUntouched(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state", "desk.db")
	st := newStoreAt(t, path)
	created := mustAdd(t, st, "keep this task", model.StatusOpen, "")
	before, err := st.GetTask(context.Background(), created.Number)
	if err != nil {
		t.Fatalf("GetTask() before reopen error = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close() before reopen error = %v", err)
	}

	db := openDatabase(t, path)
	beforeVersion := pragmaInt(t, db, "user_version")
	if err := db.Close(); err != nil {
		t.Fatalf("close database inspection connection: %v", err)
	}

	st, err = store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("reopen existing database: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	after, err := st.GetTask(context.Background(), created.Number)
	if err != nil {
		t.Fatalf("GetTask() after reopen error = %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Errorf("task detail changed after reopening existing database:\n got: %#v\nwant: %#v", after, before)
	}
	db = openDatabase(t, path)
	defer db.Close()
	if got := pragmaInt(t, db, "user_version"); got != beforeVersion {
		t.Errorf("user_version after reopen = %d, want unchanged %d", got, beforeVersion)
	}
}

func TestAddTaskStartsNumbersAtOneAndRecordsCreationEvent(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	ctx := context.Background()
	first, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "first", Notes: "details", Thread: "agent", Status: model.StatusReady}, Tags: []string{"branch:main", "urgent"}})
	if err != nil {
		t.Fatalf("first AddTask() error = %v", err)
	}
	second, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "second"}})
	if err != nil {
		t.Fatalf("second AddTask() error = %v", err)
	}
	if first.Number != 1 || second.Number != 2 {
		t.Errorf("task numbers = %d, %d; want 1, 2", first.Number, second.Number)
	}
	if first.Status != model.StatusReady {
		t.Errorf("first status = %q, want %q", first.Status, model.StatusReady)
	}

	detail, err := st.GetTask(ctx, first.Number)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if len(detail.History) != 1 {
		t.Fatalf("creation history length = %d, want 1", len(detail.History))
	}
	event := detail.History[0]
	if event.Kind != model.KindTask || event.Task != first.Number || event.Who != model.WhoUser || event.V != 1 {
		t.Errorf("creation event = %#v, want a version-one user task event for T%d", event, first.Number)
	}
	if !reflect.DeepEqual(event.Tags, []string{"branch:main", "urgent"}) {
		t.Errorf("creation event tags = %v, want input tags", event.Tags)
	}
	var data model.TaskData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatalf("unmarshal task event data: %v", err)
	}
	if !reflect.DeepEqual(data, model.TaskData{Title: "first", Notes: "details", Thread: "agent", Status: model.StatusReady}) {
		t.Errorf("creation event data = %#v, want input task data", data)
	}
}

func TestAddTaskRejectsEmptyTitle(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	_, err := st.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: ""}})
	assertRefusalCode(t, err, model.CodeEmptyTitle)
}

func TestAddTaskRejectsUnknownBareProject(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	_, err := st.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "task", Project: "not-a-known-project"}})
	assertRefusalCode(t, err, model.CodeUnknownProject)
}

func TestAddTaskResolvesOnlyOneKnownBareProject(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	ctx := context.Background()
	known := mustAdd(t, st, "known alpha", model.StatusOpen, "/work/alpha")
	if known.Project != "/work/alpha" {
		t.Fatalf("seed project = %q, want /work/alpha", known.Project)
	}
	resolved, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "resolved alpha", Project: "alpha"}})
	if err != nil {
		t.Fatalf("AddTask(bare known project) error = %v", err)
	}
	if resolved.Project != "/work/alpha" {
		t.Errorf("resolved project = %q, want /work/alpha", resolved.Project)
	}

	if _, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "other alpha", Project: "/other/alpha"}}); err != nil {
		t.Fatalf("AddTask(second absolute alpha) error = %v", err)
	}
	_, err = st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "ambiguous alpha", Project: "alpha"}})
	assertRefusalCode(t, err, model.CodeUnknownProject)
}

func TestSetTaskUpdatesMaterializedTaskAndRecordsOnePatchEvent(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	created := mustAdd(t, st, "before", model.StatusOpen, "")
	ready := model.StatusReady
	title := "after"
	notes := "new notes"
	thread := "worker"
	root := "/work/repo"
	isolation := "worktree"
	modelName := "example-model"
	archived := true
	updated, err := st.SetTask(context.Background(), store.Actor{}, created.Number, model.Patch{
		Status:    &ready,
		Title:     &title,
		Notes:     &notes,
		Thread:    &thread,
		Root:      &root,
		Isolation: &isolation,
		Model:     &modelName,
		Archived:  &archived,
		Ref:       "https://example.test/pr/1",
		Merged:    true,
	})
	if err != nil {
		t.Fatalf("SetTask() error = %v", err)
	}
	if updated.Title != title || updated.Notes != notes || updated.Thread != thread || updated.Root != root || updated.Isolation != isolation || updated.Model != modelName || updated.Status != ready || !updated.Archived {
		t.Errorf("updated task = %#v, want every patched materialized field", updated)
	}

	detail, err := st.GetTask(context.Background(), created.Number)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if len(detail.History) != 2 {
		t.Fatalf("history length after one patch = %d, want 2", len(detail.History))
	}
	event := detail.History[1]
	if event.Kind != model.KindSet || event.Task != created.Number {
		t.Errorf("patch event = %#v, want one set event for T%d", event, created.Number)
	}
	var recorded model.Patch
	if err := json.Unmarshal(event.Data, &recorded); err != nil {
		t.Fatalf("unmarshal set event data: %v", err)
	}
	if recorded.Title == nil || *recorded.Title != title || recorded.Status == nil || *recorded.Status != ready || recorded.Ref != "https://example.test/pr/1" || !recorded.Merged {
		t.Errorf("set event data = %#v, want the applied patch", recorded)
	}
}

func TestSetTaskNoOpKeepsHistoryUnchanged(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	created := mustAdd(t, st, "unchanged", model.StatusOpen, "")
	ctx := context.Background()
	before, err := st.GetTask(ctx, created.Number)
	if err != nil {
		t.Fatalf("GetTask() before no-op error = %v", err)
	}
	returned, err := st.SetTask(ctx, store.Actor{}, created.Number, model.Patch{})
	if err != nil {
		t.Fatalf("SetTask(empty patch) error = %v", err)
	}
	if !reflect.DeepEqual(returned, before.Task) {
		t.Errorf("SetTask(empty patch) = %#v, want existing task %#v", returned, before.Task)
	}
	unchangedTitle := created.Title
	returned, err = st.SetTask(ctx, store.Actor{}, created.Number, model.Patch{Title: &unchangedTitle})
	if err != nil {
		t.Fatalf("SetTask(same value) error = %v", err)
	}
	if !reflect.DeepEqual(returned, before.Task) {
		t.Errorf("SetTask(same value) = %#v, want existing task %#v", returned, before.Task)
	}
	after, err := st.GetTask(ctx, created.Number)
	if err != nil {
		t.Fatalf("GetTask() after no-op error = %v", err)
	}
	if !reflect.DeepEqual(after.History, before.History) {
		t.Errorf("no-op patch changed history:\n got: %#v\nwant: %#v", after.History, before.History)
	}
}

func TestSetTaskRefOnlyWritesOneSetEvent(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	ctx := context.Background()
	created := mustAdd(t, st, "ref only", model.StatusOpen, "")
	updated, err := st.SetTask(ctx, store.Actor{}, created.Number, model.Patch{Ref: "https://example.test/pr/42"})
	if err != nil {
		t.Fatalf("SetTask(ref-only patch) error = %v", err)
	}
	if !reflect.DeepEqual(updated, created) {
		t.Errorf("SetTask(ref-only patch) = %#v, want unchanged task %#v", updated, created)
	}
	detail, err := st.GetTask(ctx, created.Number)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if len(detail.History) != 2 {
		t.Fatalf("history length after ref-only patch = %d, want 2", len(detail.History))
	}
	event := detail.History[1]
	if event.Kind != model.KindSet {
		t.Fatalf("ref-only event kind = %q, want %q", event.Kind, model.KindSet)
	}
	var recorded model.Patch
	if err := json.Unmarshal(event.Data, &recorded); err != nil {
		t.Fatalf("unmarshal ref-only event data: %v", err)
	}
	if recorded.Ref != "https://example.test/pr/42" || recorded.Status != nil || recorded.Title != nil || recorded.Notes != nil || recorded.Thread != nil || recorded.Root != nil || recorded.Isolation != nil || recorded.Model != nil || recorded.Archived != nil || recorded.Merged {
		t.Errorf("ref-only event data = %#v, want only Ref", recorded)
	}
}

func TestTaskWritesRejectBadInputsAsRefusals(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	ctx := context.Background()
	created := mustAdd(t, st, "valid task", model.StatusOpen, "")
	invalidStatus := model.Status("later")
	invalidIsolation := "container"
	cases := []struct {
		name string
		call func() error
	}{
		{
			name: "adding an unknown status",
			call: func() error {
				_, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "invalid status", Status: invalidStatus}})
				return err
			},
		},
		{
			name: "setting an unknown status",
			call: func() error {
				_, err := st.SetTask(ctx, store.Actor{}, created.Number, model.Patch{Status: &invalidStatus})
				return err
			},
		},
		{
			name: "setting an isolation config rejects",
			call: func() error {
				_, err := st.SetTask(ctx, store.Actor{}, created.Number, model.Patch{Isolation: &invalidIsolation})
				return err
			},
		},
		{
			name: "using an unknown step operation",
			call: func() error {
				_, err := st.Step(ctx, store.Actor{}, created.Number, model.StepOp{Op: "move", Text: "invalid"})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertBadInputRefusal(t, tc.call())
		})
	}
}

func TestSetTaskRejectsUnknownTask(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	_, err := st.SetTask(context.Background(), store.Actor{}, 99, model.Patch{})
	assertRefusalCode(t, err, model.CodeUnknownTask)
}

func TestStepChangesAreMaterializedAndLogged(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	ctx := context.Background()
	created := mustAdd(t, st, "steps", model.StatusOpen, "")
	withStep, err := st.Step(ctx, store.Actor{}, created.Number, model.StepOp{Op: "add", Text: "write tests"})
	if err != nil {
		t.Fatalf("Step(add) error = %v", err)
	}
	if got := withStep.Steps; !reflect.DeepEqual(got, []model.Step{{ShortID: "s1", Text: "write tests"}}) {
		t.Errorf("steps after add = %#v, want s1", got)
	}
	toggled, err := st.Step(ctx, store.Actor{}, created.Number, model.StepOp{Op: "toggle", ShortID: "s1"})
	if err != nil {
		t.Fatalf("Step(toggle) error = %v", err)
	}
	if !toggled.Steps[0].Done {
		t.Errorf("step after toggle = %#v, want Done true", toggled.Steps[0])
	}
	renamed, err := st.Step(ctx, store.Actor{}, created.Number, model.StepOp{Op: "rename", ShortID: "s1", Text: "verify tests"})
	if err != nil {
		t.Fatalf("Step(rename) error = %v", err)
	}
	if got := renamed.Steps[0].Text; got != "verify tests" {
		t.Errorf("step text after rename = %q, want %q", got, "verify tests")
	}
	removed, err := st.Step(ctx, store.Actor{}, created.Number, model.StepOp{Op: "remove", ShortID: "s1"})
	if err != nil {
		t.Fatalf("Step(remove) error = %v", err)
	}
	if len(removed.Steps) != 0 {
		t.Errorf("steps after remove = %#v, want none", removed.Steps)
	}

	detail, err := st.GetTask(ctx, created.Number)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if len(detail.History) != 5 {
		t.Fatalf("history length after four step operations = %d, want 5", len(detail.History))
	}
	for i, event := range detail.History[1:] {
		if event.Kind != model.KindStep || event.Task != created.Number {
			t.Errorf("step history event %d = %#v, want step event for T%d", i, event, created.Number)
		}
	}
}

func TestStepIDsAreNeverReusedAfterRemoval(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	ctx := context.Background()
	created := mustAdd(t, st, "step ids", model.StatusOpen, "")
	first, err := st.Step(ctx, store.Actor{}, created.Number, model.StepOp{Op: "add", Text: "first"})
	if err != nil {
		t.Fatalf("first Step(add) error = %v", err)
	}
	if _, err := st.Step(ctx, store.Actor{}, created.Number, model.StepOp{Op: "remove", ShortID: first.Steps[0].ShortID}); err != nil {
		t.Fatalf("Step(remove) error = %v", err)
	}
	second, err := st.Step(ctx, store.Actor{}, created.Number, model.StepOp{Op: "add", Text: "second"})
	if err != nil {
		t.Fatalf("second Step(add) error = %v", err)
	}
	if got := second.Steps[0].ShortID; got != "s2" {
		t.Errorf("step id after removing s1 = %q, want s2", got)
	}
}

func TestStepRejectsUnknownTaskAndStep(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	ctx := context.Background()
	_, err := st.Step(ctx, store.Actor{}, 99, model.StepOp{Op: "add", Text: "missing task"})
	assertRefusalCode(t, err, model.CodeUnknownTask)
	created := mustAdd(t, st, "known task", model.StatusOpen, "")
	_, err = st.Step(ctx, store.Actor{}, created.Number, model.StepOp{Op: "toggle", ShortID: "s404"})
	assertRefusalCode(t, err, model.CodeUnknownStep)
}

func TestListTasksUsesFilterMatchAndOrdersByNumber(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	ctx := context.Background()
	noProject := mustAdd(t, st, "no project", model.StatusOpen, "")
	alpha := mustAdd(t, st, "alpha", model.StatusReady, "/work/alpha")
	_ = mustAdd(t, st, "done", model.StatusDone, "/work/beta")
	archivedLive := mustAdd(t, st, "archived live", model.StatusBlocked, "")
	archivedDone := mustAdd(t, st, "archived done", model.StatusDone, "")
	trueValue := true
	if _, err := st.SetTask(ctx, store.Actor{}, archivedLive.Number, model.Patch{Archived: &trueValue}); err != nil {
		t.Fatalf("archive live task: %v", err)
	}
	if _, err := st.SetTask(ctx, store.Actor{}, archivedDone.Number, model.Patch{Archived: &trueValue}); err != nil {
		t.Fatalf("archive done task: %v", err)
	}

	project := "/work/alpha"
	emptyProject := ""
	cases := []struct {
		name   string
		filter store.Filter
		want   []int
	}{
		{"default live board", store.Filter{}, []int{noProject.Number, alpha.Number}},
		{"one project", store.Filter{Project: &project}, []int{alpha.Number}},
		{"tasks without a project", store.Filter{Project: &emptyProject}, []int{noProject.Number}},
		{"done unarchived", store.Filter{Statuses: []model.Status{model.StatusDone}}, []int{3}},
		{"done archived", store.Filter{Statuses: []model.Status{model.StatusDone}, Archived: true}, []int{archivedDone.Number}},
		{"all tasks", store.Filter{All: true}, []int{1, 2, 3, 4, 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := st.ListTasks(ctx, tc.filter)
			if err != nil {
				t.Fatalf("ListTasks(%#v) error = %v", tc.filter, err)
			}
			if numbers := taskNumbers(got); !reflect.DeepEqual(numbers, tc.want) {
				t.Errorf("ListTasks(%#v) numbers = %v, want %v", tc.filter, numbers, tc.want)
			}
			for _, task := range got {
				if !tc.filter.Match(task) {
					t.Errorf("ListTasks returned T%d, but Filter.Match rejects it", task.Number)
				}
			}
		})
	}
}

func TestFilterMatchRespectsStatusProjectArchiveAndAll(t *testing.T) {
	t.Parallel()

	project := "/work/alpha"
	emptyProject := ""
	cases := []struct {
		name   string
		filter store.Filter
		task   model.Task
		want   bool
	}{
		{"default accepts live unarchived", store.Filter{}, model.Task{Status: model.StatusOpen}, true},
		{"default rejects done", store.Filter{}, model.Task{Status: model.StatusDone}, false},
		{"default rejects archived", store.Filter{}, model.Task{Status: model.StatusReview, Archived: true}, false},
		{"explicit status accepts matching done task", store.Filter{Statuses: []model.Status{model.StatusDone}}, model.Task{Status: model.StatusDone}, true},
		{"explicit status rejects another live state", store.Filter{Statuses: []model.Status{model.StatusReady}}, model.Task{Status: model.StatusOpen}, false},
		{"project matches exactly", store.Filter{Project: &project}, model.Task{Status: model.StatusOpen, Project: project}, true},
		{"project rejects another project", store.Filter{Project: &project}, model.Task{Status: model.StatusOpen, Project: "/work/beta"}, false},
		{"empty project selects no-project tasks", store.Filter{Project: &emptyProject}, model.Task{Status: model.StatusOpen}, true},
		{"archived selects archived live tasks", store.Filter{Archived: true}, model.Task{Status: model.StatusBlocked, Archived: true}, true},
		{"all includes done archived tasks", store.Filter{All: true}, model.Task{Status: model.StatusDone, Archived: true, Project: "/work/beta"}, true},
		{"all keeps the project", store.Filter{All: true, Project: &project}, model.Task{Status: model.StatusDone, Archived: true, Project: "/work/beta"}, false},
		{"all with a project takes its done archived tasks", store.Filter{All: true, Project: &project}, model.Task{Status: model.StatusDone, Archived: true, Project: project}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.filter.Match(tc.task); got != tc.want {
				t.Errorf("Filter.Match(%#v) = %t, want %t", tc.task, got, tc.want)
			}
		})
	}
}

func TestFilterLiveIdentifiesOnlyLiveBoardNarrowing(t *testing.T) {
	t.Parallel()

	project := "/work/alpha"
	cases := []struct {
		name   string
		filter store.Filter
		want   bool
	}{
		{"default live board", store.Filter{}, true},
		{"project narrows live board", store.Filter{Project: &project}, true},
		{"live status subset narrows live board", store.Filter{Statuses: []model.Status{model.StatusReady, model.StatusBlocked}}, true},
		{"done status is not live", store.Filter{Statuses: []model.Status{model.StatusDone}}, false},
		{"mixed live and done statuses are not live", store.Filter{Statuses: []model.Status{model.StatusOpen, model.StatusDone}}, false},
		{"archived board is not live", store.Filter{Archived: true}, false},
		{"all board is not live", store.Filter{All: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.filter.Live(); got != tc.want {
				t.Errorf("Filter.Live() = %t, want %t for %#v", got, tc.want, tc.filter)
			}
		})
	}
}

func TestGetTaskIncludesOrderedHistoryAndRejectsUnknownTask(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	ctx := context.Background()
	created := mustAdd(t, st, "history", model.StatusOpen, "")
	ready := model.StatusReady
	if _, err := st.SetTask(ctx, store.Actor{}, created.Number, model.Patch{Status: &ready}); err != nil {
		t.Fatalf("SetTask() error = %v", err)
	}
	if _, err := st.Step(ctx, store.Actor{}, created.Number, model.StepOp{Op: "add", Text: "one"}); err != nil {
		t.Fatalf("Step() error = %v", err)
	}

	detail, err := st.GetTask(ctx, created.Number)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if len(detail.History) != 3 {
		t.Fatalf("GetTask history length = %d, want 3", len(detail.History))
	}
	if detail.Task.Number != created.Number || detail.Task.Status != ready || !reflect.DeepEqual(detail.Task.Steps, []model.Step{{ShortID: "s1", Text: "one"}}) {
		t.Errorf("GetTask task = %#v, want current task state", detail.Task)
	}
	if got := []model.Kind{detail.History[0].Kind, detail.History[1].Kind, detail.History[2].Kind}; !reflect.DeepEqual(got, []model.Kind{model.KindTask, model.KindSet, model.KindStep}) {
		t.Errorf("GetTask history kinds = %v, want task, set, step", got)
	}
	for i := 1; i < len(detail.History); i++ {
		if detail.History[i-1].ID >= detail.History[i].ID {
			t.Errorf("history ids are not oldest-first: %d then %d", detail.History[i-1].ID, detail.History[i].ID)
		}
	}
	_, err = st.GetTask(ctx, 99)
	assertRefusalCode(t, err, model.CodeUnknownTask)
}

func TestMergedEventCarriesNoTags(t *testing.T) {
	t.Parallel()

	st := newStore(t)
	event, err := st.Merged(context.Background(), store.Actor{}, model.MergedData{Branch: "feature/no-tags"})
	if err != nil {
		t.Fatalf("Merged() error = %v", err)
	}
	if event.Kind != model.KindMerged {
		t.Errorf("merged event kind = %q, want %q", event.Kind, model.KindMerged)
	}
	if len(event.Tags) != 0 {
		t.Errorf("merged event tags = %v, want none", event.Tags)
	}
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	return newStoreAt(t, filepath.Join(t.TempDir(), "state", "desk.db"))
}

func newStoreAt(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(path, store.Options{Now: func() time.Time {
		return time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)
	}})
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mustAdd(t *testing.T, st *store.Store, title string, status model.Status, project string) model.Task {
	t.Helper()
	task, err := st.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: title, Status: status, Project: project}})
	if err != nil {
		t.Fatalf("AddTask(%q) error = %v", title, err)
	}
	return task
}

func assertRefusalCode(t *testing.T, err error, want string) {
	t.Helper()
	refusal, ok := model.AsRefusal(err)
	if !ok {
		t.Fatalf("error = %v, want *model.Refusal with code %q", err, want)
	}
	if refusal.Code != want {
		t.Errorf("refusal code = %q, want %q", refusal.Code, want)
	}
}

func assertBadInputRefusal(t *testing.T, err error) {
	t.Helper()
	refusal, ok := model.AsRefusal(err)
	if !ok {
		t.Fatalf("error = %v, want *model.Refusal with code %q", err, "bad-input")
	}
	if refusal.Code != "bad-input" {
		t.Errorf("refusal code = %q, want %q", refusal.Code, "bad-input")
	}
}

func openDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(%q) error = %v", path, err)
	}
	return db
}

func pragmaText(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(fmt.Sprintf("PRAGMA %s", name)).Scan(&value); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return value
}

func pragmaInt(t *testing.T, db *sql.DB, name string) int {
	t.Helper()
	var value int
	if err := db.QueryRow(fmt.Sprintf("PRAGMA %s", name)).Scan(&value); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return value
}

func userTableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		t.Fatalf("query table names: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table names: %v", err)
	}
	sort.Strings(names)
	return names
}

func assertColumns(t *testing.T, db *sql.DB, table string, want []string) {
	t.Helper()
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info(%s): %v", table, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("columns for %s = %v, want %v", table, got, want)
	}
}

func taskNumbers(tasks []model.Task) []int {
	numbers := make([]int, len(tasks))
	for i, task := range tasks {
		numbers[i] = task.Number
	}
	return numbers
}
