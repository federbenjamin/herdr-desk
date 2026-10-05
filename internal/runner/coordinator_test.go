package runner_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/herdr/herdrtest"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// slowHerdr takes a while to create a workspace, as a real herdr does: long enough for a second caller to have
// looked for the coordinator in the meantime.
type slowHerdr struct{ *herdrtest.Herdr }

func (h slowHerdr) CreateWorkspace(ctx context.Context, cwd, label string, env []string) (herdr.Created, error) {
	time.Sleep(100 * time.Millisecond)
	return h.Herdr.CreateWorkspace(ctx, cwd, label, env)
}

// Two `herdr-desk coordinator` calls at once must leave one coordinator agent: the second focuses the first's pane.
func TestCoordinatorCalledAtOnceOpensOneCoordinator(t *testing.T) {
	f := newFixture(t, "", "self")
	h := slowHerdr{f.herdr}
	const callers = 3
	opened := make([]bool, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		r := f.runnerWith(h)
		wg.Go(func() { _, opened[i], errs[i] = r.Coordinator(f.ctx, store.Actor{}) })
	}
	wg.Wait()
	n := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: Coordinator() error = %v", i, err)
		}
		if opened[i] {
			n++
		}
	}
	if n != 1 || len(f.herdr.Workspaces()) != 1 || len(f.herdr.Focused()) != callers-1 {
		t.Fatalf("%d opened, workspaces %#v, focused %v; want one opened, one workspace, and %d focuses", n, f.herdr.Workspaces(), f.herdr.Focused(), callers-1)
	}
}
