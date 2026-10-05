package api_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/backup"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

// backupHome serves a server whose backup takes `takes`, and reports through done whether the backup's
// context was cancelled before it finished.
func backupHome(t *testing.T, takes time.Duration) (*testutil.Machine, chan error) {
	t.Helper()
	done := make(chan error, 1)
	srv := api.NewServer(api.ServerOptions{Backup: func(ctx context.Context) (backup.Result, error) {
		select {
		case <-time.After(takes):
		case <-ctx.Done():
		}
		done <- ctx.Err()
		return backup.Result{Events: 7, Committed: true, Pushed: true}, nil
	}})
	return fakeHome(t, srv.Handler(true).ServeHTTP), done
}

func TestBackupOutlastsTheClientTimeout(t *testing.T) {
	m, done := backupHome(t, 300*time.Millisecond)
	c := api.NewClient(api.ClientOptions{Paths: m.Paths, Config: config.Default(), Timeout: 50 * time.Millisecond})

	res, err := c.Backup(context.Background())
	if err != nil || res.Events != 7 {
		t.Fatalf("Backup() = (%+v, %v), want the result of a run longer than the client timeout", res, err)
	}
	if ctxErr := <-done; ctxErr != nil {
		t.Fatalf("the backup's context ended early: %v", ctxErr)
	}
}

func TestBackupTheCallerLeavesStillFinishesOnTheHome(t *testing.T) {
	m, done := backupHome(t, 300*time.Millisecond)
	c := api.NewClient(api.ClientOptions{Paths: m.Paths, Config: config.Default()})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := c.Backup(ctx); err == nil {
		t.Fatal("Backup() with a caller that left = nil error")
	}
	select {
	case ctxErr := <-done:
		if ctxErr != nil {
			t.Fatalf("the caller leaving cancelled the backup: %v", ctxErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the backup did not finish")
	}
}

func TestTCPClientNeverSendsTheTokenThroughAnEnvironmentProxy(t *testing.T) {
	if os.Getenv("DESK_TEST_PROXY_CHILD") == "1" {
		m := testutil.NewMachine(t)
		cfg := config.Default()
		cfg.Client.Home = "desk-home.invalid:7411"
		if err := config.WriteToken(m.Paths, "proxy-test-token"); err != nil {
			t.Fatal(err)
		}
		_, _ = api.NewClient(api.ClientOptions{Paths: m.Paths, Config: cfg, Timeout: time.Second}).Status(context.Background())
		return
	}
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxied.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestTCPClientNeverSendsTheTokenThroughAnEnvironmentProxy$", "-test.count=1")
	cmd.Env = append(os.Environ(), "DESK_TEST_PROXY_CHILD=1", "HTTP_PROXY="+proxy.URL, "http_proxy="+proxy.URL,
		"NO_PROXY=", "no_proxy=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if n := proxied.Load(); n != 0 {
		t.Fatalf("the proxy received %d requests, want none", n)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	out, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(out); log.SetFlags(flags) })
	return &buf
}

func TestServerLogsInternalErrorsAndTokenReadFailuresAndHidesTheDetail(t *testing.T) {
	logged := captureLog(t)
	m := testutil.NewMachine(t)
	st, err := store.Open(m.Paths.DB(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	srv := api.NewServer(api.ServerOptions{Store: st, Paths: m.Paths, Backup: func(context.Context) (backup.Result, error) {
		return backup.Result{}, errors.New("git push: exit status 128: fatal: <remote> does not appear to be a git repository")
	}})

	rec := httptest.NewRecorder()
	srv.Handler(true).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/"+api.MethodTasksList, strings.NewReader("{}")))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "closed") || !strings.Contains(rec.Body.String(), "daemon log") {
		t.Fatalf("500 answer = %d %q, want a generic message", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logged.String(), api.MethodTasksList) || !strings.Contains(logged.String(), "closed") {
		t.Fatalf("daemon log = %q, want the method and the error", logged.String())
	}

	logged.Reset()
	rec = httptest.NewRecorder()
	srv.Handler(true).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/"+api.MethodBackupRun, strings.NewReader("{}")))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "git push: exit status 128") {
		t.Fatalf("failed backup answer = %d %q, want the git step that failed", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logged.String(), api.MethodBackupRun) || !strings.Contains(logged.String(), "git push") {
		t.Fatalf("daemon log = %q, want the failed backup", logged.String())
	}

	logged.Reset()
	req := httptest.NewRequest(http.MethodPost, "/v1/"+api.MethodStatus, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer some-token")
	rec = httptest.NewRecorder()
	srv.Handler(false).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token file: status = %d, want 401", rec.Code)
	}
	if !strings.Contains(logged.String(), "token") || strings.Contains(logged.String(), "some-token") {
		t.Fatalf("daemon log = %q, want the token read failure without the token sent", logged.String())
	}
}
