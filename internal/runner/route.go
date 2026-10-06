package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// Roots returns the roots a task may run in: the config's roots in order, then the scratch root with isolation in-place.
func Roots(c config.Config, p config.Paths) []config.Root {
	scratch := p.ScratchRoot()
	// A configured root at the scratch path is dropped: the scratch root is listed once, last, in-place.
	roots := slices.DeleteFunc(slices.Clone(c.Roots), func(r config.Root) bool { return samePath(r.Path, scratch) })
	return append(roots, config.Root{
		Path:      scratch,
		About:     "herdr-desk's scratch folder: tasks with no project, or with a project under no other root",
		Isolation: "in-place",
	})
}

// Resolve returns the route a run of t takes, roots as Roots lists them (the scratch root last). Each field is
// given's, else the task's own, else a default: for the root, the task's project when it is a listed root, else the
// scratch root; for the isolation, the root's, else worktree when the root is the top of a git work tree, else
// in-place; for the model, the first of models ("" when there are none); for the first_message, the resolved root's
// ("" when it sets none). The route is then checked: the root is listed, the isolation is self, worktree, or
// in-place, the model is in models when models is not empty, and the first_message is empty or holds {task_file}. A
// failed check is bad-input.
func Resolve(t model.Task, given store.RunRoute, roots []config.Root, models []string) (store.RunRoute, error) {
	if len(roots) == 0 {
		return store.RunRoute{}, badRoute("there is no root to run in")
	}
	out := store.RunRoute{Root: given.Root, Isolation: given.Isolation, Model: given.Model}
	if out.Root == "" {
		out.Root = t.Root
	}
	if out.Root == "" {
		out.Root = roots[len(roots)-1].Path
		if r, ok := FindRoot(roots, t.Project); ok {
			out.Root = r.Path
		}
	}
	root, ok := FindRoot(roots, out.Root)
	if !ok {
		return store.RunRoute{}, badRoute("root %q is not a listed root", out.Root)
	}
	out.Root = filepath.Clean(root.Path)
	for _, iso := range []string{t.Isolation, root.Isolation} {
		if out.Isolation == "" {
			out.Isolation = iso
		}
	}
	if out.Isolation == "" {
		out.Isolation = "in-place"
		if isWorkTree(context.Background(), out.Root) {
			out.Isolation = "worktree"
		}
	}
	if !model.ValidIsolation(out.Isolation) {
		return store.RunRoute{}, badRoute("isolation %q is not self, worktree, or in-place", out.Isolation)
	}
	if out.Model == "" {
		out.Model = t.Model
	}
	if out.Model == "" && len(models) > 0 {
		out.Model = models[0]
	}
	if len(models) > 0 && !slices.Contains(models, out.Model) {
		return store.RunRoute{}, badRoute("model %q is not in [agent] models", out.Model)
	}
	for _, msg := range []string{given.FirstMessage, t.FirstMessage, root.FirstMessage} {
		if out.FirstMessage == "" {
			out.FirstMessage = msg
		}
	}
	if !model.ValidFirstMessage(out.FirstMessage) {
		return store.RunRoute{}, badRoute("first_message must be empty or hold {%s}, the path of the task's file, not %q", model.TaskFile, out.FirstMessage)
	}
	return out, nil
}

func badRoute(format string, args ...any) error {
	return &model.Refusal{Code: model.CodeBadInput, Msg: fmt.Sprintf(format, args...)}
}

// FindRoot returns the root written as path, else the first root that names the same folder through symlinks.
func FindRoot(roots []config.Root, path string) (config.Root, bool) {
	if path == "" {
		return config.Root{}, false
	}
	for _, r := range roots {
		if filepath.Clean(r.Path) == filepath.Clean(path) {
			return r, true
		}
	}
	for _, r := range roots {
		if resolved(r.Path) == resolved(path) {
			return r, true
		}
	}
	return config.Root{}, false
}

// samePath reports whether a and b name one folder, as written or once their symlinks are resolved.
func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b) || resolved(a) == resolved(b)
}

// resolved is path with its symlinks resolved, or cleaned when it cannot be resolved.
func resolved(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p
	}
	return filepath.Clean(path)
}
