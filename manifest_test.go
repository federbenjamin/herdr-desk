package herdrdesk_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
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

// Claude Code caches a plugin by its version, so a changed skill under an unchanged version never reaches an installed
// copy. The plugin's version, the manifest's, and the one the installer stamps on its build must be the same string.
func TestThePluginManifestAndTheInstallerAgreeOnTheVersion(t *testing.T) {
	data, err := os.ReadFile("herdr-plugin.toml")
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	var manifest struct {
		Version string `toml:"version"`
	}
	if err := toml.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse the manifest: %v", err)
	}
	if manifest.Version == "" {
		t.Fatal("herdr-plugin.toml has no version")
	}

	raw, err := os.ReadFile("profiles/claude-code/.claude-plugin/plugin.json")
	if err != nil {
		t.Fatalf("read the Claude Code plugin: %v", err)
	}
	var plugin struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &plugin); err != nil {
		t.Fatalf("parse the Claude Code plugin: %v", err)
	}
	if plugin.Version != manifest.Version {
		t.Errorf("plugin.json version %q, herdr-plugin.toml version %q: they must be the same", plugin.Version, manifest.Version)
	}

	// The installer reads the version it stamps with the sed expression on its own line; run that expression.
	script, err := os.ReadFile("scripts/fetch-or-build.sh")
	if err != nil {
		t.Fatalf("read the installer: %v", err)
	}
	m := regexp.MustCompile(`sed -n '([^']*)' "\$repo_root/herdr-plugin\.toml"`).FindSubmatch(script)
	if m == nil {
		t.Fatal("scripts/fetch-or-build.sh no longer reads its version from herdr-plugin.toml with sed -n '...'")
	}
	out, err := exec.Command("sed", "-n", string(m[1]), "herdr-plugin.toml").Output()
	if err != nil {
		t.Fatalf("run the installer's version expression: %v", err)
	}
	if stamped, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n"); stamped != manifest.Version {
		t.Errorf("the installer reads version %q from herdr-plugin.toml, which declares %q", stamped, manifest.Version)
	}
}
