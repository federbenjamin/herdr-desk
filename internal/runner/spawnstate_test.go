package runner_test

import (
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestStartKeepsIdlePaneButClosesTerminalPaneAfterTheWorkerStarts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      string
		wantClosed bool
	}{
		{"idle run keeps its pane", model.RunIdle, false},
		{"ended run closes its pane", model.RunEnded, true},
		{"failed run closes its pane", model.RunFailed, true},
		{"killed run closes its pane", model.RunKilled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			f := newFixture(t, root, "self")
			task := f.armRoute(tc.name, root, "self")
			h := &hookedHerdr{Herdr: f.herdr}
			h.after("Run", func() {
				run, ok, err := f.store.CurrentRun(f.ctx, task.Number)
				if err != nil || !ok {
					t.Errorf("read run while worker starts = (%#v, %t, %v)", run, ok, err)
					return
				}
				changed, err := f.store.UpdateRun(f.ctx, run.ID, model.RunRunning, store.RunUpdate{State: tc.state})
				if err != nil || !changed {
					t.Errorf("change run to %s = (%t, %v)", tc.state, changed, err)
				}
			})

			f.startRun(f.runnerWith(h), task.Number)
			workspaces := f.herdr.Workspaces()
			if len(workspaces) != 1 || workspaces[0].Command == "" {
				t.Fatalf("workspaces = %#v, want one worker-started pane", workspaces)
			}
			if got := f.run(task.Number).State; got != tc.state {
				t.Fatalf("run state after worker starts = %q, want %q", got, tc.state)
			}
			closed := f.herdr.Closed()
			if tc.wantClosed && (len(closed) != 1 || closed[0] != workspaces[0].Pane) {
				t.Fatalf("closed panes = %#v, want %q closed", closed, workspaces[0].Pane)
			}
			if !tc.wantClosed && len(closed) != 0 {
				t.Fatalf("closed panes = %#v, want the idle pane left open", closed)
			}
		})
	}
}
