package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/backup"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func newServer(t *testing.T, backupFn func(context.Context) (backup.Result, error)) *api.Server {
	t.Helper()

	root := t.TempDir()
	paths := config.Paths{
		ConfigDir: filepath.Join(root, "config", "herdr-desk"),
		StateDir:  filepath.Join(root, "state", "herdr-desk"),
		DataDir:   filepath.Join(root, "data", "herdr-desk"),
	}
	st, err := store.Open(paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Default()
	cfg.Runner.Enabled = true
	return api.NewServer(api.ServerOptions{Store: st, Config: cfg, Paths: paths, Backup: backupFn})
}

// answer sends one request to srv and decodes its RPCResponse.
func answer(t *testing.T, srv *api.Server, method string, params []byte) api.RPCResponse {
	t.Helper()

	b, err := srv.RoundTrip(context.Background(), method, params)
	if err != nil {
		t.Fatalf("RoundTrip(%s): %v", method, err)
	}
	var resp api.RPCResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("decode the response %s: %v", b, err)
	}
	return resp
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return b
}

func requireResult(t *testing.T, resp api.RPCResponse) {
	t.Helper()
	if resp.Refusal != nil || resp.Error != nil || !json.Valid(resp.Result) {
		t.Fatalf("response = %+v (refusal %+v, error %+v), want a JSON result", resp, resp.Refusal, resp.Error)
	}
}

func requireBadRequest(t *testing.T, resp api.RPCResponse) {
	t.Helper()
	if resp.Error == nil || !resp.Error.BadRequest || resp.Result != nil || resp.Refusal != nil {
		t.Fatalf("response = %+v (error %+v), want a bad-request error only", resp, resp.Error)
	}
}

func requireRefusal(t *testing.T, resp api.RPCResponse, code string) {
	t.Helper()
	if resp.Refusal == nil || resp.Refusal.Code != code || resp.Result != nil || resp.Error != nil {
		t.Fatalf("response = %+v (refusal %+v), want only the refusal %q", resp, resp.Refusal, code)
	}
}

func TestServerAnswersEveryDocumentedMethod(t *testing.T) {
	backupCalled := false
	srv := newServer(t, func(context.Context) (backup.Result, error) {
		backupCalled = true
		return backup.Result{Events: 3, Committed: true, Pushed: true}, nil
	})

	for _, tc := range []struct {
		name   string
		method string
		params []byte
	}{
		{name: "list tasks", method: api.MethodTasksList, params: []byte(`{}`)},
		{name: "add task", method: api.MethodTasksAdd, params: []byte(`{"actor":{},"input":{"title":"API task"}}`)},
		{name: "get task", method: api.MethodTasksGet, params: []byte(`{"number":1}`)},
		{name: "set task", method: api.MethodTasksSet, params: []byte(`{"actor":{},"number":1,"patch":{"title":"renamed"}}`)},
		{name: "change steps", method: api.MethodTasksSteps, params: []byte(`{"actor":{},"number":1,"op":{"op":"add","text":"check API"}}`)},
		{name: "append event", method: api.MethodEventsAppend, params: mustJSON(t, api.AppendRequest{
			Kind: model.KindNote,
			Note: &store.NoteInput{NoteData: model.NoteData{Text: "journal entry"}},
		})},
		{name: "view session", method: api.MethodSessionView, params: []byte(`{"session":"session-one"}`)},
		{name: "list runs", method: api.MethodRunsList, params: []byte(`{}`)},
		{name: "read status", method: api.MethodStatus, params: []byte(`{}`)},
		{name: "run backup", method: api.MethodBackupRun, params: []byte(`{}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireResult(t, answer(t, srv, tc.method, tc.params))
		})
	}
	if !backupCalled {
		t.Fatal("backup.run did not call the configured backup function")
	}
}

func TestServerStatusShowsNoTickerWhenNoneHoldsTheLock(t *testing.T) {
	resp := answer(t, newServer(t, nil), api.MethodStatus, []byte(`{}`))
	requireResult(t, resp)
	var st api.Status
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		t.Fatal(err)
	}
	if st.Ticker.Running || st.Ticker.PID != 0 || st.Ticker.StartedTS != nil || !st.RunnerOn {
		t.Fatalf("status = %+v, want no ticker and the runner on", st)
	}
}

func TestServerRejectsMalformedAndUnknownRequests(t *testing.T) {
	srv := newServer(t, nil)

	for _, tc := range []struct {
		name   string
		method string
		params []byte
	}{
		{name: "malformed JSON", method: api.MethodStatus, params: []byte(`{"unterminated"`)},
		{name: "no params", method: api.MethodStatus},
		{name: "unknown method", method: "tasks.remove", params: []byte(`{}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireBadRequest(t, answer(t, srv, tc.method, tc.params))
		})
	}
}

func TestServerAnswersStoreRefusalsWithTheirStableCode(t *testing.T) {
	srv := newServer(t, nil)

	requireRefusal(t, answer(t, srv, api.MethodTasksGet, []byte(`{"number":999}`)), model.CodeUnknownTask)
	requireRefusal(t, answer(t, srv, api.MethodTasksSet, []byte(`{"actor":{},"number":1,"patch":{"status":"not-a-status"}}`)), model.CodeBadInput)
}

func TestServerAnswersBackupFailuresInTheirClasses(t *testing.T) {
	requireRefusal(t, answer(t, newServer(t, nil), api.MethodBackupRun, []byte(`{}`)), model.CodeBackupOff)

	withFailure := newServer(t, func(context.Context) (backup.Result, error) {
		return backup.Result{}, errors.New("backup failed")
	})
	resp := answer(t, withFailure, api.MethodBackupRun, []byte(`{}`))
	if resp.Error == nil || resp.Error.BadRequest {
		t.Fatalf("failed backup = %+v, want an error a retry may clear", resp)
	}
}

func TestServerRejectsAppendRequestsWhoseSetFieldDoesNotMatchKind(t *testing.T) {
	srv := newServer(t, nil)
	note := &store.NoteInput{NoteData: model.NoteData{Text: "note"}}
	decision := &store.DecisionInput{DecisionData: model.DecisionData{Text: "decision"}}
	merged := &model.MergedData{Branch: "topic"}

	cases := []struct {
		name string
		req  api.AppendRequest
	}{
		{name: "note with no note", req: api.AppendRequest{Kind: model.KindNote}},
		{name: "note with decision", req: api.AppendRequest{Kind: model.KindNote, Decision: decision}},
		{name: "decision with no decision", req: api.AppendRequest{Kind: model.KindDecision}},
		{name: "decision with note", req: api.AppendRequest{Kind: model.KindDecision, Note: note}},
		{name: "merged with no merged data", req: api.AppendRequest{Kind: model.KindMerged}},
		{name: "merged with note", req: api.AppendRequest{Kind: model.KindMerged, Note: note}},
		{name: "compacted with note", req: api.AppendRequest{Kind: model.KindCompacted, Note: note}},
		{name: "continues with no prior session", req: api.AppendRequest{Kind: model.KindContinues}},
		{name: "continues with note", req: api.AppendRequest{Kind: model.KindContinues, Note: note, From: "prior-session"}},
		{name: "unknown event kind", req: api.AppendRequest{Kind: model.KindTask, Note: note}},
		{name: "multiple event fields", req: api.AppendRequest{Kind: model.KindNote, Note: note, Decision: decision, Merged: merged}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := answer(t, srv, api.MethodEventsAppend, mustJSON(t, tc.req))
			requireBadRequest(t, resp)
		})
	}
}

func TestServerAppendsEachAllowedKindWithOnlyItsMatchingField(t *testing.T) {
	srv := newServer(t, nil)

	cases := []struct {
		name string
		req  api.AppendRequest
	}{
		{
			name: "note",
			req: api.AppendRequest{Kind: model.KindNote, Note: &store.NoteInput{
				NoteData: model.NoteData{Text: "note"},
			}},
		},
		{
			name: "decision",
			req: api.AppendRequest{Kind: model.KindDecision, Decision: &store.DecisionInput{
				DecisionData: model.DecisionData{Text: "decision"},
			}},
		},
		{name: "merged", req: api.AppendRequest{Kind: model.KindMerged, Merged: &model.MergedData{Branch: "topic"}}},
		{name: "compacted", req: api.AppendRequest{Kind: model.KindCompacted}},
		{name: "continues", req: api.AppendRequest{Actor: store.Actor{Session: "current-session"}, Kind: model.KindContinues, From: "prior-session"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := answer(t, srv, api.MethodEventsAppend, mustJSON(t, tc.req))
			requireResult(t, resp)
		})
	}
}

// who is never the caller's to say: a request that names it is refused whole, and the event's who comes from the
// actor's session.
func TestServerDerivesEventWhoFromActorSessionAndRefusesJSONWho(t *testing.T) {
	srv := newServer(t, nil)
	claimed := answer(t, srv, api.MethodEventsAppend, []byte(`{
		"actor":{"session":"agent-session","who":"user"},
		"kind":"note",
		"note":{"text":"the actor is an agent"}
	}`))
	if claimed.Error == nil || !claimed.Error.BadRequest || !strings.Contains(claimed.Error.Message, `unknown field "who"`) {
		t.Fatalf("a request naming who = %+v, want a bad request naming the field", claimed)
	}
	resp := answer(t, srv, api.MethodEventsAppend, []byte(`{
		"actor":{"session":"agent-session"},
		"kind":"note",
		"note":{"text":"the actor is an agent"}
	}`))
	requireResult(t, resp)
	var event model.Event
	if err := json.Unmarshal(resp.Result, &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.Who != model.WhoAgent {
		t.Fatalf("event who = %q, want %q despite the JSON who field", event.Who, model.WhoAgent)
	}
}
