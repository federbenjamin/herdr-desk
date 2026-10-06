package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
)

func TestRootAgentsMayStartLoadsSavesOnlyTrueAndKeepsTheWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[[roots]]\npath = \"/code/allowed\"\nagents_may_start = true\n\n[[roots]]\npath = \"/code/default\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Roots) != 2 || !cfg.Roots[0].AgentsMayStart || cfg.Roots[1].AgentsMayStart {
		t.Fatalf("loaded roots = %#v, want only /code/allowed to allow agents to start", cfg.Roots)
	}

	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(saved)
	if got := strings.Count(text, "agents_may_start = true"); got != 1 {
		t.Errorf("saved agents_may_start entries = %d, want one true entry:\n%s", got, text)
	}
	if strings.Contains(text, "agents_may_start = false") {
		t.Errorf("saved config writes the false opt-in:\n%s", text)
	}
	const warning = "WARNING: auto lets the coordinator start runs unasked and agents set tasks ready, which spends your quota; another agent may start a run only in a root with agents_may_start = true"
	warningAt := strings.Index(text, warning)
	startRunsAt := strings.Index(text, "start_runs")
	if warningAt < 0 || startRunsAt < 0 || warningAt > startRunsAt {
		t.Fatalf("saved config does not put the agents_may_start warning before start_runs:\n%s", text)
	}
	if between := text[warningAt:startRunsAt]; strings.Count(between, "\n") != 1 {
		t.Errorf("warning is not directly above start_runs: %q", between)
	}

	roundTrip, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() after Save() error: %v", err)
	}
	if len(roundTrip.Roots) != 2 || !roundTrip.Roots[0].AgentsMayStart || roundTrip.Roots[1].AgentsMayStart {
		t.Errorf("roots after Save and Load = %#v, want only /code/allowed to allow agents to start", roundTrip.Roots)
	}
}
