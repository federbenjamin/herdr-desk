package runner_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestRootsKeepsConfiguredOrderThenAddsInPlaceScratch(t *testing.T) {
	t.Parallel()

	c := config.Config{Roots: []config.Root{
		{Path: "/repos/first", About: "first", Isolation: "worktree"},
		{Path: "/repos/second", About: "second"},
	}}
	p := config.Paths{DataDir: "/desk-data"}

	got := runner.Roots(c, p)
	if len(got) != len(c.Roots)+1 {
		t.Fatalf("Roots() returned %d roots; want %d", len(got), len(c.Roots)+1)
	}
	if !reflect.DeepEqual(got[:len(c.Roots)], c.Roots) {
		t.Errorf("Roots() configured roots = %#v; want %#v", got[:len(c.Roots)], c.Roots)
	}
	scratch := got[len(c.Roots)]
	if scratch.Path != p.ScratchRoot() || scratch.Isolation != "in-place" {
		t.Errorf("Roots() scratch root = %#v; want path %q and in-place isolation", scratch, p.ScratchRoot())
	}
}

func TestRootsListsTheScratchRootOnceLastAndInPlace(t *testing.T) {
	t.Parallel()

	p := config.Paths{DataDir: "/desk-data"}
	c := config.Config{Roots: []config.Root{
		{Path: p.ScratchRoot() + "/", About: "scratch as a repo", Isolation: "worktree"},
		{Path: "/repos/one", About: "one", Isolation: "self"},
	}}
	got := runner.Roots(c, p)
	if len(got) != 2 || got[0] != c.Roots[1] || got[1].Path != p.ScratchRoot() || got[1].Isolation != "in-place" {
		t.Fatalf("Roots() = %#v; want /repos/one, then the scratch root once with in-place isolation", got)
	}
}

func TestResolveTakesGivenThenTaskThenDefaultsAndChecksTheRoute(t *testing.T) {
	t.Parallel()

	roots := []config.Root{
		{Path: "/repos/open"},
		{Path: "/repos/fixed", Isolation: "worktree"},
		{Path: "/scratch", Isolation: "in-place"},
	}
	models := []string{"gpt-5.6", "fast"}

	for _, tt := range []struct {
		name      string
		task      model.Task
		given     store.RunRoute
		models    []string
		want      store.RunRoute
		wantError string
	}{
		{
			name:   "the task's own fields",
			task:   model.Task{Root: "/repos/open", Isolation: "self", Model: "fast"},
			models: models,
			want:   store.RunRoute{Root: "/repos/open", Isolation: "self", Model: "fast"},
		},
		{
			name:   "given fields win over the task's",
			task:   model.Task{Root: "/repos/open", Isolation: "self", Model: "fast"},
			given:  store.RunRoute{Root: "/repos/fixed", Isolation: "in-place", Model: "gpt-5.6"},
			models: models,
			want:   store.RunRoute{Root: "/repos/fixed", Isolation: "in-place", Model: "gpt-5.6"},
		},
		{
			name:   "the root's isolation, then the first model",
			task:   model.Task{Root: "/repos/fixed"},
			models: models,
			want:   store.RunRoute{Root: "/repos/fixed", Isolation: "worktree", Model: "gpt-5.6"},
		},
		{
			name:   "a project that is a listed root",
			task:   model.Task{Project: "/repos/fixed/"},
			models: models,
			want:   store.RunRoute{Root: "/repos/fixed", Isolation: "worktree", Model: "gpt-5.6"},
		},
		{
			name:   "a project under no listed root runs in the scratch root",
			task:   model.Task{Project: "/elsewhere/repo"},
			models: nil,
			want:   store.RunRoute{Root: "/scratch", Isolation: "in-place"},
		},
		{
			name:   "a root that is not a git work tree defaults to in-place",
			task:   model.Task{Root: "/repos/open"},
			models: models,
			want:   store.RunRoute{Root: "/repos/open", Isolation: "in-place", Model: "gpt-5.6"},
		},
		{
			name:      "an unlisted root is refused",
			given:     store.RunRoute{Root: "/not-listed"},
			models:    models,
			wantError: "root",
		},
		{
			name:      "an invalid isolation is refused",
			task:      model.Task{Root: "/repos/open", Isolation: "bad"},
			models:    models,
			wantError: "isolation",
		},
		{
			name:      "a model outside the models is refused",
			given:     store.RunRoute{Model: "bad"},
			models:    models,
			wantError: "model",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := runner.Resolve(tt.task, tt.given, roots, tt.models)
			if tt.wantError != "" {
				if r, ok := model.AsRefusal(err); !ok || r.Code != model.CodeBadInput || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("Resolve() error = %v; want bad-input naming %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Resolve() = %#v; want %#v", got, tt.want)
			}
		})
	}
}

func TestResolveDefaultsToWorktreeForARootAtTheTopOfAGitWorkTree(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fireGitRoot(t, root)
	got, err := runner.Resolve(model.Task{Root: root}, store.RunRoute{}, []config.Root{{Path: root}}, nil)
	if want := (store.RunRoute{Root: root, Isolation: "worktree"}); err != nil || got != want {
		t.Fatalf("Resolve() = %#v, %v; want %#v", got, err, want)
	}
}

func TestResolvePrefersTheRootWrittenAsTheRouteOverASymlinkAlias(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if err := os.Symlink(real, other); err != nil {
		t.Fatal(err)
	}
	roots := []config.Root{{Path: alias, Isolation: "self"}, {Path: real, Isolation: "in-place"}}
	models := []string{"fast"}

	got, err := runner.Resolve(model.Task{Root: real}, store.RunRoute{}, roots, models)
	if want := (store.RunRoute{Root: real, Isolation: "in-place", Model: "fast"}); err != nil || got != want {
		t.Fatalf("Resolve() = %#v, %v; want %#v: the root written as the route, with its isolation", got, err, want)
	}
	got, err = runner.Resolve(model.Task{}, store.RunRoute{Root: other}, roots, models)
	if want := (store.RunRoute{Root: alias, Isolation: "self", Model: "fast"}); err != nil || got != want {
		t.Fatalf("Resolve() through an unlisted alias = %#v, %v; want the first root of that folder %#v", got, err, want)
	}
}
