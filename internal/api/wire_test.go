package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

// skewTransport serves each request on the home through ServeRPC, as `herdr-desk rpc` does, sent in the wire version
// it holds.
type skewTransport struct {
	home    *testutil.Home
	cfg     config.Config
	version int
}

func (s *skewTransport) RoundTrip(ctx context.Context, method string, params []byte) ([]byte, error) {
	req, err := json.Marshal(api.RPCRequest{Version: s.version, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := api.ServeRPC(ctx, s.home.Paths, s.cfg, bytes.NewReader(req), &out); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

func (*skewTransport) Close() error { return nil }

func homeConfig(t *testing.T, home *testutil.Home) config.Config {
	t.Helper()
	cfg, err := config.Load(home.Paths.ConfigFile())
	if err != nil {
		t.Fatalf("load the home's config: %v", err)
	}
	return cfg
}

// A client of another wire version is told so, with both versions, and what it queued stays queued: a refusal for good
// would drop a note the matching binary delivers.
func TestARequestOfAnotherWireVersionIsRefusedAndTheOutboxKeepsItsEntries(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	m := testutil.NewMachine(t)
	if err := os.MkdirAll(m.Paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	tr := &skewTransport{home: home, cfg: homeConfig(t, home), version: api.WireVersion + 1}
	c := api.NewClient(api.ClientOptions{Paths: m.Paths, Config: config.Default(), Transport: tr})
	queue(t, m.Paths, "written by a newer client")

	sent, err := c.Flush(context.Background())
	var re *api.RPCError
	if sent != 0 || !errors.As(err, &re) || re.BadRequest || !strings.Contains(re.Message, fmt.Sprintf("wire version %d and this home speaks %d", api.WireVersion+1, api.WireVersion)) {
		t.Fatalf("Flush across versions = (%d, %v), want nothing sent and an error naming both versions that is not a bad request", sent, err)
	}
	if b, err := os.ReadFile(m.Paths.Outbox()); err != nil || !strings.Contains(string(b), "written by a newer client") {
		t.Fatalf("outbox after the refusal = %q, %v; want the entry kept", b, err)
	}
	tr.version = api.WireVersion
	if sent, err := c.Flush(context.Background()); sent != 1 || err != nil {
		t.Fatalf("Flush once the versions match = (%d, %v), want the kept entry sent", sent, err)
	}
}

// A field the home does not know fails the whole request: nothing of it is applied.
func TestARequestWithAFieldTheHomeDoesNotKnowIsRefusedWhole(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	task, err := home.Client().AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "before"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := homeConfig(t, home)
	for _, input := range []string{
		`{"version":2,"method":"tasks.set","params":{"actor":{},"number":1,"patch":{"title":"after","colour":"red"}}}`,
		`{"version":2,"method":"tasks.set","params":{"actor":{},"number":1,"patch":{"title":"after"}},"priority":"high"}`,
	} {
		var out bytes.Buffer
		if err := api.ServeRPC(context.Background(), home.Paths, cfg, strings.NewReader(input), &out); err != nil {
			t.Fatalf("ServeRPC: %v", err)
		}
		var resp api.RPCResponse
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Error == nil || !resp.Error.BadRequest || !strings.Contains(resp.Error.Message, "unknown field") {
			t.Fatalf("request %s = %+v, want a bad request naming the unknown field", input, resp)
		}
	}
	d, err := home.Client().GetTask(context.Background(), task.Number)
	if err != nil || d.Task.Title != "before" {
		t.Fatalf("task after the refused requests = %#v, %v; want its title untouched", d.Task, err)
	}
}

// slowAwayTransport is a home that, while away, answers only after a delay, as ssh's connect timeout does, and once
// back answers through ServeRPC.
type slowAwayTransport struct {
	skewTransport
	away    bool
	delay   time.Duration
	entered time.Time // when the first request reached the transport while away
}

func (s *slowAwayTransport) RoundTrip(ctx context.Context, method string, params []byte) ([]byte, error) {
	if !s.away {
		return s.skewTransport.RoundTrip(ctx, method, params)
	}
	if s.entered.IsZero() {
		s.entered = time.Now()
	}
	time.Sleep(s.delay) // the connect timeout the request waits out, not a wait for readiness
	return nil, &model.Refusal{Code: model.CodeHomeUnreachable, Msg: "the home did not answer"}
}

// A note queued because the home did not answer is replayed with the time it was written, not the time the connect
// timeout ran out, nor the time it arrived.
func TestAQueuedNoteReplayedLaterKeepsTheTimeItWasWritten(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	m := testutil.NewMachine(t)
	if err := os.MkdirAll(m.Paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	tr := &slowAwayTransport{skewTransport: skewTransport{home: home, cfg: homeConfig(t, home), version: api.WireVersion},
		away: true, delay: 300 * time.Millisecond}
	c := api.NewClient(api.ClientOptions{Paths: m.Paths, Config: config.Default(), Transport: tr})
	ctx := context.Background()
	_, queued, err := c.Append(ctx, api.AppendRequest{Actor: store.Actor{Session: "s-written"}, Kind: model.KindNote,
		Note: &store.NoteInput{NoteData: model.NoteData{Text: "written while the home was away"}}})
	if err != nil || !queued {
		t.Fatalf("Append with the home away = (queued %t, %v), want queued", queued, err)
	}
	tr.away = false
	c.Retry()
	if sent, err := c.Flush(ctx); sent != 1 || err != nil {
		t.Fatalf("Flush once the home is back = (%d, %v), want the note sent", sent, err)
	}
	view, err := home.Client().SessionView(ctx, "s-written")
	if err != nil || len(view.Events) != 1 {
		t.Fatalf("the home's session = %#v, %v; want the one note", view, err)
	}
	if ts := view.Events[0].TS; ts.After(tr.entered) {
		t.Fatalf("the note's ts is %s, after the request reached the transport at %s: not the time it was written", ts, tr.entered)
	}
}
