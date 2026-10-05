package cli_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

// context on a client prints the desk as the home holds it: the home's limits, roots, models, start_runs, and its
// count of today's runs. The client's own config is a client's, and says nothing about the desk.
func TestContextOnAClientPrintsTheHomesFigures(t *testing.T) {
	home, root := startOnlyHome(t, func(c *config.Config) {
		c.Runner.Cap = 2
		c.Runner.MaxRunsPerDay = 7
		c.Runner.MaxRunMinutes = 33
		c.Coordinator.StartRuns = config.StartRunsAuto
	})
	task := addTask(t, home, "counted today")
	requireSuccess(t, runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", task), "--root", root))
	client := testutil.NewClientMachine(t, home)

	result := runDeskWithEnv(t, client, t.TempDir(), []string{"context", "--json"}, "", nil)
	requireSuccess(t, result)
	var d struct {
		StartRuns     string   `json:"start_runs"`
		Cap           int      `json:"cap"`
		Today         int      `json:"today"`
		MaxRunsPerDay int      `json:"max_runs_per_day"`
		MaxRunMinutes int      `json:"max_run_minutes"`
		Models        []string `json:"models"`
		Roots         []struct {
			Path      string `json:"path"`
			About     string `json:"about"`
			Isolation string `json:"isolation"`
		} `json:"roots"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &d); err != nil {
		t.Fatalf("context --json = %q: %v", result.stdout, err)
	}
	if d.StartRuns != config.StartRunsAuto || d.Cap != 2 || d.Today != 1 || d.MaxRunsPerDay != 7 || d.MaxRunMinutes != 33 ||
		!reflect.DeepEqual(d.Models, []string{"test-model"}) {
		t.Fatalf("client context = %+v, want the home's start_runs, cap, today's one run, limits, and models", d)
	}
	if len(d.Roots) != 2 || d.Roots[0].Path != root || d.Roots[0].About != "test root" || d.Roots[1].Path != home.Paths.ScratchRoot() {
		t.Fatalf("client context roots = %+v, want the home's root, then the home's scratch root", d.Roots)
	}
	text := runDeskWithEnv(t, client, t.TempDir(), []string{"context"}, "", nil)
	requireSuccess(t, text)
	if !strings.Contains(text.stdout, "cap 2 · today 1 of max_runs_per_day 7 · max_run_minutes 33") {
		t.Fatalf("client context text = %q, want the home's figures", text.stdout)
	}
}

// A runs or context whose check of the live runs against herdr could not run must say so: the runs it prints are the
// store's as they stood, and a run whose pane closed still reads as running.
func TestRunsAndContextSayWhenTheLiveRunsWereNotChecked(t *testing.T) {
	home, root := startOnlyHome(t, nil)
	task := addTask(t, home, "unchecked")
	requireSuccess(t, runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", task), "--root", root))
	t.Setenv("DESK_HERDR", filepath.Join(t.TempDir(), "no-herdr"))

	runs := runHomeDesk(t, home, "runs")
	requireSuccess(t, runs)
	const note = "the live runs were not checked against herdr"
	if !strings.Contains(runs.stderr, "herdr-desk runs: "+note) || !strings.Contains(runs.stdout, "running") {
		t.Fatalf("runs = stdout %q, stderr %q; want the running run and the note on stderr", runs.stdout, runs.stderr)
	}
	text := runHomeDesk(t, home, "context")
	requireSuccess(t, text)
	if !strings.Contains(text.stdout, "live runs:\n  ("+note) {
		t.Fatalf("context = %q, want the note under live runs", text.stdout)
	}
	asJSON := runHomeDesk(t, home, "context", "--json")
	requireSuccess(t, asJSON)
	var d struct {
		Unchecked string `json:"unchecked"`
	}
	if err := json.Unmarshal([]byte(asJSON.stdout), &d); err != nil || !strings.HasPrefix(d.Unchecked, note) {
		t.Fatalf("context --json unchecked = %q (%v), want the note", d.Unchecked, err)
	}
}
