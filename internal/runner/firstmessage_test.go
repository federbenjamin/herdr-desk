package runner_test

import (
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestW5ResolveFirstMessageUsesTheHighestConfiguredTier(t *testing.T) {
	roots := []config.Root{{
		Path:         "/repo",
		Isolation:    "in-place",
		FirstMessage: "root {" + model.TaskFile + "}",
	}}

	for _, tt := range []struct {
		name  string
		task  model.Task
		given store.RunRoute
		want  string
	}{
		{
			name:  "the given route wins over the task and root",
			task:  model.Task{FirstMessage: "task {" + model.TaskFile + "}"},
			given: store.RunRoute{FirstMessage: "given {" + model.TaskFile + "}"},
			want:  "given {" + model.TaskFile + "}",
		},
		{
			name: "the task wins over the root",
			task: model.Task{FirstMessage: "task {" + model.TaskFile + "}"},
			want: "task {" + model.TaskFile + "}",
		},
		{
			name: "the resolved root supplies the fallback",
			want: "root {" + model.TaskFile + "}",
		},
		{
			name: "no configured value stays empty",
			want: "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			caseRoots := roots
			if tt.want == "" {
				caseRoots = []config.Root{{Path: "/repo", Isolation: "in-place"}}
			}
			got, err := runner.Resolve(tt.task, tt.given, caseRoots, nil)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got.FirstMessage != tt.want {
				t.Errorf("Resolve().FirstMessage = %q, want %q", got.FirstMessage, tt.want)
			}
		})
	}
}

func TestW5ResolveRejectsInvalidFirstMessageAtEveryTier(t *testing.T) {
	for _, tt := range []struct {
		name  string
		task  model.Task
		given store.RunRoute
		roots []config.Root
	}{
		{
			name:  "given route",
			given: store.RunRoute{FirstMessage: "begin immediately"},
			roots: []config.Root{{Path: "/repo", Isolation: "in-place"}},
		},
		{
			name:  "task",
			task:  model.Task{FirstMessage: "begin immediately"},
			roots: []config.Root{{Path: "/repo", Isolation: "in-place"}},
		},
		{
			name:  "resolved root",
			roots: []config.Root{{Path: "/repo", Isolation: "in-place", FirstMessage: "begin immediately"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runner.Resolve(tt.task, tt.given, tt.roots, nil)
			refusal, ok := model.AsRefusal(err)
			if !ok || refusal.Code != model.CodeBadInput || !strings.Contains(err.Error(), "first_message") {
				t.Fatalf("Resolve() error = %v; want bad-input naming first_message", err)
			}
		})
	}
}
