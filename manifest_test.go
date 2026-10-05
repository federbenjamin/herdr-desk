package herdrdesk_test

import (
	"os"
	"slices"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// Every pane event that can end a run must reach the hook: herdr 0.9.1 sends pane.closed only when a pane is closed,
// and pane.exited when its process ends by itself, as a worker's does (probe log, 2026-10-05).
func TestTheManifestRunsTheHookOnEveryPaneEventThatCanEndARun(t *testing.T) {
	data, err := os.ReadFile("herdr-plugin.toml")
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	var m struct {
		Events []struct {
			On      string   `toml:"on"`
			Command []string `toml:"command"`
		} `toml:"events"`
	}
	if err := toml.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse the manifest: %v", err)
	}
	hook := []string{"herdr-desk", "hook", "herdr-event"}
	var on []string
	for _, e := range m.Events {
		if !slices.Equal(e.Command, hook) {
			t.Errorf("event %s runs %q, want %q", e.On, e.Command, hook)
		}
		on = append(on, e.On)
	}
	slices.Sort(on)
	if want := []string{"pane.agent_status_changed", "pane.closed", "pane.exited"}; !slices.Equal(on, want) {
		t.Fatalf("the manifest's events = %q, want %q", on, want)
	}
}
