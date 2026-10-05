package api_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/backup"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

// serverClient is a client whose transport is srv, answering in this process.
func serverClient(t *testing.T, srv *api.Server) *api.Client {
	t.Helper()
	return api.NewClient(api.ClientOptions{Paths: testutil.NewMachine(t).Paths, Config: config.Default(), Transport: srv})
}

func TestBackupTheCallerLeavesStillFinishesOnTheHome(t *testing.T) {
	done := make(chan error, 1)
	srv := api.NewServer(api.ServerOptions{Backup: func(ctx context.Context) (backup.Result, error) {
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
		}
		done <- ctx.Err()
		return backup.Result{Events: 7, Committed: true, Pushed: true}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if res, err := serverClient(t, srv).Backup(ctx); err != nil || res.Events != 7 {
		t.Fatalf("Backup() with a caller that left = (%+v, %v), want the finished run", res, err)
	}
	if ctxErr := <-done; ctxErr != nil {
		t.Fatalf("the caller leaving cancelled the backup: %v", ctxErr)
	}
}

func TestServerAnswersAFailureWithItsDetailAsAnErrorARetryMayClear(t *testing.T) {
	m := testutil.NewMachine(t)
	st, err := store.Open(m.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	srv := api.NewServer(api.ServerOptions{Store: st, Paths: m.Paths, Backup: func(context.Context) (backup.Result, error) {
		return backup.Result{}, errors.New("git push: exit status 128: fatal: <remote> does not appear to be a git repository")
	}})
	client := serverClient(t, srv)

	_, err = client.ListTasks(context.Background(), store.Filter{All: true})
	var re *api.RPCError
	if !errors.As(err, &re) || re.BadRequest || !strings.Contains(re.Message, api.MethodTasksList) || !strings.Contains(re.Message, "closed") {
		t.Fatalf("tasks.list on a closed store = %v, want an error naming the method and the cause", err)
	}
	_, err = client.Backup(context.Background())
	if !errors.As(err, &re) || re.BadRequest || !strings.Contains(re.Message, "git push: exit status 128") {
		t.Fatalf("failed backup = %v, want the git step that failed", err)
	}
}
