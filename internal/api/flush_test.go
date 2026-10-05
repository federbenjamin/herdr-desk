package api_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

// fakeTransport answers each request with what answer returns for it.
type fakeTransport func(method string, params []byte) api.RPCResponse

func (f fakeTransport) RoundTrip(_ context.Context, method string, params []byte) ([]byte, error) {
	return json.Marshal(f(method, params))
}

func (fakeTransport) Close() error { return nil }

// fakeClient is a client machine whose home answers through answer.
func fakeClient(t *testing.T, answer fakeTransport, o api.ClientOptions) (*testutil.Machine, *api.Client) {
	t.Helper()
	m := testutil.NewMachine(t)
	if err := os.MkdirAll(m.Paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	o.Paths, o.Config, o.Transport = m.Paths, config.Default(), answer
	return m, api.NewClient(o)
}

func result(t *testing.T, v any) api.RPCResponse {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return api.RPCResponse{Result: b}
}

func queue(t *testing.T, p config.Paths, texts ...string) {
	t.Helper()
	var b strings.Builder
	for _, text := range texts {
		line, err := json.Marshal(api.AppendRequest{Kind: model.KindNote, Note: &store.NoteInput{NoteData: model.NoteData{Text: text}}})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(p.Outbox(), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendText(t *testing.T, params []byte) string {
	t.Helper()
	var r api.AppendRequest
	if err := json.Unmarshal(params, &r); err != nil || r.Note == nil {
		t.Fatalf("append params %q: %v", params, err)
	}
	return r.Note.Text
}

func TestFlushKeepsWhatARetryMayDeliverAndDropsWhatTheHomeNeverAccepts(t *testing.T) {
	for _, test := range []struct {
		name   string
		answer api.RPCResponse
		keep   bool
		code   string
	}{
		{"scan failed", api.RPCResponse{Refusal: &model.Refusal{Code: model.CodeScanFailed, Msg: "the scanner did not start"}}, true, ""},
		{"home error", api.RPCResponse{Error: &api.RPCError{Message: "the store is locked"}}, true, ""},
		{"secret", api.RPCResponse{Refusal: &model.Refusal{Code: model.CodeSecretDetected, Msg: "aws-access-key"}}, false, model.CodeSecretDetected},
		{"bad request", api.RPCResponse{Error: &api.RPCError{BadRequest: true, Message: "over 1 MiB"}}, false, model.CodeBadInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			var codes []string
			m, c := fakeClient(t, func(method string, _ []byte) api.RPCResponse {
				if method == api.MethodEventsAppend {
					return test.answer
				}
				return result(t, api.TaskList{Tasks: []model.Task{}})
			}, api.ClientOptions{Refused: func(_ model.Kind, r *model.Refusal) { codes = append(codes, r.Code) }})
			queue(t, m.Paths, "the queued note")

			_, err := c.ListTasks(context.Background(), store.Filter{All: true})
			left, rerr := os.ReadFile(m.Paths.Outbox())
			if rerr != nil {
				t.Fatal(rerr)
			}
			if test.keep {
				if err == nil || !strings.Contains(err.Error(), m.Paths.Outbox()) {
					t.Errorf("call error = %v, want the stuck outbox named", err)
				}
				if !strings.Contains(string(left), "the queued note") || len(codes) != 0 {
					t.Errorf("outbox = %q, refused %v; want the entry kept and nothing reported refused", left, codes)
				}
				return
			}
			if err != nil || len(left) != 0 || len(codes) != 1 || codes[0] != test.code {
				t.Errorf("call error = %v, outbox = %q, refused %v; want the entry dropped as %s", err, left, codes, test.code)
			}
		})
	}
}

func TestFlushReportsABadRequestEntryAndSendsTheOnesBehindIt(t *testing.T) {
	var mu sync.Mutex
	var delivered []string
	var codes []string
	m, c := fakeClient(t, func(_ string, params []byte) api.RPCResponse {
		text := appendText(t, params)
		if text == "too big" {
			return api.RPCResponse{Error: &api.RPCError{BadRequest: true, Message: "the request is over 1048576 bytes"}}
		}
		mu.Lock()
		delivered = append(delivered, text)
		mu.Unlock()
		return result(t, model.Event{ID: 1})
	}, api.ClientOptions{Refused: func(_ model.Kind, r *model.Refusal) { codes = append(codes, r.Code) }})
	queue(t, m.Paths, "too big", "behind the big one")

	sent, err := c.Flush(context.Background())
	if err != nil || sent != 1 {
		t.Fatalf("Flush() = (%d, %v), want 1 sent", sent, err)
	}
	if len(codes) != 1 || codes[0] != model.CodeBadInput {
		t.Fatalf("refused = %v, want the bad-request entry reported as bad-input", codes)
	}
	if len(delivered) != 1 || delivered[0] != "behind the big one" {
		t.Fatalf("delivered = %v, want the entry behind the bad one", delivered)
	}
}

func TestFlushLeavesTheOutboxWholeWhenTheRewriteFails(t *testing.T) {
	var m *testutil.Machine
	m, c := fakeClient(t, func(_ string, params []byte) api.RPCResponse {
		if appendText(t, params) == "refused for good" {
			return api.RPCResponse{Refusal: &model.Refusal{Code: model.CodeSecretDetected, Msg: "aws-access-key"}}
		}
		return api.RPCResponse{Error: &api.RPCError{Message: "try later"}}
	}, api.ClientOptions{Refused: func(model.Kind, *model.Refusal) {
		// The state dir stops taking new files, so the rewrite that drops the refused entry fails.
		if err := os.Chmod(m.Paths.StateDir, 0o500); err != nil {
			t.Error(err)
		}
	}})
	queue(t, m.Paths, "refused for good", "not sent yet")
	t.Cleanup(func() { _ = os.Chmod(m.Paths.StateDir, 0o700) })

	if _, err := c.Flush(context.Background()); err == nil {
		t.Fatal("Flush() error = nil, want the failed rewrite reported")
	}
	left, err := os.ReadFile(m.Paths.Outbox())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(left), "not sent yet") {
		t.Fatalf("outbox after a failed rewrite = %q, want the unsent entry kept", left)
	}
}
