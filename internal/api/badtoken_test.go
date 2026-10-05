package api_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func noteRequest(text string) api.AppendRequest {
	return api.AppendRequest{
		Actor: store.Actor{Session: "bad-token"},
		Kind:  model.KindNote,
		Note:  &store.NoteInput{NoteData: model.NoteData{Text: text}},
	}
}

func TestClientNamesARefusedTokenWithAStableCode(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	m := testutil.NewClientMachine(t, home)
	if err := config.WriteToken(m.Paths, "not-the-home-token"); err != nil {
		t.Fatal(err)
	}

	_, err := testutil.ClientFor(m).ListTasks(context.Background(), store.Filter{All: true})
	ref, ok := model.AsRefusal(err)
	if !ok || ref.Code != model.CodeBadToken || !strings.Contains(ref.Msg, "refused the token (HTTP 401)") {
		t.Fatalf("ListTasks with a refused token = %v, want a bad-token refusal naming the 401", err)
	}
	if strings.Contains(err.Error(), "not-the-home-token") || strings.Contains(err.Error(), home.Token) {
		t.Fatal("the error carries a token")
	}
}

func TestClientQueuesAJournalWriteTheHomeRefusesTheTokenFor(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	m := testutil.NewClientMachine(t, home)
	if err := config.WriteToken(m.Paths, "not-the-home-token"); err != nil {
		t.Fatal(err)
	}
	var reasons []error
	cfg, err := config.Load(m.Paths.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	c := api.NewClient(api.ClientOptions{Paths: m.Paths, Config: cfg, Unreachable: func(err error) { reasons = append(reasons, err) }})

	// The second append first tries to forward the first, gets the 401 again, and keeps it.
	for _, text := range []string{"first while refused", "second while refused"} {
		ev, queued, err := c.Append(ctx, noteRequest(text))
		if err != nil || !queued || ev.ID != 0 {
			t.Fatalf("Append(%q) with a refused token = (%+v, %t, %v), want (Event{}, true, nil)", text, ev, queued, err)
		}
	}
	if len(reasons) != 2 {
		t.Fatalf("Unreachable was called %d times, want 2", len(reasons))
	}
	for _, reason := range reasons {
		if ref, ok := model.AsRefusal(reason); !ok || ref.Code != model.CodeBadToken {
			t.Fatalf("Unreachable got %v, want the bad-token refusal", reason)
		}
	}
	left, err := os.ReadFile(m.Paths.Outbox())
	if err != nil || strings.Count(string(left), "\n") != 2 {
		t.Fatalf("outbox = %q, %v; want both entries kept", left, err)
	}
	if data, err := home.Client().SessionView(ctx, "bad-token"); err != nil || len(data.Events) != 0 {
		t.Fatalf("the home holds %d events (%v), want none yet", len(data.Events), err)
	}

	if err := config.WriteToken(m.Paths, home.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Flush(ctx); err != nil {
		t.Fatalf("Flush() with the right token: %v", err)
	}
	data, err := home.Client().SessionView(ctx, "bad-token")
	if err != nil || len(data.Events) != 2 {
		t.Fatalf("the home holds %d events (%v) after the token is fixed, want both", len(data.Events), err)
	}
}
