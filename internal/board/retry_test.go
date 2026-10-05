package board_test

import (
	"context"
	"sync"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/board"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
)

// awayTransport is a home that does not answer while away, then answers every request with an empty result.
type awayTransport struct {
	mu   sync.Mutex
	away bool
}

func (t *awayTransport) RoundTrip(_ context.Context, method string, _ []byte) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.away {
		return nil, &model.Refusal{Code: model.CodeHomeUnreachable, Msg: "the home did not answer"}
	}
	switch method {
	case api.MethodTasksList:
		return []byte(`{"result":{"tasks":[]}}`), nil
	case api.MethodRunsList:
		return []byte(`{"result":[]}`), nil
	}
	return []byte(`{"result":{}}`), nil
}

func (t *awayTransport) Close() error { return nil }

// A board left open while the home was away must recover on the first refresh after the home answers again: its
// Client keeps an unreachable home only until the next refresh asks afresh.
func TestRefreshReachesTheHomeAgainAfterItCameBack(t *testing.T) {
	dir := t.TempDir()
	tr := &awayTransport{away: true}
	c := api.NewClient(api.ClientOptions{Paths: config.Paths{ConfigDir: dir, StateDir: dir, DataDir: dir, CacheDir: dir}, Transport: tr})
	if _, ok := board.Answer(c, board.Refresh{}).(board.Failed); !ok {
		t.Fatal("refresh with the home away and no snapshot did not fail")
	}
	tr.mu.Lock()
	tr.away = false
	tr.mu.Unlock()
	msg := board.Answer(c, board.Refresh{})
	if loaded, ok := msg.(board.Loaded); !ok || loaded.Data.Offline {
		t.Fatalf("refresh after the home came back = %#v, want live data", msg)
	}
}
