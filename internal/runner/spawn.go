package runner

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/federbenjamin/herdr-desk/internal/gitcmd"
	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

const maxSlug = 40

// Slug turns a title into a branch-name part: lower-case letters and digits kept, every other run of characters
// one "-", none at either end, at most 40 characters. It may be empty.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(title) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(c)
			dash = false
			continue
		}
		dash = true
	}
	s := b.String()
	if len(s) > maxSlug {
		s = strings.TrimRight(s[:maxSlug], "-")
	}
	return s
}

// spawn starts the starting run's worker in a new herdr workspace. It reports whether the worker was started; a
// failure sets the run failed and the task blocked.
func (r *Runner) spawn(ctx context.Context, h Herdr, t model.Task, run model.Run) bool {
	from := model.RunStarting
	// failSpawn closes the pane the spawn opened, when there is one, then fails the run while it is in state; the
	// note's clause about a pane left open follows the clipped reason, so it is never cut.
	failSpawn := func(ctx context.Context, state, reason string, pane *herdr.Pane) bool {
		extra := ""
		if pane != nil && !r.close(ctx, h, run, *pane) {
			extra = fmt.Sprintf("; pane %s was left open", pane.ID)
		}
		r.fail(ctx, run, state, "spawn: "+clip(reason)+extra)
		return false
	}
	command, err := r.paneCommand("worker")
	if err != nil {
		return failSpawn(ctx, from, err.Error(), nil)
	}
	dir, err := r.workdir(ctx, t, run)
	if err != nil {
		return failSpawn(ctx, from, err.Error(), nil)
	}
	session, err := newUUID()
	if err != nil {
		return failSpawn(ctx, from, err.Error(), nil)
	}
	env := append([]string{
		fmt.Sprintf("DESK_TASK=T%d", t.Number),
		"DESK_SESSION=" + session,
		fmt.Sprintf("DESK_RUN=%d", run.ID),
	}, r.o.Paths.Env()...)
	created, err := h.CreateWorkspace(ctx, dir, fmt.Sprintf("desk T%d", t.Number), env)
	if err != nil {
		return failSpawn(ctx, from, err.Error(), nil)
	}
	pane := herdr.Pane{ID: created.Pane, Workspace: created.Workspace}
	// The pane exists outside herdr-desk now: undoing the spawn goes on even when ctx ends, so a stopping process
	// leaves no worker and no live run behind.
	undo := context.WithoutCancel(ctx)
	// The run becomes running with its pane in one write, so a running run's pane is always known to a kill; a
	// kill that took the run while the workspace was made leaves this claim unapplied.
	ok, err := r.o.Store.UpdateRun(ctx, run.ID, from, store.RunUpdate{
		State: model.RunRunning, Session: session, Workspace: created.Workspace, Pane: created.Pane,
	})
	if err != nil {
		return failSpawn(undo, from, "could not record the pane: "+err.Error(), &pane)
	}
	if !ok {
		r.close(undo, h, run, pane)
		return false
	}
	if err := h.Run(ctx, created.Pane, command); err != nil {
		return failSpawn(undo, model.RunRunning, err.Error(), &pane)
	}
	cur, ok, err := r.o.Store.CurrentRun(ctx, t.Number)
	if err != nil {
		// The run may have been killed while its command was typed: nothing says it started until the store does.
		return failSpawn(undo, model.RunRunning, "could not confirm the run: "+err.Error(), &pane)
	}
	if !ok || cur.ID != run.ID || model.RunFinal(cur.State) {
		// A kill took the run while its command was typed: no worker stays, and nothing says it started. A run an
		// early blocked event set idle keeps its pane.
		r.close(undo, h, run, pane)
		return false
	}
	r.note(ctx, store.Actor{Run: run.ID}, t.Number, []string{model.TagRunner},
		fmt.Sprintf("run %d: workspace %s, pane %s", run.ID, created.Workspace, created.Pane))
	r.notify(ctx, fmt.Sprintf("herdr-desk: T%d started", t.Number), t.Title)
	return true
}

// paneCommand is the one text typed into a pane: exec of this herdr-desk binary with sub, a fixed subcommand
// (`worker` for a run, `coordinator run` for the coordinator), never task text.
func (r *Runner) paneCommand(sub string) (string, error) {
	exe := r.o.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return "", fmt.Errorf("find the herdr-desk binary: %w", err)
		}
	}
	quoted, err := shellQuote(exe)
	if err != nil {
		return "", err
	}
	return "exec " + quoted + " " + sub, nil
}

// shellQuote returns path as it is when it holds only [A-Za-z0-9_./-], else single-quoted. A path holding a
// single quote or a control character is refused.
func shellQuote(path string) (string, error) {
	if path == "" {
		return "", errors.New("the herdr-desk binary's path is empty")
	}
	plain := true
	for _, c := range path {
		switch {
		case c == '\'' || c < 0x20 || c == 0x7f || (c >= 0x80 && c < 0xa0):
			return "", fmt.Errorf("the herdr-desk binary's path %q holds a quote or a control character", path)
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || strings.ContainsRune("_./-", c):
		default:
			plain = false
		}
	}
	if plain {
		return path, nil
	}
	return "'" + path + "'", nil
}

// workdir returns the folder the worker starts in: a git worktree inside the root for worktree isolation, else
// the root, made when it is the scratch root and missing.
func (r *Runner) workdir(ctx context.Context, t model.Task, run model.Run) (string, error) {
	root := filepath.Clean(run.Root)
	if run.Isolation != "worktree" {
		if root == filepath.Clean(r.o.Paths.ScratchRoot()) {
			if err := os.MkdirAll(root, 0o700); err != nil {
				return "", err
			}
		}
		return root, nil
	}
	if dir, ok, err := taskWorktree(ctx, root, t.Number); err != nil {
		return "", err
	} else if ok {
		return dir, nil
	}
	dir := worktreeDir(root, t.Number)
	if err := hideWorktrees(ctx, root); err != nil {
		return "", err
	}
	branch := fmt.Sprintf("desk/T%d", t.Number)
	if s := Slug(t.Title); s != "" {
		branch += "-" + s
	}
	args := []string{"worktree", "add", "-b", branch, dir}
	if _, err := gitcmd.Run(ctx, root, gitTimeout, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		args = []string{"worktree", "add", dir, branch}
	}
	if _, err := gitcmd.Run(ctx, root, gitTimeout, args...); err != nil {
		return "", err
	}
	return dir, nil
}

// isWorkTree reports whether dir is the top of a git work tree. A missing dir, a dir git says is in no repository,
// and a dir inside a work tree it is not the top of are false with no error; a dir that could not be read, or that
// git could not answer for, is the error.
func isWorkTree(ctx context.Context, dir string) (bool, error) {
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	top, err := gitcmd.Run(ctx, dir, gitTimeout, "rev-parse", "--show-toplevel")
	if gitcmd.IsNotRepo(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	a, err := filepath.EvalSymlinks(top)
	if err != nil {
		return false, err
	}
	b, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false, err
	}
	return a == b, nil
}

// newUUID returns a random version 4 UUID.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
