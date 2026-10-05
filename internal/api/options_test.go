package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestClientTokenOptionIsUsedInPlaceOfTheTokenFile(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	cfg := config.Default()
	cfg.Client.Home = home.Addr

	noFile := testutil.NewMachine(t)
	if _, err := api.NewClient(api.ClientOptions{Paths: noFile.Paths, Config: cfg, Token: home.Token}).Status(ctx); err != nil {
		t.Fatalf("Status with Token and no token file: %v", err)
	}

	withFile := testutil.NewClientMachine(t, home)
	_, err := api.NewClient(api.ClientOptions{Paths: withFile.Paths, Config: cfg, Token: "not-the-home-token"}).Status(ctx)
	if err == nil || !strings.Contains(err.Error(), "refused the token") {
		t.Fatalf("Status with a wrong Token beside the right token file = %v, want the token refused", err)
	}
	if strings.Contains(err.Error(), "not-the-home-token") || strings.Contains(err.Error(), home.Token) {
		t.Fatal("the Status error carries a token")
	}
}

func TestClientReportsWhyEachQueuedWriteWasQueued(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	var got []error
	client := api.NewClient(api.ClientOptions{Paths: home.Paths, Config: config.Default(),
		Unreachable: func(err error) { got = append(got, err) }})
	note := api.AppendRequest{
		Actor: store.Actor{Session: "unreachable-callback"},
		Kind:  model.KindNote,
		Note:  &store.NoteInput{NoteData: model.NoteData{Text: "sent or queued"}},
	}
	if _, queued, err := client.Append(ctx, note); err != nil || queued || len(got) != 0 {
		t.Fatalf("Append to a home that answers = (queued %t, %v), Unreachable got %v; want sent and no call", queued, err, got)
	}

	home.Stop()
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
			t.Fatalf("Unreachable got %v, want the home-unreachable refusal naming the dial error", got[i-1])
		}
	}
}
