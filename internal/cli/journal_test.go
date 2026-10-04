package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/cli"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
)

func runDeskWithEnv(t *testing.T, machine *testutil.Machine, cwd string, args []string, stdin string, extra map[string]string, spawn func(config.Paths) error) commandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return commandResult{
		exit: cli.Run(context.Background(), args, cli.Env{
			Stdin:  strings.NewReader(stdin),
			Stdout: &stdout,
			Stderr: &stderr,
			Getenv: machine.Getenv(extra),
			Cwd:    cwd,
			Spawn:  spawn,
		}),
		stdout: stdout.String(),
		stderr: stderr.String(),
	}
}

func TestNoteRecordsSessionTaskReferenceAndTags(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	task, err := home.Client().AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "journal task"}})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{
		"note", "finished the first pass", "--task", "T1", "--ref", "internal/cli/journal.go", "--branch", "feature/journal", "--tag", "question",
	}, "", map[string]string{"DESK_SESSION": "session-note"}, nil)
	if result.exit != 0 {
		t.Fatalf("note exit = %d, stderr = %q", result.exit, result.stderr)
	}
	if strings.TrimSpace(result.stdout) != "e2" {
		t.Fatalf("note stdout = %q, want e2", result.stdout)
	}

	data, err := home.Client().SessionView(context.Background(), "session-note")
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if len(data.Events) != 1 {
		t.Fatalf("session events = %d, want 1", len(data.Events))
	}
	event := data.Events[0]
	if event.Kind != model.KindNote || event.Task != task.Number || event.Session != "session-note" {
		t.Fatalf("note event = %#v, want note for task %d in session-note", event, task.Number)
	}
	var note model.NoteData
	if err := json.Unmarshal(event.Data, &note); err != nil {
		t.Fatalf("decode note: %v", err)
	}
	if note.Text != "finished the first pass" || note.Ref != "internal/cli/journal.go" {
		t.Fatalf("note data = %#v", note)
	}
	if !hasStrings(event.Tags, model.BranchTag("feature/journal"), "question") {
		t.Fatalf("note tags = %q, want branch and question", event.Tags)
	}
}

func TestOfflineNoteQueuesAndForwardsOnTheNextJournalWrite(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.NewClientMachine(t, home)
	home.Stop()

	queued := runDeskWithEnv(t, client, t.TempDir(), []string{"note", "queued fact"}, "", map[string]string{"DESK_SESSION": "offline-journal"}, nil)
	if queued.exit != 0 || strings.TrimSpace(queued.stdout) != "queued" {
		t.Fatalf("offline note = (%d, %q, %q), want queued success", queued.exit, queued.stdout, queued.stderr)
	}
	if !strings.Contains(queued.stderr, "forward") {
		t.Fatalf("offline note stderr = %q, want forwarding notice", queued.stderr)
	}

	home.Restart(t)
	sent := runDeskWithEnv(t, client, t.TempDir(), []string{"note", "sent after reconnect"}, "", map[string]string{"DESK_SESSION": "offline-journal"}, nil)
	if sent.exit != 0 || strings.TrimSpace(sent.stdout) != "e2" {
		t.Fatalf("reconnected note = (%d, %q, %q), want e2", sent.exit, sent.stdout, sent.stderr)
	}
	view, err := home.Client().SessionView(context.Background(), "offline-journal")
	if err != nil {
		t.Fatalf("read forwarded notes: %v", err)
	}
	if len(view.Events) != 2 {
		t.Fatalf("forwarded events = %d, want 2", len(view.Events))
	}
	for i, want := range []string{"queued fact", "sent after reconnect"} {
		var note model.NoteData
		if err := json.Unmarshal(view.Events[i].Data, &note); err != nil {
			t.Fatalf("decode event %d: %v", i, err)
		}
		if note.Text != want {
			t.Fatalf("event %d text = %q, want %q", i, note.Text, want)
		}
	}
}

func TestForwardingAQueuedSecretReportsItsRefusalOnceWithoutChangingTheCommand(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.NewClientMachine(t, home)
	env := map[string]string{"DESK_SESSION": "refused-queued-note"}
	secret := "AKIA1234567890ABCDEF"
	home.Stop()

	queued := runDeskWithEnv(t, client, t.TempDir(), []string{"note", secret}, "", env, nil)
	if queued.exit != 0 || strings.TrimSpace(queued.stdout) != "queued" {
		t.Fatalf("offline secret note = (%d, %q, %q), want queued success", queued.exit, queued.stdout, queued.stderr)
	}

	home.Restart(t)
	forwarded := runDeskWithEnv(t, client, t.TempDir(), []string{"note", "accepted after refusal"}, "", env, nil)
	if forwarded.exit != 0 || strings.TrimSpace(forwarded.stdout) != "e1" {
		t.Fatalf("forwarding command = (%d, %q, %q), want e1 success", forwarded.exit, forwarded.stdout, forwarded.stderr)
	}
	wantRefusal := "desk: a queued note was refused: secret-detected: the text matches the secret pattern aws-access-key\n"
	if forwarded.stderr != wantRefusal {
		t.Fatalf("forwarding stderr = %q, want %q", forwarded.stderr, wantRefusal)
	}
	if strings.Contains(forwarded.stderr, secret) {
		t.Fatalf("forwarding stderr exposes the queued secret: %q", forwarded.stderr)
	}

	again := runDeskWithEnv(t, client, t.TempDir(), []string{"note", "accepted again"}, "", env, nil)
	if again.exit != 0 || strings.TrimSpace(again.stdout) != "e2" || again.stderr != "" {
		t.Fatalf("second command = (%d, %q, %q), want e2 success with no refusal", again.exit, again.stdout, again.stderr)
	}
}

func TestMergedAndDecisionJournalCommandsPreserveTheirPayloads(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	env := map[string]string{"DESK_SESSION": "journal-payloads"}
	first := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"decide", "first decision"}, "", env, nil)
	if first.exit != 0 || strings.TrimSpace(first.stdout) != "e1" {
		t.Fatalf("first decision = (%d, %q, %q)", first.exit, first.stdout, first.stderr)
	}
	merged := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"note", "--merged", "--branch", "feature/payload", "--pr", "42", "--sha", "deadbeef", "merged safely"}, "", env, nil)
	if merged.exit != 0 || strings.TrimSpace(merged.stdout) != "e2" {
		t.Fatalf("merged note = (%d, %q, %q)", merged.exit, merged.stdout, merged.stderr)
	}
	decision := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"decide", "keep the durable outbox", "--tag", "tunable:outbox", "--replaces", "e1"}, "", env, nil)
	if decision.exit != 0 || strings.TrimSpace(decision.stdout) != "e3" {
		t.Fatalf("decide = (%d, %q, %q)", decision.exit, decision.stdout, decision.stderr)
	}

	view, err := home.Client().SessionView(context.Background(), "journal-payloads")
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if len(view.Events) != 3 || view.Events[1].Kind != model.KindMerged || view.Events[2].Kind != model.KindDecision {
		t.Fatalf("journal kinds = %#v, want decision, merged, then decision", view.Events)
	}
	var merge model.MergedData
	if err := json.Unmarshal(view.Events[1].Data, &merge); err != nil {
		t.Fatalf("decode merged event: %v", err)
	}
	if merge.Branch != "feature/payload" || merge.PR != 42 || merge.SHA != "deadbeef" || merge.Text != "merged safely" {
		t.Fatalf("merged data = %#v", merge)
	}
	var got model.DecisionData
	if err := json.Unmarshal(view.Events[2].Data, &got); err != nil {
		t.Fatalf("decode decision event: %v", err)
	}
	if got.Text != "keep the durable outbox" || got.Replaces != 1 || !hasStrings(view.Events[2].Tags, "tunable:outbox") {
		t.Fatalf("decision event = %#v with data %#v", view.Events[2], got)
	}
}

func TestSessionPrefersFlagThenDeskSessionThenConfiguredSessionVariable(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.SessionEnv = "AGENT_SESSION"
	home := testutil.StartHome(t, testutil.HomeOptions{Config: cfg})
	for _, item := range []struct {
		args    []string
		extra   map[string]string
		wantID  string
		content string
	}{
		{args: []string{"note", "configured"}, extra: map[string]string{"AGENT_SESSION": "configured-id"}, wantID: "configured-id", content: "configured"},
		{args: []string{"note", "desk session"}, extra: map[string]string{"DESK_SESSION": "desk-id", "AGENT_SESSION": "configured-id"}, wantID: "desk-id", content: "desk session"},
		{args: []string{"note", "flag session", "--session", "flag-id"}, extra: map[string]string{"DESK_SESSION": "desk-id", "AGENT_SESSION": "configured-id"}, wantID: "flag-id", content: "flag session"},
	} {
		result := runDeskWithEnv(t, home.Machine, t.TempDir(), item.args, "", item.extra, nil)
		if result.exit != 0 {
			t.Fatalf("note %q exit = %d, stderr = %q", item.content, result.exit, result.stderr)
		}
		view, err := home.Client().SessionView(context.Background(), item.wantID)
		if err != nil || len(view.Events) != 1 {
			t.Fatalf("session %q = %#v, %v; want one event", item.wantID, view, err)
		}
	}

	selected := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"session", "--md"}, "", map[string]string{"DESK_SESSION": "desk-id", "AGENT_SESSION": "configured-id"}, nil)
	if selected.exit != 0 || !strings.Contains(selected.stdout, "desk session") || strings.Contains(selected.stdout, "configured") {
		t.Fatalf("implicit session view = (%d, %q, %q), want DESK_SESSION view", selected.exit, selected.stdout, selected.stderr)
	}
	explicit := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"session", "flag-id", "--md"}, "", map[string]string{"DESK_SESSION": "desk-id"}, nil)
	if explicit.exit != 0 || !strings.Contains(explicit.stdout, "flag session") {
		t.Fatalf("explicit session view = (%d, %q, %q)", explicit.exit, explicit.stdout, explicit.stderr)
	}
}

func hasStrings(got []string, want ...string) bool {
	for _, needle := range want {
		found := false
		for _, value := range got {
			if value == needle {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
