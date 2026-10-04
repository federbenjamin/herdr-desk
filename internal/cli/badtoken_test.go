package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/testutil"
)

func TestARefusedTokenHasACodeAndAJournalWriteIsQueuedNotLost(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.NewClientMachine(t, home)
	getenv := client.Getenv(nil)
	cwd := t.TempDir()
	requireSuccess(t, runDesk(t, getenv, cwd, "", "add", "-t", "before the rotation", "--desk"))
	if _, err := config.RotateToken(home.Paths); err != nil {
		t.Fatal(err)
	}

	requireRefusal(t, runDesk(t, getenv, cwd, "", "list"), "list", model.CodeBadToken, 3)

	env := map[string]string{"DESK_SESSION": "token-session"}
	for _, args := range [][]string{{"note", "written with a refused token"}, {"decide", "keep working"}} {
		result := runDeskWithEnv(t, client, cwd, args, "", env, nil)
		if result.exit != 0 || strings.TrimSpace(result.stdout) != "queued" || !strings.Contains(result.stderr, "refused the token") {
			t.Fatalf("%v = (%d, %q, %q), want exit 0, queued on stdout, and stderr saying the token was refused", args, result.exit, result.stdout, result.stderr)
		}
	}
	if left, err := os.ReadFile(client.Paths.Outbox()); err != nil || strings.Count(string(left), "\n") != 2 {
		t.Fatalf("outbox = %q, %v; want both entries kept after the second forward got a 401", left, err)
	}

	token, err := config.ReadToken(home.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.WriteToken(client.Paths, token); err != nil {
		t.Fatal(err)
	}
	result := runDeskWithEnv(t, client, cwd, []string{"session", "token-session"}, "", nil, nil)
	requireSuccess(t, result)
	if !strings.Contains(result.stdout, "written with a refused token") || !strings.Contains(result.stdout, "keep working") {
		t.Errorf("session after the token is fixed = %q, want both queued entries delivered", result.stdout)
	}
}

func TestTheHookDoesNotFailAtSessionStartWhenTheTokenIsRefused(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.NewClientMachine(t, home)
	if _, err := config.RotateToken(home.Paths); err != nil {
		t.Fatal(err)
	}
	result := runDeskWithEnv(t, client, t.TempDir(), []string{"hook", "start", "--format", "claude-code"},
		`{"source":"compact","session_id":"hook-bad-token"}`, nil, nil)
	if result.exit != 0 || !strings.Contains(result.stdout, "not loaded") || !strings.Contains(result.stdout, "refused the token") {
		t.Fatalf("hook with a refused token = (%d, %q, %q), want exit 0 and one line saying the journal is not loaded", result.exit, result.stdout, result.stderr)
	}
	if left, err := os.ReadFile(client.Paths.Outbox()); err != nil || !strings.Contains(string(left), "compacted") {
		t.Fatalf("outbox = %q, %v; want the compacted record queued", left, err)
	}
}
