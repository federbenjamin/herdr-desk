package model_test

import (
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

func TestValidFirstMessageRequiresTheTaskFilePlaceholder(t *testing.T) {
	if model.TaskFile != "task_file" {
		t.Fatalf("TaskFile = %q, want %q", model.TaskFile, "task_file")
	}

	for _, tc := range []struct {
		name  string
		input string
		want  bool
	}{
		{name: "empty falls back to the generated message", input: "", want: true},
		{name: "placeholder on its own", input: "{task_file}", want: true},
		{name: "placeholder in a command", input: "/build {task_file}", want: true},
		{name: "placeholder surrounded by text", input: "read {task_file} then continue", want: true},
		{name: "missing placeholder", input: "/build", want: false},
		{name: "old placeholder", input: "/build {task}", want: false},
		{name: "literal task file name", input: "/build task_file", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := model.ValidFirstMessage(tc.input); got != tc.want {
				t.Errorf("ValidFirstMessage(%q) = %t, want %t", tc.input, got, tc.want)
			}
		})
	}
}
