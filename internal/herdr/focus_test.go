package herdr_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/herdr/herdrtest"
)

func TestFocusArgvIsTheThreeFocusCommandsInOrder(t *testing.T) {
	got, err := herdr.FocusArgv("/opt/herdr", "w1", "p1-2")
	if err != nil {
		t.Fatalf("FocusArgv: %v", err)
	}
	want := [][]string{
		{"/opt/herdr", "workspace", "focus", "w1"},
		{"/opt/herdr", "pane", "zoom", "p1-2", "--on"},
		{"/opt/herdr", "pane", "zoom", "p1-2", "--off"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FocusArgv = %q, want %q", got, want)
	}
}

// An id reaches herdr's argv, so one that could read as a flag or carry a space is refused before any process runs.
func TestFocusArgvRefusesAnIdHerdrDoesNotGive(t *testing.T) {
	for _, test := range []struct{ name, workspace, pane string }{
		{"empty workspace", "", "p1"},
		{"empty pane", "w1", ""},
		{"flag-like workspace", "--help", "p1"},
		{"flag-like pane", "w1", "-x"},
		{"space in pane", "w1", "p1 --on"},
		{"shell metacharacter", "w1;rm", "p1"},
		{"newline", "w1", "p1\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			argv, err := herdr.FocusArgv("herdr", test.workspace, test.pane)
			if err == nil || argv != nil {
				t.Fatalf("FocusArgv(%q, %q) = (%q, %v), want an error and no argv", test.workspace, test.pane, argv, err)
			}
		})
	}
}

func callsLog(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if err != nil {
		t.Fatalf("read calls.log: %v", err)
	}
	var calls []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if _, call, ok := strings.Cut(line, "\t"); ok {
			calls = append(calls, call)
		}
	}
	return calls
}

func TestFocusPaneRunsTheThreeCommandsAgainstFakeHerdr(t *testing.T) {
	dir := t.TempDir()
	client := newFakeClientIn(t, dir)
	created := createWorkspace(t, client)

	if err := client.FocusPane(context.Background(), created.Workspace, created.Pane); err != nil {
		t.Fatalf("FocusPane: %v", err)
	}
	calls := callsLog(t, dir)
	want := []string{
		"workspace focus " + created.Workspace,
		"pane zoom " + created.Pane + " --on",
		"pane zoom " + created.Pane + " --off",
	}
	if len(calls) < len(want) || !reflect.DeepEqual(calls[len(calls)-3:], want) {
		t.Fatalf("calls.log = %q, want it to end with %q", calls, want)
	}
}

func TestFocusPaneOfAClosedPaneFailsAtZoomNamingIt(t *testing.T) {
	dir := t.TempDir()
	client := newFakeClientIn(t, dir)
	created := createWorkspace(t, client)
	if err := client.ClosePane(context.Background(), created.Pane); err != nil {
		t.Fatalf("ClosePane: %v", err)
	}

	err := client.FocusPane(context.Background(), created.Workspace, created.Pane)
	if err == nil || !strings.Contains(err.Error(), "pane zoom") || !strings.Contains(err.Error(), "pane_not_found") {
		t.Fatalf("FocusPane of a closed pane = %v, want an error from pane zoom naming pane_not_found", err)
	}
	calls := callsLog(t, dir)
	for _, c := range calls {
		if strings.HasSuffix(c, "--off") {
			t.Fatalf("calls.log = %q: the first failing command must stop the sequence before --off", calls)
		}
	}
}

func TestFocusPaneOfAnUnknownWorkspaceFailsAtTheFirstCommand(t *testing.T) {
	dir := t.TempDir()
	client := newFakeClientIn(t, dir)
	created := createWorkspace(t, client)

	err := client.FocusPane(context.Background(), "w999", created.Pane)
	if err == nil || !strings.Contains(err.Error(), "workspace focus") {
		t.Fatalf("FocusPane in an unknown workspace = %v, want an error from workspace focus", err)
	}
	for _, c := range callsLog(t, dir) {
		if strings.HasPrefix(c, "pane zoom") {
			t.Fatalf("a pane zoom ran after workspace focus failed")
		}
	}
}

func TestFocusPaneRefusesABadIdBeforeRunningAnything(t *testing.T) {
	dir := t.TempDir()
	client := newFakeClientIn(t, dir)
	if err := client.FocusPane(context.Background(), "w1", "--on"); err == nil {
		t.Fatal("FocusPane with a flag-like pane id = nil error, want a refusal")
	}
	if _, err := os.Stat(filepath.Join(dir, "calls.log")); err == nil {
		t.Fatal("herdr ran for a bad id, want no process started")
	}
}

func TestHerdrtestFocusPaneRecordsPanesInOrderAndChecksIdsAndPane(t *testing.T) {
	ctx := context.Background()
	h := herdrtest.NewHerdr()
	a, err := h.CreateWorkspace(ctx, "/tmp", "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.CreateWorkspace(ctx, "/tmp", "b", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []herdr.Created{b, a, b} {
		if err := h.FocusPane(ctx, p.Workspace, p.Pane); err != nil {
			t.Fatalf("FocusPane(%v): %v", p, err)
		}
	}
	if got, want := h.Focused(), []string{b.Pane, a.Pane, b.Pane}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Focused() = %q, want %q", got, want)
	}
	if err := h.FocusPane(ctx, a.Workspace, "--on"); err == nil {
		t.Fatal("FocusPane with a bad id succeeded, want the FocusArgv check")
	}
	if err := h.FocusPane(ctx, a.Workspace, "nope"); err == nil {
		t.Fatal("FocusPane of an unknown pane succeeded, want an error")
	}
	if err := h.ClosePane(ctx, a.Pane); err != nil {
		t.Fatal(err)
	}
	if err := h.FocusPane(ctx, a.Workspace, a.Pane); err == nil {
		t.Fatal("FocusPane of a closed pane succeeded, want an error")
	}
	if got := len(h.Focused()); got != 3 {
		t.Fatalf("Focused() has %d entries after three good calls and failures, want 3", got)
	}
}
