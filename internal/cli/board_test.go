package cli_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

// clientOf returns a client machine of a home served by h on 127.0.0.1.
func clientOf(t *testing.T, h http.HandlerFunc) *testutil.Machine {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	m := testutil.NewMachine(t)
	cfg := config.Default()
	cfg.Client.Home = strings.TrimPrefix(srv.URL, "http://")
	if err := cfg.Save(m.Paths.ConfigFile()); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteToken(m.Paths, "test-token"); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBoardJSONNeedsOnlyTheTaskListAndTheTextBoardAsksStatus(t *testing.T) {
	statusCalls := 0
	machine := clientOf(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/" + api.MethodTasksList:
			_ = json.NewEncoder(w).Encode(api.TaskList{Tasks: []model.Task{{Number: 1, Title: "listed", Status: model.StatusOpen}}})
		case "/v1/" + api.MethodStatus:
			statusCalls++
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"status is down"}`))
		default:
			http.NotFound(w, r)
		}
	})

	result := runDeskWithEnv(t, machine, t.TempDir(), []string{"--json"}, "", nil, nil)
	var tl api.TaskList
	if err := json.Unmarshal([]byte(result.stdout), &tl); err != nil || result.exit != 0 {
		t.Fatalf("herdr-desk --json = (%d, %q, %q): %v", result.exit, result.stdout, result.stderr, err)
	}
	if len(tl.Tasks) != 1 || tl.Tasks[0].Title != "listed" || statusCalls != 0 {
		t.Fatalf("herdr-desk --json = %+v with %d status calls, want the list and no status call", tl, statusCalls)
	}

	text := runDeskWithEnv(t, machine, t.TempDir(), nil, "", nil, nil)
	if text.exit != 3 || statusCalls != 1 {
		t.Fatalf("herdr-desk = (%d, %q, %q) with %d status calls, want the failed status to end it", text.exit, text.stdout, text.stderr, statusCalls)
	}
}

func TestSetupAndClientAddRefuseBadValuesAsUsageWithoutWriting(t *testing.T) {
	machine := testutil.NewMachine(t)
	for _, test := range []struct {
		args  []string
		stdin string
	}{
		{[]string{"setup", "--profile", "other", "--no-herdr"}, ""},
		{[]string{"setup", "--listen", "no-port", "--no-herdr"}, ""},
		{[]string{"client", "add", "no-port"}, "a-token\n"},
	} {
		result := runDeskWithEnv(t, machine, t.TempDir(), test.args, test.stdin, nil, nil)
		if result.exit != 2 || !strings.Contains(result.stderr, model.CodeBadInput) {
			t.Errorf("%v = (%d, %q), want exit 2 with bad-input", test.args, result.exit, result.stderr)
		}
		for _, f := range []string{machine.Paths.ConfigFile(), machine.Paths.TokenFile()} {
			if _, err := os.Stat(f); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%v wrote %s: %v", test.args, f, err)
			}
		}
	}
}
