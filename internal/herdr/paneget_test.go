package herdr_test

import (
	"context"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
)

func TestPaneReturnsTheListedPaneIncludingItsAgentSession(t *testing.T) {
	client := newFakeClient(t)
	created := createWorkspace(t, client)
	runFake(t, "pane", "report-agent", created.Pane, "--source", "test", "--agent", "test", "--state", "blocked", "--agent-session-id", "session-123")

	pane, found, err := client.Pane(context.Background(), created.Pane)
	if err != nil || !found {
		t.Fatalf("Pane() = (%#v, %t, %v), want the pane found", pane, found, err)
	}
	if pane.ID != created.Pane || pane.Workspace != created.Workspace || pane.Status != "blocked" || pane.Session != "session-123" {
		t.Fatalf("Pane() = %#v, want the pane identity, blocked status, and agent session", pane)
	}
}

func TestPaneTreatsPaneNotFoundAsAnAbsentPane(t *testing.T) {
	client := herdr.Client{Bin: writeHerdrStub(t, "printf '%s' '{\"error\":{\"code\":\"pane_not_found\"}}' >&2\nexit 1")}

	pane, found, err := client.Pane(context.Background(), "gone")
	if err != nil || found || pane != (herdr.Pane{}) {
		t.Fatalf("Pane(gone) = (%#v, %t, %v), want (zero, false, nil)", pane, found, err)
	}
}

func TestPaneRejectsUnexpectedRefusalsAndIncompleteAnswers(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"other refusal", "printf '%s' '{\"error\":{\"code\":\"permission_denied\"}}' >&2\nexit 1"},
		{"no pane", "printf '%s' '{\"result\":{}}'"},
		{"null pane", "printf '%s' '{\"result\":{\"pane\":null}}'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := herdr.Client{Bin: writeHerdrStub(t, tc.body)}
			pane, found, err := client.Pane(context.Background(), "p1")
			if err == nil || found || pane != (herdr.Pane{}) || !strings.Contains(err.Error(), "pane get") {
				t.Fatalf("Pane() = (%#v, %t, %v), want a pane-get error and no pane", pane, found, err)
			}
		})
	}
}
