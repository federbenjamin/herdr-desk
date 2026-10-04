package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/backup"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

const (
	firstToken  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	secondToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type apiHarness struct {
	paths     config.Paths
	trusted   http.Handler
	untrusted http.Handler
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("request body failed") }

func (failingReadCloser) Close() error { return nil }

func newAPIHarness(t *testing.T, backupFn func(context.Context) (backup.Result, error)) apiHarness {
	t.Helper()

	root := t.TempDir()
	paths := config.Paths{
		ConfigDir: filepath.Join(root, "config", "desk"),
		DataDir:   filepath.Join(root, "data", "desk"),
	}
	if err := os.MkdirAll(paths.ConfigDir, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := config.WriteToken(paths, firstToken); err != nil {
		t.Fatalf("write token: %v", err)
	}

	st, err := store.Open(paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Default()
	cfg.Home.Listen = "127.0.0.1:48123"
	cfg.Runner.Enabled = true
	server := api.NewServer(api.ServerOptions{
		Store:     st,
		Config:    cfg,
		Paths:     paths,
		StartedTS: time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC),
		Backup:    backupFn,
	})
	return apiHarness{
		paths:     paths,
		trusted:   server.Handler(true),
		untrusted: server.Handler(false),
	}
}

func postAPI(t *testing.T, h http.Handler, token, method string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v1/"+method, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return b
}

func requireStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if got := rec.Code; got != want {
		t.Fatalf("status = %d, want %d; body: %s", got, want, rec.Body.String())
	}
}

func TestHandlerServesEveryDocumentedMethod(t *testing.T) {
	backupCalled := false
	h := newAPIHarness(t, func(context.Context) (backup.Result, error) {
		backupCalled = true
		return backup.Result{Events: 3, Committed: true, Pushed: true}, nil
	})

	cases := []struct {
		name    string
		method  string
		body    []byte
		handler http.Handler
	}{
		{name: "list tasks", method: api.MethodTasksList, body: []byte(`{}`), handler: h.untrusted},
		{name: "add task", method: api.MethodTasksAdd, body: []byte(`{"actor":{},"input":{"title":"API task"}}`), handler: h.untrusted},
		{name: "get task", method: api.MethodTasksGet, body: []byte(`{"number":1}`), handler: h.untrusted},
		{name: "set task", method: api.MethodTasksSet, body: []byte(`{"actor":{},"number":1,"patch":{"title":"renamed"}}`), handler: h.untrusted},
		{name: "change steps", method: api.MethodTasksSteps, body: []byte(`{"actor":{},"number":1,"op":{"op":"add","text":"check API"}}`), handler: h.untrusted},
		{name: "append event", method: api.MethodEventsAppend, body: mustJSON(t, api.AppendRequest{
			Kind: model.KindNote,
			Note: &store.NoteInput{NoteData: model.NoteData{Text: "journal entry"}},
		}), handler: h.untrusted},
		{name: "view session", method: api.MethodSessionView, body: []byte(`{"session":"session-one"}`), handler: h.untrusted},
		{name: "list runs", method: api.MethodRunsList, body: []byte(`{}`), handler: h.untrusted},
		{name: "read status", method: api.MethodStatus, body: []byte(`{}`), handler: h.untrusted},
		{name: "run backup on trusted handler", method: api.MethodBackupRun, body: []byte(`{}`), handler: h.trusted},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postAPI(t, tc.handler, firstToken, tc.method, tc.body)
			requireStatus(t, rec, http.StatusOK)
			if !json.Valid(rec.Body.Bytes()) {
				t.Fatalf("response is not JSON: %s", rec.Body.String())
			}
		})
	}
	if !backupCalled {
		t.Fatal("trusted backup.run did not call the configured backup function")
	}
}

func TestUntrustedHandlerRequiresTheCurrentExactToken(t *testing.T) {
	h := newAPIHarness(t, nil)

	for _, tc := range []struct {
		name  string
		token string
	}{
		{name: "missing token"},
		{name: "wrong token", token: secondToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postAPI(t, h.untrusted, tc.token, api.MethodStatus, []byte(`{}`))
			requireStatus(t, rec, http.StatusUnauthorized)
		})
	}

	requireStatus(t, postAPI(t, h.untrusted, firstToken, api.MethodStatus, []byte(`{}`)), http.StatusOK)
	if err := config.WriteToken(h.paths, secondToken); err != nil {
		t.Fatalf("rotate token: %v", err)
	}
	requireStatus(t, postAPI(t, h.untrusted, firstToken, api.MethodStatus, []byte(`{}`)), http.StatusUnauthorized)
	requireStatus(t, postAPI(t, h.untrusted, secondToken, api.MethodStatus, []byte(`{}`)), http.StatusOK)
}

func TestUntrustedHandlerNeverServesBackupRun(t *testing.T) {
	backupCalled := false
	h := newAPIHarness(t, func(context.Context) (backup.Result, error) {
		backupCalled = true
		return backup.Result{}, nil
	})

	rec := postAPI(t, h.untrusted, firstToken, api.MethodBackupRun, []byte(`{}`))
	requireStatus(t, rec, http.StatusBadRequest)
	if backupCalled {
		t.Fatal("untrusted backup.run called the backup function")
	}
}

func TestHandlerRejectsNonPostRequests(t *testing.T) {
	h := newAPIHarness(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/"+api.MethodStatus, nil)
	rec := httptest.NewRecorder()

	h.untrusted.ServeHTTP(rec, req)
	requireStatus(t, rec, http.StatusMethodNotAllowed)
}

func TestUntrustedHandlerRejectsWhenTheTokenCannotBeRead(t *testing.T) {
	h := newAPIHarness(t, nil)
	if err := os.Remove(h.paths.TokenFile()); err != nil {
		t.Fatalf("remove token: %v", err)
	}

	requireStatus(t, postAPI(t, h.untrusted, firstToken, api.MethodStatus, []byte(`{}`)), http.StatusUnauthorized)
}

func TestHandlerRejectsMalformedAndUnknownRequests(t *testing.T) {
	h := newAPIHarness(t, nil)

	for _, tc := range []struct {
		name   string
		method string
		body   []byte
	}{
		{name: "malformed JSON", method: api.MethodStatus, body: []byte(`{"unterminated"`)},
		{name: "unknown method", method: "tasks.remove", body: []byte(`{}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postAPI(t, h.untrusted, firstToken, tc.method, tc.body)
			requireStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestHandlerRejectsUnreadableRequestBodies(t *testing.T) {
	h := newAPIHarness(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/"+api.MethodStatus, nil)
	req.Header.Set("Authorization", "Bearer "+firstToken)
	req.Body = failingReadCloser{}
	rec := httptest.NewRecorder()

	h.untrusted.ServeHTTP(rec, req)
	requireStatus(t, rec, http.StatusBadRequest)
}

func TestHandlerRejectsOnlyBodiesOverOneMiB(t *testing.T) {
	h := newAPIHarness(t, nil)
	const limit = 1 << 20
	const prefix = `{"ignored":"`
	const suffix = `"}`
	atLimit := []byte(prefix + strings.Repeat("x", limit-len(prefix)-len(suffix)) + suffix)

	withinLimit := postAPI(t, h.untrusted, firstToken, api.MethodStatus, atLimit)
	if withinLimit.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("exactly %d byte body was rejected as too large", limit)
	}

	overLimit := append(append([]byte(nil), atLimit...), 'x')
	requireStatus(t, postAPI(t, h.untrusted, firstToken, api.MethodStatus, overLimit), http.StatusRequestEntityTooLarge)
}

func TestHandlerMapsStoreRefusalsToConflictWithTheirStableCode(t *testing.T) {
	h := newAPIHarness(t, nil)

	rec := postAPI(t, h.untrusted, firstToken, api.MethodTasksGet, []byte(`{"number":999}`))
	requireStatus(t, rec, http.StatusConflict)
	var refusal model.Refusal
	if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if refusal.Code != model.CodeUnknownTask {
		t.Fatalf("refusal code = %q, want %q", refusal.Code, model.CodeUnknownTask)
	}
}

func TestHandlerMapsBadInputStoreRefusalsToConflictWithTheirStableCode(t *testing.T) {
	h := newAPIHarness(t, nil)

	rec := postAPI(t, h.untrusted, firstToken, api.MethodTasksSet, []byte(`{
		"actor": {},
		"number": 1,
		"patch": {"status": "not-a-status"}
	}`))
	requireStatus(t, rec, http.StatusConflict)

	var refusal model.Refusal
	if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if refusal.Code != model.CodeBadInput {
		t.Fatalf("refusal code = %q, want %q", refusal.Code, model.CodeBadInput)
	}
}

func TestTrustedHandlerMapsBackupFailuresToTheirHTTPClasses(t *testing.T) {
	withoutBackup := newAPIHarness(t, nil)
	refusal := postAPI(t, withoutBackup.trusted, "", api.MethodBackupRun, []byte(`{}`))
	requireStatus(t, refusal, http.StatusConflict)

	var body model.Refusal
	if err := json.Unmarshal(refusal.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if body.Code != model.CodeBackupOff {
		t.Fatalf("refusal code = %q, want %q", body.Code, model.CodeBackupOff)
	}

	withFailure := newAPIHarness(t, func(context.Context) (backup.Result, error) {
		return backup.Result{}, errors.New("backup failed")
	})
	requireStatus(t, postAPI(t, withFailure.trusted, "", api.MethodBackupRun, []byte(`{}`)), http.StatusInternalServerError)
}

func TestHandlerRejectsAppendRequestsWhoseSetFieldDoesNotMatchKind(t *testing.T) {
	h := newAPIHarness(t, nil)
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
			rec := postAPI(t, h.untrusted, firstToken, api.MethodEventsAppend, mustJSON(t, tc.req))
			requireStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestHandlerAppendsEachAllowedKindWithOnlyItsMatchingField(t *testing.T) {
	h := newAPIHarness(t, nil)

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
			rec := postAPI(t, h.untrusted, firstToken, api.MethodEventsAppend, mustJSON(t, tc.req))
			requireStatus(t, rec, http.StatusOK)
		})
	}
}

func TestHandlerDerivesEventWhoFromActorSessionAndIgnoresJSONWho(t *testing.T) {
	h := newAPIHarness(t, nil)
	body := []byte(`{
		"actor":{"session":"agent-session","who":"user"},
		"kind":"note",
		"note":{"text":"the actor is an agent"}
	}`)

	rec := postAPI(t, h.untrusted, firstToken, api.MethodEventsAppend, body)
	requireStatus(t, rec, http.StatusOK)
	var event model.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.Who != model.WhoAgent {
		t.Fatalf("event who = %q, want %q despite the JSON who field", event.Who, model.WhoAgent)
	}
}
