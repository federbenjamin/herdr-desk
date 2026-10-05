package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/federbenjamin/herdr-desk"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

const maxReason = 500

// Route is where and how a task runs.
type Route struct {
	Root      string `json:"root"`
	Isolation string `json:"isolation"`
	Model     string `json:"model"`
	Reason    string `json:"reason"`
}

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

// Schema returns the JSON schema of a route: root an enum of the roots' paths, isolation an enum of worktree and
// in-place, model an enum of models (left out, with its requirement, when models is empty), reason a string.
func Schema(roots []config.Root, models []string) []byte {
	type prop struct {
		Type string   `json:"type"`
		Enum []string `json:"enum,omitempty"`
	}
	paths := make([]string, len(roots))
	for i, r := range roots {
		paths[i] = r.Path
	}
	props := map[string]prop{
		"root":      {Type: "string", Enum: paths},
		"isolation": {Type: "string", Enum: []string{"worktree", "in-place"}},
		"reason":    {Type: "string"},
	}
	required := []string{"root", "isolation", "reason"}
	if len(models) > 0 {
		props["model"] = prop{Type: "string", Enum: models}
		required = []string{"root", "isolation", "model", "reason"}
	}
	return model.MustData(struct {
		Type       string          `json:"type"`
		Properties map[string]prop `json:"properties"`
		Required   []string        `json:"required"`
		Additional bool            `json:"additionalProperties"`
	}{"object", props, required, false})
}

// RouterInput returns the router's stdin: {"task":{number,title,notes,project,steps},"roots":[{path,about,isolation}],"models":[…]}.
func RouterInput(t model.Task, roots []config.Root, models []string) []byte {
	type task struct {
		Number  int          `json:"number"`
		Title   string       `json:"title"`
		Notes   string       `json:"notes"`
		Project string       `json:"project"`
		Steps   []model.Step `json:"steps"`
	}
	type root struct {
		Path      string `json:"path"`
		About     string `json:"about"`
		Isolation string `json:"isolation"`
	}
	in := struct {
		Task   task     `json:"task"`
		Roots  []root   `json:"roots"`
		Models []string `json:"models"`
	}{
		Task:   task{t.Number, t.Title, t.Notes, t.Project, t.Steps},
		Roots:  make([]root, len(roots)),
		Models: models,
	}
	if in.Task.Steps == nil {
		in.Task.Steps = []model.Step{}
	}
	if in.Models == nil {
		in.Models = []string{}
	}
	for i, r := range roots {
		in.Roots[i] = root{r.Path, r.About, r.Isolation}
	}
	return model.MustData(in)
}

// ParseRoute reads a router's stdout: one JSON object holding the route at its top level or under
// "structured_output". The reason is trimmed and cut to 500 characters. It does not check the values.
func ParseRoute(out []byte) (Route, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil || top == nil {
		return Route{}, errors.New("the answer is not a JSON object")
	}
	if len(bytes.TrimSpace(out[dec.InputOffset():])) > 0 {
		return Route{}, errors.New("the answer holds more than one JSON value")
	}
	obj := out
	if so, ok := top["structured_output"]; ok {
		if string(bytes.TrimSpace(so)) == "null" {
			return Route{}, errors.New("the answer's structured_output is null")
		}
		obj = so
	}
	var r Route
	if err := json.Unmarshal(obj, &r); err != nil {
		return Route{}, errors.New("the route is not an object of strings")
	}
	r.Reason = strings.TrimSpace(r.Reason)
	if utf8.RuneCountInString(r.Reason) > maxReason {
		r.Reason = string([]rune(r.Reason)[:maxReason])
	}
	return r, nil
}

// Resolve returns what needs no router and whether the router is needed. A field needs no router when the task's
// own field sets it; isolation also when the decided root has a configured isolation; model also when models is
// empty. When the router is not needed, the route is checked as Apply checks it.
func Resolve(t model.Task, roots []config.Root, models []string) (decided Route, needsRouter bool, err error) {
	decided = Route{Root: t.Root, Isolation: t.Isolation, Model: t.Model}
	if decided.Isolation == "" && decided.Root != "" {
		if r, ok := findRoot(roots, decided.Root); ok {
			decided.Isolation = r.Isolation
		}
	}
	needsRouter = decided.Root == "" || decided.Isolation == "" || (decided.Model == "" && len(models) > 0)
	if needsRouter {
		return decided, true, nil
	}
	route, err := Apply(decided, Route{}, roots, models)
	if err != nil {
		return decided, false, err
	}
	return route, false, nil
}

// Apply fills the fields decided leaves open from the router's pick (a root's configured isolation wins over the
// pick) and checks the result: the root is a listed root; the isolation is self, worktree, or in-place, and never
// self by the router's own pick; the model is in models when models is not empty. A failed check is an error
// that names the field.
func Apply(decided, picked Route, roots []config.Root, models []string) (Route, error) {
	out := decided
	if out.Root == "" {
		out.Root = picked.Root
	}
	root, ok := findRoot(roots, out.Root)
	if !ok {
		return Route{}, fmt.Errorf("root %q is not a listed root", out.Root)
	}
	out.Root = root.Path
	byPick := false
	if out.Isolation == "" {
		out.Isolation = root.Isolation
	}
	if out.Isolation == "" {
		out.Isolation, byPick = picked.Isolation, true
	}
	if out.Isolation == "" || !model.ValidIsolation(out.Isolation) {
		return Route{}, fmt.Errorf("isolation %q is not self, worktree, or in-place", out.Isolation)
	}
	if byPick && out.Isolation == "self" {
		return Route{}, errors.New("isolation self comes from a root or the task, never the router")
	}
	if out.Model == "" && len(models) > 0 {
		out.Model = picked.Model
	}
	if len(models) > 0 && !slices.Contains(models, out.Model) {
		return Route{}, fmt.Errorf("model %q is not in [agent] models", out.Model)
	}
	if out.Reason == "" {
		out.Reason = picked.Reason
	}
	return out, nil
}

// findRoot returns the root written as path, else the first root that names the same folder through symlinks.
func findRoot(roots []config.Root, path string) (config.Root, bool) {
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

// listedProject returns project under the path of the most specific listed root that holds it, as the config
// writes that path, so the router sees one folder by one path. A project under no root is returned as it is.
func listedProject(project string, roots []config.Root) string {
	if project == "" {
		return project
	}
	in, best, out := resolved(project), -1, project
	for _, r := range roots {
		root := resolved(r.Path)
		rest, ok := strings.CutPrefix(in, root)
		if !ok || (rest != "" && !strings.HasPrefix(rest, string(filepath.Separator))) || len(root) <= best {
			continue
		}
		best, out = len(root), filepath.Clean(r.Path)+rest
	}
	return out
}

// route decides where the run's task runs, through the router when Resolve says it is needed, and saves the route
// on the task and the run. It reports false when the run failed or was taken by a kill.
func (r *Runner) route(ctx context.Context, t model.Task, run *model.Run) bool {
	roots := Roots(r.o.Config, r.o.Paths)
	models := r.o.Config.Agent.Models
	routerTags := []string{model.TagRunner, model.TagRouter}
	decided, needed, err := Resolve(t, roots, models)
	if err != nil {
		r.fail(ctx, *run, model.RunRouting, routerTags, "router: "+clip(err.Error()))
		return false
	}
	route := decided
	if needed {
		picked, err := r.callRouter(ctx, t, roots, models)
		if err == nil {
			route, err = Apply(decided, picked, roots, models)
		}
		if err != nil {
			r.fail(ctx, *run, model.RunRouting, routerTags, "router: "+clip(err.Error()))
			return false
		}
	}
	ok, err := r.o.Store.UpdateRun(ctx, run.ID, model.RunRouting, store.RunUpdate{
		Root: route.Root, Isolation: route.Isolation, Model: route.Model, Reason: route.Reason,
	})
	if err != nil {
		r.logErr("T%d run %d: record the route", t.Number, run.ID, err)
	}
	if err != nil || !ok {
		return false
	}
	run.Root, run.Isolation, run.Model, run.Reason = route.Root, route.Isolation, route.Model, route.Reason
	actor := store.Actor{Run: run.ID}
	if _, err := r.o.Store.SetTask(ctx, actor, t.Number, model.Patch{
		Root: &route.Root, Isolation: &route.Isolation, Model: &route.Model,
	}); err != nil {
		r.fail(ctx, *run, model.RunRouting, routerTags, "router: could not save the route on the task: "+clip(err.Error()))
		return false
	}
	if needed {
		how := route.Isolation
		if route.Model != "" {
			how += ", " + route.Model
		}
		r.note(ctx, actor, t.Number, routerTags, fmt.Sprintf("routed to %s (%s): %s", route.Root, how, route.Reason))
	}
	return true
}

// callRouter runs [agent] router with the task on stdin and parses its answer.
func (r *Runner) callRouter(ctx context.Context, t model.Task, roots []config.Root, models []string) (Route, error) {
	system := r.o.Config.Router.System
	if system == "" {
		system = r.o.Paths.RouterSystemFile()
		if err := config.WriteFileAtomic(system, []byte(herdrdesk.RouterSystem())); err != nil {
			return Route{}, fmt.Errorf("write the system prompt: %w", err)
		}
	}
	schema := Schema(roots, models)
	if f := r.o.Config.Router.Schema; f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return Route{}, fmt.Errorf("read [router] schema: %w", err)
		}
		schema = b
	}
	argv := config.Expand(r.o.Config.Agent.Router, map[string]string{"system": system, "schema": string(schema)})
	t.Project = listedProject(t.Project, roots)
	out, err := runChild(ctx, argv, RouterInput(t, roots, models), r.o.RouterTimeout, []string{"DESK_HOOKS=off"})
	if err != nil {
		return Route{}, err
	}
	return ParseRoute(out)
}
