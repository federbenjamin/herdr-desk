package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestClientReportsWhyEachQueuedWriteWasQueued(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	machine := testutil.NewClientMachine(t, home)
	var got []error
	options := api.ClientOptions{Unreachable: func(err error) { got = append(got, err) }}
	note := api.AppendRequest{
		Actor: store.Actor{Session: "unreachable-callback"},
		Kind:  model.KindNote,
		Note:  &store.NoteInput{NoteData: model.NoteData{Text: "sent or queued"}},
	}
	if _, queued, err := machineClient(t, machine, options).Append(ctx, note); err != nil || queued || len(got) != 0 {
		t.Fatalf("Append to a home that answers = (queued %t, %v), Unreachable got %v; want sent and no call", queued, err, got)
	}

	home.Stop()
	client := machineClient(t, machine, options)
	for i := 1; i <= 2; i++ {
		ev, queued, err := client.Append(ctx, note)
		if err != nil || !queued || ev.ID != 0 {
			t.Fatalf("Append %d with the home down = (%+v, %t, %v), want (Event{}, true, nil)", i, ev, queued, err)
		}
		if len(got) != i {
			t.Fatalf("after %d queued writes Unreachable was called %d times", i, len(got))
		}
		ref, ok := model.AsRefusal(got[i-1])
		if !ok || ref.Code != model.CodeHomeUnreachable || !strings.Contains(ref.Msg, "did not answer: ") {
			t.Fatalf("Unreachable got %v, want the home-unreachable refusal naming why", got[i-1])
		}
	}
}
