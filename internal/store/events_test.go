package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

func TestJournalEventsKeepAgentSessionWhoAndRun(t *testing.T) {
	ctx := context.Background()
	actor := store.Actor{Session: "agent-session", Run: 27}
	cases := []struct {
		name  string
		kind  model.Kind
		tags  []string
		write func(*store.Store) (model.Event, error)
	}{
		{
			name: "note",
			kind: model.KindNote,
			tags: []string{"note-tag"},
			write: func(st *store.Store) (model.Event, error) {
				return st.Note(ctx, actor, store.NoteInput{
					NoteData: model.NoteData{Text: "note"}, Tags: []string{"note-tag"},
				})
			},
		},
		{
			name: "decision",
			kind: model.KindDecision,
			tags: []string{"decision-tag"},
			write: func(st *store.Store) (model.Event, error) {
				return st.Decide(ctx, actor, store.DecisionInput{
					DecisionData: model.DecisionData{Text: "decision"}, Tags: []string{"decision-tag"},
				})
			},
		},
		{
			name: "merged",
			kind: model.KindMerged,
			write: func(st *store.Store) (model.Event, error) {
				return st.Merged(ctx, actor, model.MergedData{Branch: "topic"})
			},
		},
		{
			name: "compacted",
			kind: model.KindCompacted,
			write: func(st *store.Store) (model.Event, error) {
				return st.Compacted(ctx, actor)
			},
		},
		{
			name: "continues",
			kind: model.KindContinues,
			write: func(st *store.Store) (model.Event, error) {
				return st.Continues(ctx, actor, "prior-session")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t, store.Options{})
			before := eventCount(t, st)
			event, err := tc.write(st)
			if err != nil {
				t.Fatalf("append event: %v", err)
			}
			if got := eventCount(t, st); got != before+1 {
				t.Fatalf("event count = %d, want %d", got, before+1)
			}
			if event.Kind != tc.kind {
				t.Fatalf("event kind = %q, want %q", event.Kind, tc.kind)
			}
			if event.Session != actor.Session {
				t.Fatalf("event session = %q, want %q", event.Session, actor.Session)
			}
			if event.Who != model.WhoAgent {
				t.Fatalf("event who = %q, want %q", event.Who, model.WhoAgent)
			}
			if event.Run != actor.Run {
				t.Fatalf("event run = %d, want %d", event.Run, actor.Run)
			}
			if tc.tags != nil && !reflect.DeepEqual(event.Tags, tc.tags) {
				t.Fatalf("event tags = %q, want %q", event.Tags, tc.tags)
			}
		})
	}
}

func TestJournalEventsCarryOnlyTheirAcceptedTags(t *testing.T) {
	ctx := context.Background()
	actor := store.Actor{Session: "agent-session"}
	cases := []struct {
		name  string
		want  []string
		write func(*store.Store) (model.Event, error)
	}{
		{
			name: "note keeps supplied tags",
			want: []string{"note-tag"},
			write: func(st *store.Store) (model.Event, error) {
				return st.Note(ctx, actor, store.NoteInput{
					NoteData: model.NoteData{Text: "note"}, Tags: []string{"note-tag"},
				})
			},
		},
		{
			name: "decision keeps supplied tags",
			want: []string{"decision-tag"},
			write: func(st *store.Store) (model.Event, error) {
				return st.Decide(ctx, actor, store.DecisionInput{
					DecisionData: model.DecisionData{Text: "decision"}, Tags: []string{"decision-tag"},
				})
			},
		},
		{
			name: "merged carries no tags",
			write: func(st *store.Store) (model.Event, error) {
				return st.Merged(ctx, actor, model.MergedData{Branch: "topic"})
			},
		},
		{
			name: "compacted carries no tags",
			write: func(st *store.Store) (model.Event, error) {
				return st.Compacted(ctx, actor)
			},
		},
		{
			name: "continues carries no tags",
			write: func(st *store.Store) (model.Event, error) {
				return st.Continues(ctx, actor, "prior-session")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, err := tc.write(openStore(t, store.Options{}))
			if err != nil {
				t.Fatalf("append event: %v", err)
			}
			if len(tc.want) == 0 {
				if len(event.Tags) != 0 {
					t.Fatalf("event tags = %q, want none", event.Tags)
				}
				return
			}
			if !reflect.DeepEqual(event.Tags, tc.want) {
				t.Fatalf("event tags = %q, want %q", event.Tags, tc.want)
			}
		})
	}
}

func TestMergedAndContinuesRejectInvalidInputsAsBadInput(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		write func(*store.Store) error
	}{
		{
			name: "merged without a branch",
			write: func(st *store.Store) error {
				_, err := st.Merged(ctx, store.Actor{Session: "agent-session"}, model.MergedData{})
				return err
			},
		},
		{
			name: "continues with equal session ids",
			write: func(st *store.Store) error {
				_, err := st.Continues(ctx, store.Actor{Session: "same-session"}, "same-session")
				return err
			},
		},
		{
			name: "continues with an invalid current session id",
			write: func(st *store.Store) error {
				_, err := st.Continues(ctx, store.Actor{Session: ".."}, "prior-session")
				return err
			},
		},
		{
			name: "continues with an invalid predecessor session id",
			write: func(st *store.Store) error {
				_, err := st.Continues(ctx, store.Actor{Session: "next-session"}, ".")
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t, store.Options{})
			before := eventCount(t, st)
			err := tc.write(st)
			refusal, ok := model.AsRefusal(err)
			if !ok {
				t.Fatalf("error = %v, want a refusal with code %q", err, "bad-input")
			}
			if refusal.Code != "bad-input" {
				t.Fatalf("refusal code = %q, want %q", refusal.Code, "bad-input")
			}
			if got := eventCount(t, st); got != before {
				t.Fatalf("event count after refused write = %d, want %d", got, before)
			}
		})
	}
}

func TestContinuesLinksTheCurrentSessionToItsPredecessor(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	prior := store.Actor{Session: "prior-session"}
	priorEvent, err := st.Note(ctx, prior, store.NoteInput{NoteData: model.NoteData{Text: "prior note"}})
	if err != nil {
		t.Fatalf("write prior note: %v", err)
	}
	if _, err := st.Continues(ctx, store.Actor{Session: "next-session"}, prior.Session); err != nil {
		t.Fatalf("record continuation: %v", err)
	}

	data, err := st.SessionEvents(ctx, "next-session")
	if err != nil {
		t.Fatalf("read session events: %v", err)
	}
	if want := []string{"next-session", "prior-session"}; !reflect.DeepEqual(data.Chain, want) {
		t.Fatalf("session chain = %q, want %q", data.Chain, want)
	}
	if !hasEventID(data.Events, priorEvent.ID) {
		t.Fatalf("session events do not include prior event e%d", priorEvent.ID)
	}
}

func TestDecisionCannotReplaceMissingOrNonDecisionEvent(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func(*store.Store) int64
	}{
		{
			name:  "missing event",
			setup: func(*store.Store) int64 { return 99 },
		},
		{
			name: "note event",
			setup: func(st *store.Store) int64 {
				event, err := st.Note(ctx, store.Actor{}, store.NoteInput{NoteData: model.NoteData{Text: "not a decision"}})
				if err != nil {
					t.Fatalf("write note: %v", err)
				}
				return event.ID
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t, store.Options{})
			replaces := tc.setup(st)
			before := eventCount(t, st)
			_, err := st.Decide(ctx, store.Actor{}, store.DecisionInput{DecisionData: model.DecisionData{
				Text: "replacement", Replaces: replaces,
			}})
			if got := refusalCode(t, err); got != model.CodeUnknownEvent {
				t.Fatalf("refusal code = %q, want %q", got, model.CodeUnknownEvent)
			}
			if got := eventCount(t, st); got != before {
				t.Fatalf("event count after refused decision = %d, want %d", got, before)
			}
		})
	}
}

func TestNoteAndDecisionRejectBlankTextWithoutAppendingEvents(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		write func(*store.Store) error
	}{
		{
			name: "note",
			write: func(st *store.Store) error {
				_, err := st.Note(ctx, store.Actor{}, store.NoteInput{NoteData: model.NoteData{Text: " \t "}})
				return err
			},
		},
		{
			name: "decision",
			write: func(st *store.Store) error {
				_, err := st.Decide(ctx, store.Actor{}, store.DecisionInput{DecisionData: model.DecisionData{Text: " \t "}})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t, store.Options{})
			before := eventCount(t, st)
			if got := refusalCode(t, tc.write(st)); got != model.CodeEmptyText {
				t.Fatalf("refusal code = %q, want %q", got, model.CodeEmptyText)
			}
			if got := eventCount(t, st); got != before {
				t.Fatalf("event count after refused write = %d, want %d", got, before)
			}
		})
	}
}

func TestSessionEventsIncludesChainMergedEventsAndTaskJournalFields(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	first := store.Actor{Session: "first-session"}
	task, err := st.AddTask(ctx, first, store.AddTaskInput{
		TaskData: model.TaskData{Title: "chain task"}, Tags: []string{"task-tag"},
	})
	if err != nil {
		t.Fatalf("add chain task: %v", err)
	}
	chainNote, err := st.Note(ctx, first, store.NoteInput{NoteData: model.NoteData{Text: "chain note"}})
	if err != nil {
		t.Fatalf("write chain note: %v", err)
	}
	if _, err := st.SetTask(ctx, store.Actor{}, task.Number, model.Patch{Status: statusPtr(model.StatusDone)}); err != nil {
		t.Fatalf("complete chain task: %v", err)
	}
	if _, err := st.Continues(ctx, store.Actor{Session: "current-session"}, first.Session); err != nil {
		t.Fatalf("link current session: %v", err)
	}
	mergedOne, err := st.Merged(ctx, store.Actor{Session: "other-session"}, model.MergedData{Branch: "topic-one"})
	if err != nil {
		t.Fatalf("write first merged event: %v", err)
	}
	mergedTwo, err := st.Merged(ctx, store.Actor{Session: "another-session"}, model.MergedData{Branch: "topic-two"})
	if err != nil {
		t.Fatalf("write second merged event: %v", err)
	}
	remoteNote, err := st.Note(ctx, store.Actor{Session: "other-session"}, store.NoteInput{NoteData: model.NoteData{Text: "unrelated note"}})
	if err != nil {
		t.Fatalf("write unrelated note: %v", err)
	}

	data, err := st.SessionEvents(ctx, "current-session")
	if err != nil {
		t.Fatalf("read session events: %v", err)
	}
	if want := []string{"current-session", "first-session"}; !reflect.DeepEqual(data.Chain, want) {
		t.Fatalf("session chain = %q, want %q", data.Chain, want)
	}
	if !hasEventID(data.Events, mergedOne.ID) || !hasEventID(data.Events, mergedTwo.ID) {
		t.Fatalf("session events do not include every merged event: %#v", data.Events)
	}
	if !hasEventID(data.Events, chainNote.ID) {
		t.Fatalf("session events do not include chain note e%d", chainNote.ID)
	}
	if hasEventID(data.Events, remoteNote.ID) {
		t.Fatalf("session events include unrelated note e%d", remoteNote.ID)
	}
	assertEventsByID(t, data.Events)

	journalTask, ok := sessionTask(data.Tasks, task.Number)
	if !ok {
		t.Fatalf("session tasks do not include T%d: %#v", task.Number, data.Tasks)
	}
	if journalTask.Created == 0 {
		t.Fatal("session task Created is zero")
	}
	if journalTask.DoneAt <= journalTask.Created {
		t.Fatalf("session task DoneAt = %d, want an event after Created = %d", journalTask.DoneAt, journalTask.Created)
	}
	if want := []string{"task-tag"}; !reflect.DeepEqual(journalTask.Tags, want) {
		t.Fatalf("session task tags = %q, want %q", journalTask.Tags, want)
	}
}

func TestSessionEventChainStopsAtACycle(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	if _, err := st.Continues(ctx, store.Actor{Session: "one"}, "two"); err != nil {
		t.Fatalf("link one to two: %v", err)
	}
	if _, err := st.Continues(ctx, store.Actor{Session: "two"}, "one"); err != nil {
		t.Fatalf("link two to one: %v", err)
	}
	data, err := st.SessionEvents(ctx, "one")
	if err != nil {
		t.Fatalf("read cyclic session chain: %v", err)
	}
	if want := []string{"one", "two"}; !reflect.DeepEqual(data.Chain, want) {
		t.Fatalf("cycle-guarded chain = %q, want %q", data.Chain, want)
	}
}

func TestListRunsStartsEmpty(t *testing.T) {
	runs, err := openStore(t, store.Options{}).ListRuns(context.Background())
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %#v, want no rows in a new store", runs)
	}
}

func TestListRunsDecodesEveryColumnAndNullableCompletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "desk.db")
	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store before seeding runs: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite connection: %v", err)
	}
	started := time.Date(2026, time.October, 4, 15, 30, 0, 123, time.UTC)
	ended := started.Add(2 * time.Minute)
	_, err = db.Exec(`INSERT INTO runs(id, task, state, root, isolation, model, reason, session, workspace, pane, started_ts, ended_ts, exit)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		4, 11, "running", "/work/project", "worktree", "model-a", "manual", "session-a", "/work/project/.worktrees/a", "pane-a", started.Format(time.RFC3339Nano), nil, nil)
	if err != nil {
		db.Close()
		t.Fatalf("insert active run: %v", err)
	}
	_, err = db.Exec(`INSERT INTO runs(id, task, state, root, isolation, model, reason, session, workspace, pane, started_ts, ended_ts, exit)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		9, 12, "finished", "/work/other", "in-place", "model-b", "retry", "session-b", "/work/other", "pane-b", started.Add(time.Hour).Format(time.RFC3339Nano), ended.Format(time.RFC3339Nano), 17)
	if err != nil {
		db.Close()
		t.Fatalf("insert completed run: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed connection: %v", err)
	}

	st, err = store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	runs, err := st.ListRuns(ctx)
	if err != nil {
		t.Fatalf("list seeded runs: %v", err)
	}
	want := []model.Run{
		{ID: 4, Task: 11, State: "running", Root: "/work/project", Isolation: "worktree", Model: "model-a", Reason: "manual", Session: "session-a", Workspace: "/work/project/.worktrees/a", Pane: "pane-a", StartedTS: started},
		{ID: 9, Task: 12, State: "finished", Root: "/work/other", Isolation: "in-place", Model: "model-b", Reason: "retry", Session: "session-b", Workspace: "/work/other", Pane: "pane-b", StartedTS: started.Add(time.Hour), EndedTS: ended, Exit: 17},
	}
	if !reflect.DeepEqual(runs, want) {
		t.Fatalf("runs = %#v, want %#v", runs, want)
	}
}

func TestDecisionMayReplaceDecisionFromAnotherSession(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	original, err := st.Decide(ctx, store.Actor{Session: "first-session"}, store.DecisionInput{DecisionData: model.DecisionData{Text: "original"}})
	if err != nil {
		t.Fatalf("write original decision: %v", err)
	}
	replacement, err := st.Decide(ctx, store.Actor{Session: "second-session"}, store.DecisionInput{DecisionData: model.DecisionData{Text: "replacement", Replaces: original.ID}})
	if err != nil {
		t.Fatalf("replace decision from another session: %v", err)
	}
	if replacement.Session != "second-session" {
		t.Fatalf("replacement session = %q, want second-session", replacement.Session)
	}
	var data model.DecisionData
	if err := json.Unmarshal(replacement.Data, &data); err != nil {
		t.Fatalf("decode replacement event: %v", err)
	}
	if data.Replaces != original.ID {
		t.Fatalf("replacement target = %d, want e%d", data.Replaces, original.ID)
	}
}

func TestExportEventsWritesOneJSONEventPerLineInIDOrder(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, store.Options{})
	if _, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "task"}}); err != nil {
		t.Fatalf("add task: %v", err)
	}
	if _, err := st.Note(ctx, store.Actor{}, store.NoteInput{NoteData: model.NoteData{Text: "note"}}); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if _, err := st.Decide(ctx, store.Actor{}, store.DecisionInput{DecisionData: model.DecisionData{Text: "decision"}}); err != nil {
		t.Fatalf("write decision: %v", err)
	}

	var output bytes.Buffer
	count, err := st.ExportEvents(ctx, &output)
	if err != nil {
		t.Fatalf("export events: %v", err)
	}
	if !strings.HasSuffix(output.String(), "\n") {
		t.Fatalf("export does not end each event line with a newline: %q", output.String())
	}
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if got := len(lines); got != count {
		t.Fatalf("JSON lines = %d, return count = %d", got, count)
	}
	events := make([]model.Event, 0, len(lines))
	for lineNo, line := range lines {
		var event model.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line %d is not a JSON event: %v\n%s", lineNo+1, err, line)
		}
		events = append(events, event)
	}
	assertEventsByID(t, events)
}

func TestExportEventsReturnsTheWriterError(t *testing.T) {
	st := openStore(t, store.Options{})
	if _, err := st.Note(context.Background(), store.Actor{}, store.NoteInput{NoteData: model.NoteData{Text: "note"}}); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if _, err := st.ExportEvents(context.Background(), failingWriter{}); !errors.Is(err, errWrite) {
		t.Fatalf("export error = %v, want %v", err, errWrite)
	}
}

var errWrite = errors.New("write failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func hasEventID(events []model.Event, id int64) bool {
	for _, event := range events {
		if event.ID == id {
			return true
		}
	}
	return false
}

func assertEventsByID(t *testing.T, events []model.Event) {
	t.Helper()
	for index := 1; index < len(events); index++ {
		if events[index-1].ID >= events[index].ID {
			t.Fatalf("events are not in increasing ID order: %#v", events)
		}
	}
}

func sessionTask(tasks []model.SessionTask, number int) (model.SessionTask, bool) {
	for _, task := range tasks {
		if task.Number == number {
			return task, true
		}
	}
	return model.SessionTask{}, false
}
