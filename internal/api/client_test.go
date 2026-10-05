package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestClientUsesHomeSocketAndClientTCP(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})

	local, err := home.Client().AddTask(ctx, store.Actor{}, store.AddTaskInput{
		TaskData: model.TaskData{Title: "over the socket"},
	})
	if err != nil {
		t.Fatalf("add task over home socket: %v", err)
	}

	clientMachine := testutil.NewClientMachine(t, home)
	remote := testutil.ClientFor(clientMachine)
	overTCP, err := remote.AddTask(ctx, store.Actor{}, store.AddTaskInput{
		TaskData: model.TaskData{Title: "over TCP"},
	})
	if err != nil {
		t.Fatalf("add task over client TCP connection: %v", err)
	}
	if overTCP.Number != local.Number+1 {
		t.Fatalf("TCP task number = %d, want %d after socket task", overTCP.Number, local.Number+1)
	}

	list, err := home.Client().ListTasks(ctx, store.Filter{All: true})
	if err != nil {
		t.Fatalf("list tasks over home socket: %v", err)
	}
	if len(list.Tasks) != 2 {
		t.Fatalf("socket list returned %d tasks, want 2", len(list.Tasks))
	}
}

func TestClientReturnsServerRefusalsAsModelRefusals(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.ClientFor(testutil.NewClientMachine(t, home))

	_, err := client.GetTask(context.Background(), 999)
	if err == nil {
		t.Fatal("GetTask of an unknown task returned nil error")
	}
	refusal, ok := model.AsRefusal(err)
	if !ok {
		t.Fatalf("GetTask error = %T %v, want a model.Refusal", err, err)
	}
	if refusal.Code != model.CodeUnknownTask {
		t.Fatalf("refusal code = %q, want %q", refusal.Code, model.CodeUnknownTask)
	}
}

func TestClientSpawnsThenReturnsHomeUnreachable(t *testing.T) {
	machine := testutil.NewMachine(t)
	spawnCalls := 0
	client := api.NewClient(api.ClientOptions{
		Paths:  machine.Paths,
		Config: config.Default(),
		Spawn: func(config.Paths) error {
			spawnCalls++
			return nil
		},
	})

	_, err := client.ListTasks(context.Background(), store.Filter{})
	if err == nil {
		t.Fatal("ListTasks without a home returned nil error")
	}
	if spawnCalls != 1 {
		t.Fatalf("spawn calls = %d, want 1", spawnCalls)
	}
	refusal, ok := model.AsRefusal(err)
	if !ok {
		t.Fatalf("ListTasks error = %T %v, want a model.Refusal", err, err)
	}
	if refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("refusal code = %q, want %q", refusal.Code, model.CodeHomeUnreachable)
	}
}

func TestClientStatusNeverStartsDaemon(t *testing.T) {
	machine := testutil.NewMachine(t)
	spawnCalls := 0
	client := api.NewClient(api.ClientOptions{
		Paths:  machine.Paths,
		Config: config.Default(),
		Spawn: func(config.Paths) error {
			spawnCalls++
			return errors.New("Spawn must not be called by Status")
		},
	})

	_, err := client.Status(context.Background())
	if err == nil {
		t.Fatal("Status without a home returned nil error")
	}
	if spawnCalls != 0 {
		t.Fatalf("Status started the daemon %d times", spawnCalls)
	}
	refusal, ok := model.AsRefusal(err)
	if !ok || refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("Status error = %v, want %q refusal", err, model.CodeHomeUnreachable)
	}
}

func TestClientLiveListSnapshotsWholeBoardForOfflineFilters(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := home.Client()

	for _, in := range []store.AddTaskInput{
		{TaskData: model.TaskData{Title: "open task", Status: model.StatusOpen}},
		{TaskData: model.TaskData{Title: "ready task", Status: model.StatusReady}},
	} {
		if _, err := client.AddTask(ctx, store.Actor{}, in); err != nil {
			t.Fatalf("add %q: %v", in.Title, err)
		}
	}

	ready, err := client.ListTasks(ctx, store.Filter{Statuses: []model.Status{model.StatusReady}})
	if err != nil {
		t.Fatalf("list ready tasks while home is up: %v", err)
	}
	if ready.Offline || len(ready.Tasks) != 1 || ready.Tasks[0].Title != "ready task" {
		t.Fatalf("online ready list = %#v, want only the ready task", ready)
	}
	if _, err := os.Stat(home.Paths.Snapshot()); err != nil {
		t.Fatalf("live list did not write snapshot %q: %v", home.Paths.Snapshot(), err)
	}

	home.Stop()
	offline, err := client.ListTasks(ctx, store.Filter{Statuses: []model.Status{model.StatusOpen}})
	if err != nil {
		t.Fatalf("list open tasks from snapshot: %v", err)
	}
	if !offline.Offline || offline.SnapshotTS == nil {
		t.Fatalf("offline list = %#v, want Offline with SnapshotTS", offline)
	}
	if len(offline.Tasks) != 1 || offline.Tasks[0].Title != "open task" {
		t.Fatalf("offline open list = %#v, want the open task from the whole-board snapshot", offline.Tasks)
	}
}

func TestClientNeverAnswersNonLiveFiltersFromSnapshot(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := home.Client()
	if _, err := client.ListTasks(ctx, store.Filter{}); err != nil {
		t.Fatalf("write live snapshot: %v", err)
	}

	home.Stop()
	_, err := client.ListTasks(ctx, store.Filter{All: true})
	if err == nil {
		t.Fatal("non-live list returned a snapshot while home was down")
	}
	refusal, ok := model.AsRefusal(err)
	if !ok || refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("non-live list error = %v, want %q refusal", err, model.CodeHomeUnreachable)
	}
}

func TestClientFlushesQueuedEventsBeforeTheNextAppendInOrder(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := home.Client()
	actor := store.Actor{Session: "outbox-order"}
	home.Stop()

	_, wasQueued, err := client.Append(ctx, api.AppendRequest{
		Actor: actor,
		Kind:  model.KindNote,
		Note:  &store.NoteInput{NoteData: model.NoteData{Text: "queued first"}},
	})
	if err != nil {
		t.Fatalf("queue first event: %v", err)
	}
	if !wasQueued {
		t.Fatal("first Append did not report the event queued while home was down")
	}

	home.Restart(t)
	_, wasQueued, err = client.Append(ctx, api.AppendRequest{
		Actor: actor,
		Kind:  model.KindNote,
		Note:  &store.NoteInput{NoteData: model.NoteData{Text: "sent second"}},
	})
	if err != nil {
		t.Fatalf("append after home restart: %v", err)
	}
	if wasQueued {
		t.Fatal("Append after home restart reported queued")
	}

	view, err := client.SessionView(ctx, actor.Session)
	if err != nil {
		t.Fatalf("read flushed session: %v", err)
	}
	if len(view.Events) != 2 {
		t.Fatalf("session events = %d, want 2", len(view.Events))
	}
	for i, want := range []string{"queued first", "sent second"} {
		var data model.NoteData
		if err := json.Unmarshal(view.Events[i].Data, &data); err != nil {
			t.Fatalf("decode event %d: %v", i, err)
		}
		if data.Text != want {
			t.Fatalf("event %d text = %q, want %q", i, data.Text, want)
		}
	}
}

func TestClientFlushDropsRefusedEntryReportsItAndSendsEntriesBehindIt(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	var refusals []struct {
		kind model.Kind
		err  *model.Refusal
	}
	client := api.NewClient(api.ClientOptions{
		Paths:  home.Paths,
		Config: config.Default(),
		Refused: func(kind model.Kind, r *model.Refusal) {
			refusals = append(refusals, struct {
				kind model.Kind
				err  *model.Refusal
			}{kind: kind, err: r})
		},
	})
	actor := store.Actor{Session: "outbox-retain"}
	home.Stop()

	requests := []api.AppendRequest{
		{Actor: actor, Kind: model.KindNote, Note: &store.NoteInput{NoteData: model.NoteData{Text: "sent before refusal"}}},
		{Actor: actor, Kind: model.KindNote, Note: &store.NoteInput{NoteData: model.NoteData{Text: ""}}},
		{Actor: actor, Kind: model.KindNote, Note: &store.NoteInput{NoteData: model.NoteData{Text: "must stay queued"}}},
	}
	for i, request := range requests {
		_, queued, err := client.Append(ctx, request)
		if err != nil {
			t.Fatalf("queue event %d: %v", i, err)
		}
		if !queued {
			t.Fatalf("event %d was not queued while home was down", i)
		}
	}

	home.Restart(t)
	sent, err := client.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if sent != 2 {
		t.Fatalf("Flush sent %d events, want 2 around the refused event", sent)
	}
	if len(refusals) != 1 {
		t.Fatalf("refusals = %d, want 1", len(refusals))
	}
	if refusals[0].kind != model.KindNote || refusals[0].err.Code != model.CodeEmptyText {
		t.Fatalf("refusal = (%q, %#v), want (%q, code %q)", refusals[0].kind, refusals[0].err, model.KindNote, model.CodeEmptyText)
	}

	view, err := client.SessionView(ctx, actor.Session)
	if err != nil {
		t.Fatalf("read flushed session: %v", err)
	}
	if len(view.Events) != 2 {
		t.Fatalf("session events = %d, want 2 entries around the refusal", len(view.Events))
	}
	for i, want := range []string{"sent before refusal", "must stay queued"} {
		var data model.NoteData
		if err := json.Unmarshal(view.Events[i].Data, &data); err != nil {
			t.Fatalf("decode event %d: %v", i, err)
		}
		if data.Text != want {
			t.Fatalf("event %d text = %q, want %q", i, data.Text, want)
		}
	}
	if sent, err := client.Flush(ctx); err != nil || sent != 0 {
		t.Fatalf("second Flush = (%d, %v), want (0, nil)", sent, err)
	}
	if len(refusals) != 1 {
		t.Fatalf("second Flush reported %d refusals, want the original one only", len(refusals))
	}
}

func TestClientFlushDropsUnparseableOutboxLineAndReportsBadInput(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	var refusals []*model.Refusal
	client := api.NewClient(api.ClientOptions{
		Paths:  home.Paths,
		Config: config.Default(),
		Refused: func(_ model.Kind, r *model.Refusal) {
			refusals = append(refusals, r)
		},
	})
	actor := store.Actor{Session: "outbox-bad-input"}
	home.Stop()

	_, queued, err := client.Append(ctx, api.AppendRequest{
		Actor: actor,
		Kind:  model.KindNote,
		Note:  &store.NoteInput{NoteData: model.NoteData{Text: "sent after bad input"}},
	})
	if err != nil || !queued {
		t.Fatalf("queue valid event = (queued %t, err %v), want (true, nil)", queued, err)
	}
	queuedLine, err := os.ReadFile(home.Paths.Outbox())
	if err != nil {
		t.Fatalf("read queued event: %v", err)
	}
	if err := os.WriteFile(home.Paths.Outbox(), append([]byte("{not json}\n"), queuedLine...), 0o600); err != nil {
		t.Fatalf("place malformed outbox line: %v", err)
	}

	home.Restart(t)
	sent, err := client.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if sent != 1 {
		t.Fatalf("Flush sent %d events, want 1 after malformed line", sent)
	}
	if len(refusals) != 1 || refusals[0].Code != model.CodeBadInput {
		t.Fatalf("refusals = %#v, want one %q refusal", refusals, model.CodeBadInput)
	}
	view, err := client.SessionView(ctx, actor.Session)
	if err != nil {
		t.Fatalf("read flushed session: %v", err)
	}
	if len(view.Events) != 1 {
		t.Fatalf("session events = %d, want 1 valid event", len(view.Events))
	}
	if sent, err := client.Flush(ctx); err != nil || sent != 0 {
		t.Fatalf("second Flush = (%d, %v), want (0, nil)", sent, err)
	}
	if len(refusals) != 1 {
		t.Fatalf("second Flush reported %d refusals, want the original one only", len(refusals))
	}
}

func TestClientFlushDropsRefusedEntryWhenRefusedCallbackIsNil(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := home.Client()
	home.Stop()

	_, queued, err := client.Append(ctx, api.AppendRequest{
		Actor: store.Actor{Session: "outbox-nil-refused"},
		Kind:  model.KindNote,
		Note:  &store.NoteInput{NoteData: model.NoteData{}},
	})
	if err != nil || !queued {
		t.Fatalf("queue refused event = (queued %t, err %v), want (true, nil)", queued, err)
	}

	home.Restart(t)
	sent, err := client.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush with nil Refused: %v", err)
	}
	if sent != 0 {
		t.Fatalf("Flush with nil Refused sent %d events, want 0", sent)
	}
	remaining, err := os.ReadFile(home.Paths.Outbox())
	if err != nil {
		t.Fatalf("read outbox after refusal: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("outbox after refusal = %q, want empty", remaining)
	}
}

func TestClientFlushKeepsEveryEntryWhenHomeIsUnreachable(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := home.Client()
	home.Stop()

	_, queued, err := client.Append(ctx, api.AppendRequest{
		Actor: store.Actor{Session: "outbox-unreachable"},
		Kind:  model.KindNote,
		Note:  &store.NoteInput{NoteData: model.NoteData{Text: "keep me queued"}},
	})
	if err != nil || !queued {
		t.Fatalf("queue event = (queued %t, err %v), want (true, nil)", queued, err)
	}
	before, err := os.ReadFile(home.Paths.Outbox())
	if err != nil {
		t.Fatalf("read queued outbox: %v", err)
	}

	sent, err := client.Flush(ctx)
	if sent != 0 {
		t.Fatalf("Flush sent %d events while home was down, want 0", sent)
	}
	refusal, ok := model.AsRefusal(err)
	if !ok || refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("Flush error = %v, want %q refusal", err, model.CodeHomeUnreachable)
	}
	after, err := os.ReadFile(home.Paths.Outbox())
	if err != nil {
		t.Fatalf("read retained outbox: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("outbox after unreachable Flush = %q, want unchanged %q", after, before)
	}
}
