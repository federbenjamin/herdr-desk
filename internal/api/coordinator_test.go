package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

const coordinatorSession = "coordinator-session"

// recordCoordinator writes the coordinator row the way runner.Coordinator does, through the home's own store.
func recordCoordinator(t *testing.T, home *testutil.Home) {
	t.Helper()
	st, err := store.Open(home.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open the home's store: %v", err)
	}
	defer st.Close()
	if err := st.SetCoordinator(context.Background(), store.Actor{}, model.Coordinator{Session: coordinatorSession, Workspace: "w1", Pane: "p1"}); err != nil {
		t.Fatalf("SetCoordinator: %v", err)
	}
}

func addTasks(t *testing.T, c *api.Client, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := c.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: fmt.Sprintf("task %d", i)}}); err != nil {
			t.Fatalf("AddTask: %v", err)
		}
	}
}

func TestChangesWithNoCoordinatorReturnsEveryEventAndMovesNoCursor(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	c := home.Client()
	addTasks(t, c, 2)

	first, err := c.Changes(ctx, store.Actor{Session: coordinatorSession})
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if first.From != 0 || first.To == 0 || len(first.Events) == 0 || first.LeftOut != 0 {
		t.Fatalf("Changes with no coordinator recorded = %+v, want from 0 and every event", first)
	}
	second, err := c.Changes(ctx, store.Actor{Session: coordinatorSession})
	if err != nil {
		t.Fatalf("Changes again: %v", err)
	}
	if second.From != 0 || len(second.Events) != len(first.Events) {
		t.Fatalf("second Changes = %+v, want the same events from 0: no coordinator is recorded to hold a cursor", second)
	}
}

func TestChangesMovesTheCursorOnlyForTheCoordinatorsOwnSession(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	c := home.Client()
	addTasks(t, c, 2)
	recordCoordinator(t, home)

	for _, reader := range []struct {
		name  string
		actor store.Actor
	}{
		{"a person", store.Actor{}},
		{"another agent session", store.Actor{Session: "someone-else"}},
	} {
		first, err := c.Changes(ctx, reader.actor)
		if err != nil {
			t.Fatalf("%s: Changes: %v", reader.name, err)
		}
		again, err := c.Changes(ctx, reader.actor)
		if err != nil {
			t.Fatalf("%s: Changes again: %v", reader.name, err)
		}
		if first.From != 0 || again.From != 0 || len(again.Events) != len(first.Events) || len(first.Events) == 0 {
			t.Fatalf("%s: reads = %+v then %+v, want both from 0 with the same events: only the coordinator's own call moves the cursor", reader.name, first, again)
		}
	}

	actor := store.Actor{Session: coordinatorSession}
	own, err := c.Changes(ctx, actor)
	if err != nil {
		t.Fatalf("coordinator Changes: %v", err)
	}
	if own.From != 0 || own.To == 0 || len(own.Events) == 0 {
		t.Fatalf("coordinator's first read = %+v, want every event from 0", own)
	}
	empty, err := c.Changes(ctx, actor)
	if err != nil {
		t.Fatalf("coordinator Changes again: %v", err)
	}
	if empty.From != own.To || empty.To != own.To || len(empty.Events) != 0 {
		t.Fatalf("coordinator's second read = %+v, want From = To = %d and no events", empty, own.To)
	}

	addTasks(t, c, 1)
	next, err := c.Changes(ctx, actor)
	if err != nil {
		t.Fatalf("coordinator Changes after a new event: %v", err)
	}
	if next.From != own.To || next.To <= own.To || len(next.Events) == 0 {
		t.Fatalf("coordinator's read after a new task = %+v, want only events after %d", next, own.To)
	}
	for _, e := range next.Events {
		if e.ID <= own.To {
			t.Fatalf("event e%d was already read (cursor %d)", e.ID, own.To)
		}
	}
}

func TestChangesCapsAtOneHundredEventsNewestKeptOldestFirstAndCountsTheRest(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	c := home.Client()
	addTasks(t, c, 105)

	ch, err := c.Changes(ctx, store.Actor{})
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	total := int(ch.To - ch.From)
	if len(ch.Events) != 100 || ch.LeftOut != total-100 || ch.LeftOut < 5 {
		t.Fatalf("Changes = %d events, %d left out of %d, want 100 kept and the rest counted", len(ch.Events), ch.LeftOut, total)
	}
	if ch.Events[len(ch.Events)-1].ID != ch.To {
		t.Fatalf("last event is e%d, want the newest e%d kept", ch.Events[len(ch.Events)-1].ID, ch.To)
	}
	for i := 1; i < len(ch.Events); i++ {
		if ch.Events[i].ID <= ch.Events[i-1].ID {
			t.Fatalf("events are not oldest first at %d: e%d after e%d", i, ch.Events[i].ID, ch.Events[i-1].ID)
		}
	}
}

func TestChangesOnAClientMachineReadsTheHomesEventsOverRPC(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	addTasks(t, home.Client(), 2)
	recordCoordinator(t, home)
	client := testutil.ClientFor(testutil.NewClientMachine(t, home))
	t.Cleanup(func() { client.Close() })

	actor := store.Actor{Session: coordinatorSession}
	first, err := client.Changes(ctx, actor)
	if err != nil {
		t.Fatalf("client Changes: %v", err)
	}
	if first.From != 0 || len(first.Events) == 0 {
		t.Fatalf("client's first read = %+v, want the home's events from 0", first)
	}
	again, err := client.Changes(ctx, actor)
	if err != nil {
		t.Fatalf("client Changes again: %v", err)
	}
	if again.From != first.To || len(again.Events) != 0 {
		t.Fatalf("client's second read = %+v, want the cursor the first read moved on the home (%d)", again, first.To)
	}
}

func TestTheMethodTableHasChangesAndNoCoordinatorGetOrSet(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	cfg, err := config.Load(home.Paths.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	ask := func(method string) api.RPCResponse {
		t.Helper()
		body, _ := json.Marshal(api.RPCRequest{Method: method, Params: json.RawMessage(`{"actor":{}}`)})
		var out strings.Builder
		if err := api.ServeRPC(context.Background(), home.Paths, cfg, strings.NewReader(string(body)), &out); err != nil {
			t.Fatalf("ServeRPC %s: %v", method, err)
		}
		var resp api.RPCResponse
		if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
			t.Fatalf("%s response %q: %v", method, out.String(), err)
		}
		return resp
	}
	if got := ask(api.MethodCoordinatorChanges); got.Error != nil || got.Refusal != nil || len(got.Result) == 0 {
		t.Fatalf("coordinator.changes = %+v, want a result", got)
	}
	// The coordinator is recorded on the home by runner.Coordinator; a remote caller must not be able to write it.
	for _, method := range []string{"coordinator.get", "coordinator.set"} {
		got := ask(method)
		if got.Error == nil || !got.Error.BadRequest || got.Result != nil {
			t.Fatalf("%s = %+v, want a bad_request error: the method does not exist", method, got)
		}
	}
}
