package runner_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/desk"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/runner"
)

func TestRootsKeepsConfiguredOrderThenAddsInPlaceScratch(t *testing.T) {
	t.Parallel()

	c := config.Config{Roots: []config.Root{
		{Path: "/repos/first", About: "first", Isolation: "worktree"},
		{Path: "/repos/second", About: "second"},
	}}
	p := config.Paths{DataDir: "/desk-data"}

	got := runner.Roots(c, p)
	want := []config.Root{
		{Path: "/repos/first", About: "first", Isolation: "worktree"},
		{Path: "/repos/second", About: "second"},
		{Path: "/desk-data/scratch", Isolation: "in-place"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Roots() = %#v; want %#v", got, want)
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

func TestParseRouteRefusesNonObjectAndRouteLessOutput(t *testing.T) {
	t.Parallel()

	for _, out := range []string{
		"not JSON",
		`[{"root":"/repos/one"}]`,
		`{"result":"no route here"}`,
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

func TestRouterSystemTellsTheRouterHowToChooseAndRespond(t *testing.T) {
	t.Parallel()

	prompt := desk.RouterSystem()
	for _, want := range []string{
		"only the JSON object",
		"path is or contains the task's project",
		"no project",
		"last root listed",
		"worktree",
		"changes a repository's files",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("RouterSystem() does not tell the router %q", want)
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
