package runner

import (
	"context"
	"fmt"
	"os"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// coordinatorLabel is the coordinator workspace's label in herdr.
const coordinatorLabel = "desk coordinator"

// Coordinator focuses the desk's coordinator pane when herdr still has it, and records nothing. Else it opens a new
// coordinator: a workspace in the scratch root with a new DESK_SESSION, recorded in the store, whose pane types
// `herdr-desk coordinator run`. An agent session gets not-allowed before herdr is asked; no herdr is no-herdr.
// opened reports whether a new coordinator was opened. The look and the open hold the coordinator lock, so calls at
// once from several processes open one coordinator, and the others focus it.
func (r *Runner) Coordinator(ctx context.Context, a store.Actor) (c model.Coordinator, opened bool, err error) {
	if a.Session != "" {
		return model.Coordinator{}, false, &model.Refusal{Code: model.CodeNotAllowed, Msg: "an agent may not open the coordinator; a person does"}
	}
	h, err := r.findHerdr()
	if err != nil {
		return model.Coordinator{}, false, &model.Refusal{Code: model.CodeNoHerdr, Msg: "herdr was not found, so no pane can be opened: " + err.Error()}
	}
	unlock, err := config.Lock(r.o.Paths.CoordinatorLock())
	if err != nil {
		return model.Coordinator{}, false, err
	}
	defer unlock()
	old, ok, err := r.o.Store.Coordinator(ctx)
	if err != nil {
		return model.Coordinator{}, false, err
	}
	if ok {
		_, found, err := h.Pane(ctx, old.Pane)
		if err != nil {
			return model.Coordinator{}, false, err
		}
		if found {
			return old, false, nil
		}
	}
	command, err := r.paneCommand("coordinator run")
	if err != nil {
		return model.Coordinator{}, false, err
	}
	session, err := newUUID()
	if err != nil {
		return model.Coordinator{}, false, err
	}
	dir := r.o.Paths.ScratchRoot()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return model.Coordinator{}, false, err
	}
	created, err := h.CreateWorkspace(ctx, dir, coordinatorLabel, append([]string{"DESK_SESSION=" + session}, r.o.Paths.Env()...))
	if err != nil {
		return model.Coordinator{}, false, err
	}
	c = model.Coordinator{Session: session, Workspace: created.Workspace, Pane: created.Pane, Cursor: old.Cursor}
	// The pane exists outside herdr-desk now: a failure closes it even when ctx ends, so no coordinator is left
	// that the store does not name.
	undo := func(err error) (model.Coordinator, bool, error) {
		if cerr := h.ClosePane(context.WithoutCancel(ctx), created.Pane); cerr != nil {
			err = fmt.Errorf("%w; pane %s was left open: %v", err, created.Pane, cerr)
		}
		return model.Coordinator{}, false, err
	}
	if err := r.o.Store.SetCoordinator(ctx, a, c); err != nil {
		return undo(err)
	}
	if err := h.Run(ctx, created.Pane, command); err != nil {
		return undo(err)
	}
	return c, true, nil
}
