package cli_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestSessionContinuesRecordsTheLinkInTheNewSessionThenRendersTheChain(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	note := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"note", "written by the old session"}, "", map[string]string{"DESK_SESSION": "old-id"})
	if note.exit != 0 {
		t.Fatalf("note = (%d, %q, %q)", note.exit, note.stdout, note.stderr)
	}

	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"session", "new-id", "--continues", "old-id"}, "", nil)
	if result.exit != 0 || !strings.Contains(result.stdout, "written by the old session") {
		t.Fatalf("session new-id --continues old-id = (%d, %q, %q), want the old session's note", result.exit, result.stdout, result.stderr)
	}
	data, err := home.Client().SessionView(context.Background(), "new-id")
	if err != nil {
		t.Fatalf("session view: %v", err)
	}
	links := 0
	for _, ev := range data.Events {
		if ev.Kind == model.KindContinues {
			var d model.ContinuesData
			if err := json.Unmarshal(ev.Data, &d); err != nil || ev.Session != "new-id" || d.From != "old-id" {
				t.Fatalf("continues event = %+v (%s), want new-id continuing old-id", ev, ev.Data)
			}
			links++
		}
	}
	if links != 1 {
		t.Fatalf("continues events = %d, want 1", links)
	}

	asJSON := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"session", "new-id", "--json"}, "", nil)
	var view struct{ Work []struct{ Text string } }
	if err := json.Unmarshal([]byte(asJSON.stdout), &view); err != nil || asJSON.exit != 0 {
		t.Fatalf("session --json = (%d, %q): %v", asJSON.exit, asJSON.stdout, err)
	}
	if len(view.Work) == 0 || view.Work[0].Text != "written by the old session" {
		t.Fatalf("session --json work = %+v, want the old session's note", view.Work)
	}

	bad := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"session", "new-id", "--continues", "../escape"}, "", nil)
	if bad.exit != 2 {
		t.Fatalf("session --continues ../escape exit = %d, want 2; stderr = %q", bad.exit, bad.stderr)
	}
	after, err := home.Client().SessionView(context.Background(), "new-id")
	if err != nil || len(after.Events) != len(data.Events) {
		t.Fatalf("events after a refused --continues = %d (%v), want %d", len(after.Events), err, len(data.Events))
	}

	none := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"session"}, "", nil)
	if none.exit != 2 || none.stdout != "" {
		t.Fatalf("session with no id = (%d, %q, %q), want exit 2", none.exit, none.stdout, none.stderr)
	}
}

func TestSessionJSONPrintsSnakeCaseKeys(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	env := map[string]string{"DESK_SESSION": "keys"}
	for _, args := range [][]string{{"note", "a fact"}, {"decide", "a choice"}, {"add", "-t", "a task", "--desk"}} {
		if r := runDeskWithEnv(t, home.Machine, t.TempDir(), args, "", env); r.exit != 0 {
			t.Fatalf("%v = (%d, %q, %q)", args, r.exit, r.stdout, r.stderr)
		}
	}

	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"session", "keys", "--json"}, "", nil)
	var view map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.stdout), &view); err != nil || result.exit != 0 {
		t.Fatalf("session --json = (%d, %q): %v", result.exit, result.stdout, err)
	}
	if got := sortedKeys(view); got != "decisions session todo work" {
		t.Fatalf("view keys = %q, want decisions session todo work", got)
	}
	wantLine := "event_id hidden ref replaces scope status tags task text ts who"
	for _, section := range []string{"work", "todo", "decisions"} {
		var lines []map[string]json.RawMessage
		if err := json.Unmarshal(view[section], &lines); err != nil || len(lines) != 1 {
			t.Fatalf("%s = %s (%v), want one line", section, view[section], err)
		}
		if got := sortedKeys(lines[0]); got != wantLine {
			t.Errorf("%s line keys = %q, want %q", section, got, wantLine)
		}
	}
}

func sortedKeys(m map[string]json.RawMessage) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, " ")
}

func TestQueuedWritesAndTheHookNameWhyTheHomeDidNotAnswer(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := testutil.NewClientMachine(t, home)
	home.Stop()
	const cause = "testutil: the home is stopped"

	note := runDeskWithEnv(t, client, t.TempDir(), []string{"note", "kept for later"}, "", map[string]string{"DESK_SESSION": "cause"})
	if note.exit != 0 || strings.TrimSpace(note.stdout) != "queued" {
		t.Fatalf("note with the home down = (%d, %q, %q), want queued", note.exit, note.stdout, note.stderr)
	}
	if !strings.Contains(note.stderr, cause) {
		t.Fatalf("note stderr = %q, want the reason the home did not answer", note.stderr)
	}

	hook := runDeskWithEnv(t, client, t.TempDir(), []string{"hook", "start", "--format", "claude-code"}, `{"source":"startup","session_id":"cause"}`, nil)
	if hook.exit != 0 || !strings.Contains(hook.stdout, cause) {
		t.Fatalf("hook with the home down = (%d, %q, %q), want one line with the reason", hook.exit, hook.stdout, hook.stderr)
	}
}
