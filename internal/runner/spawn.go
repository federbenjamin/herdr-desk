package runner

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/federbenjamin/desk/internal/gitcmd"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
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

// spawn starts the run's worker in a new herdr workspace. from is the run's state now (routing or waiting). It
// reports whether the worker was started; a failure sets the run failed and the task blocked.
func (r *Runner) spawn(ctx context.Context, h Herdr, t model.Task, run model.Run, from string) bool {
	failSpawn := func(state string, reason string) bool {
		r.fail(ctx, run, state, []string{model.TagRunner}, "spawn: "+clip(reason))
		return false
	}
	command, err := r.paneCommand()
	if err != nil {
		return failSpawn(from, err.Error())
	}
	dir, err := r.workdir(ctx, t, run)
	if err != nil {
		return failSpawn(from, err.Error())
	}
	session, err := newUUID()
	if err != nil {
		return failSpawn(from, err.Error())
	}
	ok, err := r.o.Store.UpdateRun(ctx, run.ID, from, store.RunUpdate{State: model.RunRunning, Session: session})
	if err != nil {
		r.logErr("T%d run %d: set running", t.Number, run.ID, err)
	}
	if err != nil || !ok {
		return false
	}
	env := append([]string{
		fmt.Sprintf("DESK_TASK=T%d", t.Number),
		"DESK_SESSION=" + session,
		fmt.Sprintf("DESK_RUN=%d", run.ID),
	}, r.o.Paths.Env()...)
	created, err := h.CreateWorkspace(ctx, dir, fmt.Sprintf("desk T%d", t.Number), env)
	if err != nil {
		return failSpawn(model.RunRunning, err.Error())
	}
	// Recorded before the command runs, so a kill that comes now finds the pane by its id.
	if _, err := r.o.Store.UpdateRun(ctx, run.ID, model.RunRunning, store.RunUpdate{Workspace: created.Workspace, Pane: created.Pane}); err != nil {
		r.logErr("T%d run %d: record the pane", t.Number, run.ID, err)
	}
	if err := h.Run(ctx, created.Pane, command); err != nil {
		if cerr := h.ClosePane(ctx, created.Pane); cerr != nil {
			r.logErr("T%d: close pane %s", t.Number, created.Pane, cerr)
		}
		return failSpawn(model.RunRunning, err.Error())
	}
	r.note(ctx, store.Actor{Run: run.ID}, t.Number, []string{model.TagRunner},
		fmt.Sprintf("run %d: workspace %s, pane %s", run.ID, created.Workspace, created.Pane))
	r.notify(ctx, fmt.Sprintf("desk: T%d started", t.Number), t.Title)
	return true
}

// paneCommand is the one text typed into a pane: exec of this desk binary's worker command.
func (r *Runner) paneCommand() (string, error) {
	exe := r.o.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return "", fmt.Errorf("find the desk binary: %w", err)
		}
	}
	quoted, err := shellQuote(exe)
	if err != nil {
		return "", err
	}
	return "exec " + quoted + " worker", nil
}

// shellQuote returns path as it is when it holds only [A-Za-z0-9_./-], else single-quoted. A path holding a
// single quote or a control character is refused.
func shellQuote(path string) (string, error) {
	if path == "" {
		return "", errors.New("the desk binary's path is empty")
	}
	plain := true
	for _, c := range path {
		switch {
		case c == '\'' || c < 0x20 || c == 0x7f || (c >= 0x80 && c < 0xa0):
			return "", fmt.Errorf("the desk binary's path %q holds a quote or a control character", path)
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

// workdir returns the folder the worker starts in: a git worktree beside the root for worktree isolation, else
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
	dir := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-T"+strconv.Itoa(t.Number))
	if isWorkTree(ctx, dir) {
		return dir, nil
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

// isWorkTree reports whether dir is the top of a git work tree.
func isWorkTree(ctx context.Context, dir string) bool {
	if _, err := os.Stat(dir); err != nil {
		return false
	}
	top, err := gitcmd.Run(ctx, dir, gitTimeout, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	a, errA := filepath.EvalSymlinks(top)
	b, errB := filepath.EvalSymlinks(dir)
	return errA == nil && errB == nil && a == b
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
