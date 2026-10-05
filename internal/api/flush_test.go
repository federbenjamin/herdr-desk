package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
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

// fakeHome serves h on a machine's socket, as a home's daemon does.
func fakeHome(t *testing.T, h http.HandlerFunc) *testutil.Machine {
	t.Helper()
	m := testutil.NewMachine(t)
	if err := os.MkdirAll(m.Paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", m.Paths.Socket())
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return m
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

func appendText(t *testing.T, body []byte) string {
	t.Helper()
	var r api.AppendRequest
	if err := json.Unmarshal(body, &r); err != nil || r.Note == nil {
		t.Fatalf("append body %q: %v", body, err)
	}
	return r.Note.Text
}

func TestFlushKeepsWhatARetryMayDeliverAndDropsWhatTheHomeNeverAccepts(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		keep   bool
		code   string
	}{
		{"scan failed", http.StatusConflict, `{"code":"scan-failed","message":"the scanner did not start"}`, true, ""},
		{"server error", http.StatusInternalServerError, `{"message":"an internal error"}`, true, ""},
		{"secret", http.StatusConflict, `{"code":"secret-detected","message":"aws-access-key"}`, false, model.CodeSecretDetected},
		{"bad request", http.StatusBadRequest, `{"message":"does not parse"}`, false, model.CodeBadInput},
		{"too large", http.StatusRequestEntityTooLarge, `{"message":"over 1 MiB"}`, false, model.CodeBadInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := fakeHome(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/"+api.MethodEventsAppend {
					w.WriteHeader(test.status)
					_, _ = io.WriteString(w, test.body)
					return
				}
				_, _ = io.WriteString(w, `{"tasks":[]}`)
			})
			queue(t, m.Paths, "the queued note")
			var codes []string
			c := api.NewClient(api.ClientOptions{Paths: m.Paths, Config: config.Default(),
				Refused: func(_ model.Kind, r *model.Refusal) { codes = append(codes, r.Code) }})

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

func TestFlushReportsAnOversizedEntryAndSendsTheOnesBehindIt(t *testing.T) {
	var mu sync.Mutex
	var delivered []string
	m := fakeHome(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/"+api.MethodEventsAppend {
			_, _ = io.WriteString(w, `{"tasks":[]}`)
			return
		}
		if len(body) > 1<<20 {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = io.WriteString(w, `{"message":"the body is over 1048576 bytes"}`)
			return
		}
		mu.Lock()
		delivered = append(delivered, appendText(t, body))
		mu.Unlock()
		_, _ = io.WriteString(w, `{"id":1}`)
	})
	queue(t, m.Paths, strings.Repeat("x", 1<<20+10), "behind the big one")
	var codes []string
	c := api.NewClient(api.ClientOptions{Paths: m.Paths, Config: config.Default(),
		Refused: func(_ model.Kind, r *model.Refusal) { codes = append(codes, r.Code) }})

	sent, err := c.Flush(context.Background())
	if err != nil || sent != 1 {
		t.Fatalf("Flush() = (%d, %v), want 1 sent", sent, err)
	}
	if len(codes) != 1 || codes[0] != model.CodeBadInput {
		t.Fatalf("refused = %v, want the oversized entry reported as bad-input", codes)
	}
	if len(delivered) != 1 || delivered[0] != "behind the big one" {
		t.Fatalf("delivered = %v, want the entry behind the oversized one", delivered)
	}
}

func TestFlushLeavesTheOutboxWholeWhenTheRewriteFails(t *testing.T) {
	m := fakeHome(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if appendText(t, body) == "refused for good" {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"code":"secret-detected","message":"aws-access-key"}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"try later"}`)
	})
	queue(t, m.Paths, "refused for good", "not sent yet")
	t.Cleanup(func() { _ = os.Chmod(m.Paths.StateDir, 0o700) })
	c := api.NewClient(api.ClientOptions{Paths: m.Paths, Config: config.Default(),
		Refused: func(model.Kind, *model.Refusal) {
			// The state dir stops taking new files, so the rewrite that drops the refused entry fails.
			if err := os.Chmod(m.Paths.StateDir, 0o500); err != nil {
				t.Error(err)
			}
		}})

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
