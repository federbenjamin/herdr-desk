package cli_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestW6RunStartKeepsItsFirstMessageOnTheRun(t *testing.T) {
	home, root := startOnlyHome(t, nil)
	task := addTask(t, home, "template run")
	template := "Read {task_file} before you begin."

	result := runHomeDesk(t, home, "run", "start", fmt.Sprintf("T%d", task), "--root", root, "--first-message", template, "--json")
	requireSuccess(t, result)
	var run model.Run
	if err := json.Unmarshal([]byte(result.stdout), &run); err != nil {
		t.Fatalf("decode run start JSON: %v; output=%q", err, result.stdout)
	}
	if run.FirstMessage != template {
		t.Fatalf("run first_message = %q, want %q", run.FirstMessage, template)
	}
}

func TestW6SetFirstMessageShowsItAndAnEmptyFlagClearsIt(t *testing.T) {
	home, _ := startOnlyHome(t, nil)
	task := addTask(t, home, "remember the template")
	template := "Open {task_file}."

	requireSuccess(t, runHomeDesk(t, home, "set", fmt.Sprintf("T%d", task), "--first-message", template))
	shown := runHomeDesk(t, home, "show", fmt.Sprintf("T%d", task))
	requireSuccess(t, shown)
	if !strings.Contains(shown.stdout, "model:") || !strings.Contains(shown.stdout, "first_message: "+template) {
		t.Fatalf("show output = %q, want first_message under the task route", shown.stdout)
	}

	requireSuccess(t, runHomeDesk(t, home, "set", fmt.Sprintf("T%d", task), "--first-message", ""))
	result := runHomeDesk(t, home, "show", fmt.Sprintf("T%d", task), "--json")
	requireSuccess(t, result)
	var detail store.TaskDetail
	if err := json.Unmarshal([]byte(result.stdout), &detail); err != nil {
		t.Fatalf("decode cleared task: %v; output=%q", err, result.stdout)
	}
	if detail.Task.FirstMessage != "" {
		t.Fatalf("task first_message after --first-message '' = %q, want empty", detail.Task.FirstMessage)
	}
}
