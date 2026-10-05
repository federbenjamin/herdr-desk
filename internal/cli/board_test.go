package cli_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

// clientOf returns a client machine whose [client] command answers each method with the RPCResponse answers names,
// and the file it logs each method to, one line per request.
func clientOf(t *testing.T, answers map[string]api.RPCResponse) (*testutil.Machine, string) {
	t.Helper()
	m := testutil.NewMachine(t)
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	var script strings.Builder
	script.WriteString("req=$(cat)\ncase \"$req\" in\n")
	for method, resp := range answers {
		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&script, "*'\"method\":\"%s\"'*) echo %s >> '%s'; echo '%s' ;;\n", method, method, calls, b)
	}
	script.WriteString("*) exit 9 ;;\nesac\n")
	path := filepath.Join(dir, "home.sh")
	if err := os.WriteFile(path, []byte(script.String()), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Client.Home = "home"
	cfg.Client.Command = []string{"/bin/sh", path}
	if err := cfg.Save(m.Paths.ConfigFile()); err != nil {
		t.Fatal(err)
	}
	return m, calls
}

// callCount is how many requests of method the clientOf home answered.
func callCount(t *testing.T, calls, method string) int {
	t.Helper()
	b, err := os.ReadFile(calls)
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if line == method {
			n++
		}
	}
	return n
}

func TestBoardJSONNeedsOnlyTheTaskListAndTheTextBoardAsksStatus(t *testing.T) {
	list, err := json.Marshal(api.TaskList{Tasks: []model.Task{{Number: 1, Title: "listed", Status: model.StatusOpen}}})
	if err != nil {
		t.Fatal(err)
	}
	machine, calls := clientOf(t, map[string]api.RPCResponse{
		api.MethodTasksList: {Result: list},
		api.MethodStatus:    {Error: &api.RPCError{Message: "status is down"}},
	})

	result := runDeskWithEnv(t, machine, t.TempDir(), []string{"--json"}, "", nil)
	var tl api.TaskList
	if err := json.Unmarshal([]byte(result.stdout), &tl); err != nil || result.exit != 0 {
		t.Fatalf("herdr-desk --json = (%d, %q, %q): %v", result.exit, result.stdout, result.stderr, err)
	}
	if n := callCount(t, calls, api.MethodStatus); len(tl.Tasks) != 1 || tl.Tasks[0].Title != "listed" || n != 0 {
		t.Fatalf("herdr-desk --json = %+v with %d status calls, want the list and no status call", tl, n)
	}

	text := runDeskWithEnv(t, machine, t.TempDir(), nil, "", nil)
	if n := callCount(t, calls, api.MethodStatus); text.exit != 3 || n != 1 {
		t.Fatalf("herdr-desk = (%d, %q, %q) with %d status calls, want the failed status to end it", text.exit, text.stdout, text.stderr, n)
	}
}

func TestSetupAndClientAddRefuseBadValuesAsUsageWithoutWriting(t *testing.T) {
	machine := testutil.NewMachine(t)
	for _, args := range [][]string{
		{"setup", "--profile", "other", "--no-herdr"},
		{"client", "add", "--", "-oProxyCommand=sh"},
		{"client", "add", "two words"},
	} {
		result := runDeskWithEnv(t, machine, t.TempDir(), args, "", nil)
		if result.exit != 2 || !strings.Contains(result.stderr, model.CodeBadInput) {
			t.Errorf("%v = (%d, %q), want exit 2 with bad-input", args, result.exit, result.stderr)
		}
		if _, err := os.Stat(machine.Paths.ConfigFile()); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%v wrote %s: %v", args, machine.Paths.ConfigFile(), err)
		}
	}
}
