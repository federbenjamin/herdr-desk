package setup_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/setup"
)

func TestRunClaudeCodeProfileSetsModels(t *testing.T) {
	paths, getenv := setupPaths(t)
	var out bytes.Buffer
	if err := setup.Run(context.Background(), setup.Options{
		Paths:   paths,
		Getenv:  getenv,
		Profile: "claude-code",
		NoHerdr: true,
		Out:     &out,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got, err := config.Load(paths.ConfigFile())
	if err != nil {
		t.Fatalf("load configured home: %v", err)
	}
	if want := []string{"sonnet", "opus"}; !reflect.DeepEqual(got.Agent.Models, want) {
		t.Fatalf("agent models = %#v, want %#v", got.Agent.Models, want)
	}
	if !strings.Contains(out.String(), "agent.models: set by the claude-code profile") {
		t.Fatalf("Run() output = %q, want models set line", out.String())
	}
}

func TestRunClaudeCodeProfileKeepsExistingModels(t *testing.T) {
	paths, getenv := setupPaths(t)
	existing := config.Default()
	existing.Agent.Models = []string{"custom-model"}
	if err := existing.Save(paths.ConfigFile()); err != nil {
		t.Fatalf("save existing config: %v", err)
	}
	var out bytes.Buffer
	if err := setup.Run(context.Background(), setup.Options{
		Paths:   paths,
		Getenv:  getenv,
		Profile: "claude-code",
		NoHerdr: true,
		Out:     &out,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got, err := config.Load(paths.ConfigFile())
	if err != nil {
		t.Fatalf("load configured home: %v", err)
	}
	if want := []string{"custom-model"}; !reflect.DeepEqual(got.Agent.Models, want) {
		t.Fatalf("agent models = %#v, want %#v", got.Agent.Models, want)
	}
	if !strings.Contains(out.String(), "agent.models: kept") {
		t.Fatalf("Run() output = %q, want models kept line", out.String())
	}
}
