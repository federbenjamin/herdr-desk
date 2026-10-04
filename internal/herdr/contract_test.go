package herdr_test

import (
	"context"
	"testing"

	"github.com/federbenjamin/desk/internal/herdr"
	"github.com/federbenjamin/desk/internal/herdr/herdrtest"
)

type herdrContract interface {
	CreateWorkspace(context.Context, string, string, []string) (herdr.Created, error)
	Run(context.Context, string, string) error
	Panes(context.Context) ([]herdr.Pane, error)
	Processes(context.Context, string) (herdr.Processes, error)
	ClosePane(context.Context, string) error
}

func TestHerdrImplementationsSharePaneContract(t *testing.T) {
	implementations := []struct {
		name string
		new  func(*testing.T) herdrContract
	}{
		{
			name: "memory",
			new: func(t *testing.T) herdrContract {
				return herdrtest.NewHerdr()
			},
		},
		{
			name: "fake command",
			new: func(t *testing.T) herdrContract {
				client := newFakeClient(t)
				return &client
			},
		},
	}
	cases := []struct {
		name  string
		check func(*testing.T, herdrContract)
	}{
		{
			name: "new pane is unknown without a session",
			check: func(t *testing.T, h herdrContract) {
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
			check: func(t *testing.T, h herdrContract) {
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
			name: "running an unknown pane is refused",
			check: func(t *testing.T, h herdrContract) {
				if err := h.Run(context.Background(), "unknown", "exit 0"); err == nil {
					t.Fatal("Run(unknown pane) error = nil, want an error")
				}
			},
		},
		{
			name: "reading processes from an unknown pane is refused",
			check: func(t *testing.T, h herdrContract) {
				if _, err := h.Processes(context.Background(), "unknown"); err == nil {
					t.Fatal("Processes(unknown pane) error = nil, want an error")
				}
			},
		},
		{
			name: "closing an unknown pane is refused",
			check: func(t *testing.T, h herdrContract) {
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

func contractWorkspace(t *testing.T, h herdrContract) herdr.Created {
	t.Helper()
	created, err := h.CreateWorkspace(context.Background(), t.TempDir(), "contract workspace", nil)
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	return created
}

var _ herdrContract = (*herdr.Client)(nil)
var _ herdrContract = (*herdrtest.Herdr)(nil)
