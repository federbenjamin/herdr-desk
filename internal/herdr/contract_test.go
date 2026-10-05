package herdr_test

import (
	"context"
	"encoding/json"
	"maps"
	"os/exec"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/herdr/herdrtest"
	"github.com/federbenjamin/herdr-desk/internal/runner"
)

func TestHerdrImplementationsSharePaneContract(t *testing.T) {
	implementations := []struct {
		name string
		new  func(*testing.T) runner.Herdr
	}{
		{
			name: "memory",
			new: func(t *testing.T) runner.Herdr {
				return herdrtest.NewHerdr()
			},
		},
		{
			name: "fake command",
			new: func(t *testing.T) runner.Herdr {
				client := newFakeClient(t)
				return &client
			},
		},
	}
	cases := []struct {
		name  string
		check func(*testing.T, runner.Herdr)
	}{
		{
			name: "new pane is unknown without a session",
			check: func(t *testing.T, h runner.Herdr) {
				created := contractWorkspace(t, h)
				panes, err := h.Panes(context.Background())
				if err != nil {
					t.Fatalf("Panes() error = %v", err)
				}
				pane := findPane(panes, created.Pane)
				if pane.Status != "unknown" || pane.Session != "" {
					t.Fatalf("new pane = %#v, want status unknown and no session", pane)
				}
			},
		},
		{
			name: "closing a pane removes it",
			check: func(t *testing.T, h runner.Herdr) {
				created := contractWorkspace(t, h)
				if err := h.ClosePane(context.Background(), created.Pane); err != nil {
					t.Fatalf("ClosePane() error = %v", err)
				}
				panes, err := h.Panes(context.Background())
				if err != nil {
					t.Fatalf("Panes() error = %v", err)
				}
				if hasPane(panes, created.Pane) {
					t.Fatalf("Panes() still contains closed pane %q", created.Pane)
				}
			},
		},
		{
			name: "getting a new pane finds it as listed, with no session yet",
			check: func(t *testing.T, h runner.Herdr) {
				created := contractWorkspace(t, h)
				pane, found, err := h.Pane(context.Background(), created.Pane)
				if err != nil || !found {
					t.Fatalf("Pane() = %#v, %v, %v; want the pane found", pane, found, err)
				}
				want := herdr.Pane{ID: created.Pane, Workspace: created.Workspace, Status: "unknown"}
				if pane != want {
					t.Fatalf("Pane() = %#v, want %#v", pane, want)
				}
			},
		},
		{
			name: "getting a closed pane is not found, with no error",
			check: func(t *testing.T, h runner.Herdr) {
				created := contractWorkspace(t, h)
				if err := h.ClosePane(context.Background(), created.Pane); err != nil {
					t.Fatalf("ClosePane() error = %v", err)
				}
				pane, found, err := h.Pane(context.Background(), created.Pane)
				if err != nil || found {
					t.Fatalf("Pane(closed) = %#v, %v, %v; want not found and no error", pane, found, err)
				}
			},
		},
		{
			name: "a reported token is kept for the pane, and an empty value clears it",
			check: func(t *testing.T, h runner.Herdr) {
				created := contractWorkspace(t, h)
				ctx := context.Background()
				if err := h.ReportToken(ctx, created.Pane, "herdr-desk", "desk", "T1 running · since 14:05"); err != nil {
					t.Fatalf("ReportToken() error = %v", err)
				}
				if got := contractTokens(t, h, created.Pane); !maps.Equal(got, map[string]string{"desk": "T1 running · since 14:05"}) {
					t.Fatalf("tokens = %v, want desk set", got)
				}
				if err := h.ReportToken(ctx, created.Pane, "herdr-desk", "desk", ""); err != nil {
					t.Fatalf("ReportToken(clear) error = %v", err)
				}
				if got := contractTokens(t, h, created.Pane); len(got) != 0 {
					t.Fatalf("tokens after clear = %v, want none", got)
				}
			},
		},
		{
			name: "reporting a token to a closed pane is refused",
			check: func(t *testing.T, h runner.Herdr) {
				created := contractWorkspace(t, h)
				if err := h.ClosePane(context.Background(), created.Pane); err != nil {
					t.Fatalf("ClosePane() error = %v", err)
				}
				if err := h.ReportToken(context.Background(), created.Pane, "herdr-desk", "desk", "T1 done"); err == nil {
					t.Fatal("ReportToken(closed pane) error = nil, want an error")
				}
			},
		},
		{
			name: "running an unknown pane is refused",
			check: func(t *testing.T, h runner.Herdr) {
				if err := h.Run(context.Background(), "unknown", "exit 0"); err == nil {
					t.Fatal("Run(unknown pane) error = nil, want an error")
				}
			},
		},
		{
			name: "reading processes from an unknown pane is refused",
			check: func(t *testing.T, h runner.Herdr) {
				if _, err := h.Processes(context.Background(), "unknown"); err == nil {
					t.Fatal("Processes(unknown pane) error = nil, want an error")
				}
			},
		},
		{
			name: "closing an unknown pane is refused",
			check: func(t *testing.T, h runner.Herdr) {
				if err := h.ClosePane(context.Background(), "unknown"); err == nil {
					t.Fatal("ClosePane(unknown pane) error = nil, want an error")
				}
			},
		},
	}

	for _, implementation := range implementations {
		for _, tt := range cases {
			t.Run(implementation.name+"/"+tt.name, func(t *testing.T) {
				tt.check(t, implementation.new(t))
			})
		}
	}
}

func contractWorkspace(t *testing.T, h runner.Herdr) herdr.Created {
	t.Helper()
	created, err := h.CreateWorkspace(context.Background(), t.TempDir(), "contract workspace", nil)
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	return created
}

// contractTokens reads the pane's tokens: from the stand-in's record, or from the fake command's `pane get`.
func contractTokens(t *testing.T, h runner.Herdr, pane string) map[string]string {
	t.Helper()
	switch h := h.(type) {
	case *herdrtest.Herdr:
		return h.Tokens(pane)
	case *herdr.Client:
		out, err := exec.Command(h.Bin, "pane", "get", pane).Output()
		if err != nil {
			t.Fatalf("fake-herdr pane get %s: %v", pane, err)
		}
		var res struct {
			Result struct {
				Pane struct {
					Tokens map[string]string `json:"tokens"`
				} `json:"pane"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out, &res); err != nil {
			t.Fatalf("fake-herdr pane get %s: %v: %s", pane, err, out)
		}
		return res.Result.Pane.Tokens
	}
	t.Fatalf("no way to read the tokens of a %T", h)
	return nil
}

var _ runner.Herdr = (*herdr.Client)(nil)
var _ runner.Herdr = (*herdrtest.Herdr)(nil)
