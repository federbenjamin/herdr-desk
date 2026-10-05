package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func startSessionView(t *testing.T, m *testutil.Machine, session string) string {
	t.Helper()
	input := fmt.Sprintf(`{"source":"startup","session_id":%q}`, session)
	result := runDeskWithEnv(t, m, t.TempDir(), []string{"hook", "start", "--format", "claude-code"}, input, nil, nil)
	requireSuccess(t, result)
	return filepath.Join(m.Paths.SessionsDir(), session+".md")
}

func readView(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEveryWriteBySessionRefreshesTheViewFileTheHookNamed(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.NewClientMachine(t, home)
	for _, machine := range []struct {
		name string
		m    *testutil.Machine
	}{{"home", home.Machine}, {"client", client}} {
		t.Run(machine.name, func(t *testing.T) {
			session := "view-" + machine.name
			path := startSessionView(t, machine.m, session)
			env := map[string]string{"DESK_SESSION": session}
			run := func(args ...string) string {
				t.Helper()
				result := runDeskWithEnv(t, machine.m, t.TempDir(), args, "", env, nil)
				requireSuccess(t, result)
				return strings.TrimSpace(result.stdout)
			}
			if got := readView(t, path); strings.Contains(got, "wrote the parser") {
				t.Fatalf("view before any write holds the note: %q", got)
			}

			run("note", "wrote the parser")
			if got := readView(t, path); !strings.Contains(got, "wrote the parser") {
				t.Errorf("view after note = %q, want the note", got)
			}
			run("decide", "picked sqlite over postgres")
			if got := readView(t, path); !strings.Contains(got, "picked sqlite over postgres") {
				t.Errorf("view after decide = %q, want the decision", got)
			}
			task := run("add", "-t", "follow up on the parser", "--desk", "--thread", "agent")
			if got := readView(t, path); !strings.Contains(got, "follow up on the parser") {
				t.Errorf("view after add = %q, want the task", got)
			}
			before := readView(t, path)
			run("set", task, "blocked")
			if got := readView(t, path); got == before {
				t.Errorf("view after set is unchanged: %q", got)
			}
			before = readView(t, path)
			run("edit", task, "--title", "follow up on the lexer")
			if got := readView(t, path); got == before || !strings.Contains(got, "follow up on the lexer") {
				t.Errorf("view after edit = %q, want the new title", got)
			}
			captured := runDeskWithEnv(t, machine.m, t.TempDir(), []string{"capture"}, "captured by the session #agent\n", env, nil)
			requireSuccess(t, captured)
			if got := readView(t, path); !strings.Contains(got, "captured by the session") {
				t.Errorf("view after capture = %q, want the task", got)
			}
			older := map[string]string{"DESK_SESSION": "older-" + machine.name}
			requireSuccess(t, runDeskWithEnv(t, machine.m, t.TempDir(), []string{"note", "found in the older session"}, "", older, nil))
			run("session", "--continues", "older-"+machine.name)
			if got := readView(t, path); !strings.Contains(got, "found in the older session") {
				t.Errorf("view after session --continues = %q, want the older session's note", got)
			}
		})
	}
}

func TestAWriteBySessionWithNoViewFileMakesNoneAndAQueuedWriteLeavesItStale(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.NewClientMachine(t, home)

	env := map[string]string{"DESK_SESSION": "no-hook"}
	requireSuccess(t, runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"note", "no hook ran"}, "", env, nil))
	if _, err := os.Stat(filepath.Join(home.Paths.SessionsDir(), "no-hook.md")); err == nil {
		t.Error("a note by a session whose hook never ran created a view file")
	}

	path := startSessionView(t, client, "queued")
	stale := readView(t, path)
	home.Stop()
	result := runDeskWithEnv(t, client, t.TempDir(), []string{"note", "written offline"}, "", map[string]string{"DESK_SESSION": "queued"}, nil)
	requireSuccess(t, result)
	if strings.TrimSpace(result.stdout) != "queued" {
		t.Fatalf("offline note stdout = %q, want queued", result.stdout)
	}
	if got := readView(t, path); got != stale {
		t.Errorf("view after a queued note changed: %q", got)
	}
}
