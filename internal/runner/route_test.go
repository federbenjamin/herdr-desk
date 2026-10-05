package runner_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
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

func TestSchemaConstrainsEveryRouteFieldAndRefusesExtraFields(t *testing.T) {
	t.Parallel()

	roots := []config.Root{{Path: "/repos/one"}, {Path: "/repos/two"}}
	for _, tt := range []struct {
		name       string
		models     []string
		wantModel  bool
		wantNeeded []string
	}{
		{
			name:       "models are required when configured",
			models:     []string{"gpt-5.6", "fast"},
			wantModel:  true,
			wantNeeded: []string{"root", "isolation", "model", "reason"},
		},
		{
			name:       "model is absent when no models are configured",
			wantNeeded: []string{"root", "isolation", "reason"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var schema map[string]any
			if err := json.Unmarshal(runner.Schema(roots, tt.models), &schema); err != nil {
				t.Fatalf("Schema() is not valid JSON: %v", err)
			}
			if got, ok := schema["additionalProperties"].(bool); !ok || got {
				t.Errorf("Schema().additionalProperties = %#v; want false", schema["additionalProperties"])
			}
			properties, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("Schema().properties = %#v; want object", schema["properties"])
			}
			assertSchemaStringEnum(t, properties, "root", []string{"/repos/one", "/repos/two"})
			assertSchemaStringEnum(t, properties, "isolation", []string{"worktree", "in-place"})
			assertSchemaString(t, properties, "reason")
			_, hasModel := properties["model"]
			if hasModel != tt.wantModel {
				t.Errorf("Schema() has model property = %t; want %t", hasModel, tt.wantModel)
			}
			if tt.wantModel {
				assertSchemaStringEnum(t, properties, "model", tt.models)
			}
			assertStringSet(t, schema["required"], tt.wantNeeded)
		})
	}
}

func TestRouterInputIncludesOnlyTheRouterContractFields(t *testing.T) {
	t.Parallel()

	task := model.Task{
		Number:    42,
		Title:     "Route this",
		Notes:     "A useful note",
		Project:   "/repos/one/subdir",
		Status:    model.StatusReady,
		Thread:    "agent",
		Root:      "/ignored",
		Isolation: "self",
		Model:     "ignored",
		Steps: []model.Step{
			{ShortID: "s1", Text: "first", Done: false},
			{ShortID: "s2", Text: "second", Done: true},
		},
	}
	roots := []config.Root{{Path: "/repos/one", About: "one", Isolation: "worktree"}}

	var got map[string]any
	if err := json.Unmarshal(runner.RouterInput(task, roots, []string{"gpt-5.6"}), &got); err != nil {
		t.Fatalf("RouterInput() is not valid JSON: %v", err)
	}
	want := map[string]any{
		"task": map[string]any{
			"number":  float64(42),
			"title":   "Route this",
			"notes":   "A useful note",
			"project": "/repos/one/subdir",
			"steps": []any{
				map[string]any{"short_id": "s1", "text": "first", "done": false},
				map[string]any{"short_id": "s2", "text": "second", "done": true},
			},
		},
		"roots":  []any{map[string]any{"path": "/repos/one", "about": "one", "isolation": "worktree"}},
		"models": []any{"gpt-5.6"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RouterInput() = %#v; want %#v", got, want)
	}
}

func TestParseRouteAcceptsClaudeOutputShapesWithoutValidatingValues(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		out  string
		want runner.Route
	}{
		{
			name: "top level route",
			out:  `{"root":"/not-listed","isolation":"not-an-isolation","model":"not-a-model","reason":"  chosen directly  "}`,
			want: runner.Route{Root: "/not-listed", Isolation: "not-an-isolation", Model: "not-a-model", Reason: "chosen directly"},
		},
		{
			name: "structured output beside claude metadata",
			out:  `{"type":"result","structured_output":{"root":"/repos/one","isolation":"worktree","model":"fast","reason":"picked"},"result":"unused"}`,
			want: runner.Route{Root: "/repos/one", Isolation: "worktree", Model: "fast", Reason: "picked"},
		},
		{
			name: "reason is trimmed and capped",
			out:  `{"root":"/repos/one","reason":" ` + strings.Repeat("x", 502) + ` "}`,
			want: runner.Route{Root: "/repos/one", Reason: strings.Repeat("x", 500)},
		},
		{
			name: "object without route fields is an empty route",
			out:  `{"result":"no route here"}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := runner.ParseRoute([]byte(tt.out))
			if err != nil {
				t.Fatalf("ParseRoute() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseRoute() = %#v; want %#v", got, tt.want)
			}
		})
	}
}

func TestParseRouteRefusesNonObjectOutput(t *testing.T) {
	t.Parallel()

	for _, out := range []string{
		"not JSON",
		`[{"root":"/repos/one"}]`,
		`{"structured_output":{"root":"/repos/one","isolation":"worktree","model":"fast","reason":"r"}} diagnostic`,
		`{"root":"/repos/one"} {"root":"/repos/two"}`,
		`{"root":"/repos/one"} ]`,
	} {
		t.Run(out, func(t *testing.T) {
			if _, err := runner.ParseRoute([]byte(out)); err == nil {
				t.Fatalf("ParseRoute(%s) error = nil; want error", out)
			}
		})
	}
}

func TestResolveUsesTaskAndConfiguredValuesBeforeCallingRouter(t *testing.T) {
	t.Parallel()

	roots := []config.Root{
		{Path: "/repos/open"},
		{Path: "/repos/fixed", Isolation: "in-place"},
	}
	models := []string{"gpt-5.6", "fast"}

	for _, tt := range []struct {
		name      string
		task      model.Task
		roots     []config.Root
		models    []string
		want      runner.Route
		wantNeed  bool
		wantError string
	}{
		{
			name:   "complete task never needs router",
			task:   model.Task{Root: "/repos/open", Isolation: "worktree", Model: "fast"},
			roots:  roots,
			models: models,
			want:   runner.Route{Root: "/repos/open", Isolation: "worktree", Model: "fast"},
		},
		{
			name:   "configured root isolation decides it",
			task:   model.Task{Root: "/repos/fixed", Model: "fast"},
			roots:  roots,
			models: models,
			want:   runner.Route{Root: "/repos/fixed", Isolation: "in-place", Model: "fast"},
		},
		{
			name:     "no configured models decides empty model",
			task:     model.Task{Root: "/repos/open", Isolation: "worktree"},
			roots:    roots,
			models:   nil,
			want:     runner.Route{Root: "/repos/open", Isolation: "worktree"},
			wantNeed: false,
		},
		{
			name:     "missing root needs router",
			task:     model.Task{Isolation: "worktree", Model: "fast"},
			roots:    roots,
			models:   models,
			want:     runner.Route{Isolation: "worktree", Model: "fast"},
			wantNeed: true,
		},
		{
			name:     "task root remains decided while router chooses isolation",
			task:     model.Task{Root: "/repos/open", Model: "fast"},
			roots:    roots,
			models:   models,
			want:     runner.Route{Root: "/repos/open", Model: "fast"},
			wantNeed: true,
		},
		{
			name:     "root isolation remains decided while router chooses model",
			task:     model.Task{Root: "/repos/fixed"},
			roots:    roots,
			models:   models,
			want:     runner.Route{Root: "/repos/fixed", Isolation: "in-place"},
			wantNeed: true,
		},
		{
			name:     "empty models remains decided while router chooses root",
			task:     model.Task{Isolation: "worktree"},
			roots:    roots,
			models:   nil,
			want:     runner.Route{Isolation: "worktree"},
			wantNeed: true,
		},
		{
			name:      "unlisted decided root is refused",
			task:      model.Task{Root: "/not-listed", Isolation: "worktree", Model: "fast"},
			roots:     roots,
			models:    models,
			wantError: "root",
		},
		{
			name:      "invalid decided isolation is refused",
			task:      model.Task{Root: "/repos/open", Isolation: "bad", Model: "fast"},
			roots:     roots,
			models:    models,
			wantError: "isolation",
		},
		{
			name:      "unconfigured decided model is refused",
			task:      model.Task{Root: "/repos/open", Isolation: "worktree", Model: "bad"},
			roots:     roots,
			models:    models,
			wantError: "model",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, needsRouter, err := runner.Resolve(tt.task, tt.roots, tt.models)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("Resolve() error = %v; want error naming %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if needsRouter != tt.wantNeed {
				t.Errorf("Resolve() needsRouter = %t; want %t", needsRouter, tt.wantNeed)
			}
			if got != tt.want {
				t.Errorf("Resolve() route = %#v; want %#v", got, tt.want)
			}
		})
	}
}

func TestApplyCompletesOnlyOpenFieldsAndHonorsRootIsolation(t *testing.T) {
	t.Parallel()

	roots := []config.Root{
		{Path: "/repos/open"},
		{Path: "/repos/fixed", Isolation: "in-place"},
		{Path: "/repos/self", Isolation: "self"},
	}
	models := []string{"gpt-5.6", "fast"}

	for _, tt := range []struct {
		name      string
		decided   runner.Route
		picked    runner.Route
		want      runner.Route
		wantError string
	}{
		{
			name:    "task fields win over router pick",
			decided: runner.Route{Root: "/repos/open", Isolation: "worktree", Model: "fast"},
			picked:  runner.Route{Root: "/repos/fixed", Isolation: "in-place", Model: "gpt-5.6", Reason: "router reason"},
			want:    runner.Route{Root: "/repos/open", Isolation: "worktree", Model: "fast", Reason: "router reason"},
		},
		{
			name:    "configured root isolation wins over router pick",
			decided: runner.Route{Root: "/repos/fixed"},
			picked:  runner.Route{Root: "/repos/open", Isolation: "worktree", Model: "fast", Reason: "fixed root"},
			want:    runner.Route{Root: "/repos/fixed", Isolation: "in-place", Model: "fast", Reason: "fixed root"},
		},
		{
			name:      "router cannot select unlisted root",
			picked:    runner.Route{Root: "/not-listed", Isolation: "worktree", Model: "fast"},
			wantError: "root",
		},
		{
			name:      "router cannot select self isolation",
			picked:    runner.Route{Root: "/repos/open", Isolation: "self", Model: "fast"},
			wantError: "isolation",
		},
		{
			name:      "router cannot select unknown isolation",
			picked:    runner.Route{Root: "/repos/open", Isolation: "unknown", Model: "fast"},
			wantError: "isolation",
		},
		{
			name:      "router cannot select unconfigured model",
			picked:    runner.Route{Root: "/repos/open", Isolation: "worktree", Model: "unknown"},
			wantError: "model",
		},
		{
			name:    "task may select self isolation",
			decided: runner.Route{Root: "/repos/open", Isolation: "self", Model: "fast"},
			picked:  runner.Route{Root: "/repos/fixed", Isolation: "worktree", Model: "gpt-5.6"},
			want:    runner.Route{Root: "/repos/open", Isolation: "self", Model: "fast"},
		},
		{
			name:   "root configuration may select self isolation",
			picked: runner.Route{Root: "/repos/self", Isolation: "worktree", Model: "fast"},
			want:   runner.Route{Root: "/repos/self", Isolation: "self", Model: "fast"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := runner.Apply(tt.decided, tt.picked, roots, models)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("Apply() error = %v; want error naming %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Apply() = %#v; want %#v", got, tt.want)
			}
		})
	}
}

func TestApplyAndResolvePreferTheRootWrittenAsTheRouteOverASymlinkAlias(t *testing.T) {
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
	roots := []config.Root{{Path: alias, Isolation: "self"}, {Path: real, Isolation: "worktree"}}
	models := []string{"fast"}

	got, err := runner.Apply(runner.Route{Root: real}, runner.Route{Model: "fast"}, roots, models)
	if want := (runner.Route{Root: real, Isolation: "worktree", Model: "fast"}); err != nil || got != want {
		t.Fatalf("Apply() = %#v, %v; want %#v: the root written as the route, with its isolation", got, err, want)
	}
	decided, needed, err := runner.Resolve(model.Task{Root: real, Model: "fast"}, roots, models)
	if want := (runner.Route{Root: real, Isolation: "worktree", Model: "fast"}); err != nil || needed || decided != want {
		t.Fatalf("Resolve() = %#v, %t, %v; want %#v with no router", decided, needed, err, want)
	}
	got, err = runner.Apply(runner.Route{Root: other}, runner.Route{Model: "fast"}, roots, models)
	if want := (runner.Route{Root: alias, Isolation: "self", Model: "fast"}); err != nil || got != want {
		t.Fatalf("Apply() through an unlisted alias = %#v, %v; want the first root of that folder %#v", got, err, want)
	}
}

func TestRouterSystemTellsTheRouterHowToChooseAndRespond(t *testing.T) {
	t.Parallel()

	prompt := herdrdesk.RouterSystem()
	if strings.TrimSpace(prompt) == "" {
		t.Fatal("RouterSystem() is empty")
	}
	for _, instruction := range [][]string{
		{"Answer", "JSON object"},
		{"root", "project", "path"},
		{"no project", "last root listed"},
		{"worktree", "repository"},
	} {
		for _, keyword := range instruction {
			if !strings.Contains(prompt, keyword) {
				t.Errorf("RouterSystem() does not give the router an instruction recognizable by %q", keyword)
			}
		}
	}
}

func assertSchemaStringEnum(t *testing.T, properties map[string]any, field string, want []string) {
	t.Helper()
	property, ok := properties[field].(map[string]any)
	if !ok {
		t.Fatalf("Schema().properties.%s = %#v; want object", field, properties[field])
	}
	if got := property["type"]; got != "string" {
		t.Errorf("Schema().properties.%s.type = %#v; want string", field, got)
	}
	assertStringSet(t, property["enum"], want)
}

func assertSchemaString(t *testing.T, properties map[string]any, field string) {
	t.Helper()
	property, ok := properties[field].(map[string]any)
	if !ok {
		t.Fatalf("Schema().properties.%s = %#v; want object", field, properties[field])
	}
	if got := property["type"]; got != "string" {
		t.Errorf("Schema().properties.%s.type = %#v; want string", field, got)
	}
}

func assertStringSet(t *testing.T, got any, want []string) {
	t.Helper()
	values, ok := got.([]any)
	if !ok {
		t.Fatalf("JSON strings = %#v; want array", got)
	}
	actual := make([]string, len(values))
	for i, value := range values {
		var ok bool
		actual[i], ok = value.(string)
		if !ok {
			t.Fatalf("JSON strings[%d] = %#v; want string", i, value)
		}
	}
	if len(actual) != len(want) {
		t.Errorf("JSON strings = %#v; want %#v", actual, want)
		return
	}
	for _, expected := range want {
		if !contains(actual, expected) {
			t.Errorf("JSON strings = %#v; want %#v", actual, want)
			return
		}
	}
	if len(actual) != len(unique(actual)) {
		t.Errorf("JSON strings = %#v; want %#v", actual, want)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func unique(values []string) []string {
	var got []string
	for _, value := range values {
		if !contains(got, value) {
			got = append(got, value)
		}
	}
	return got
}
