package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestW6StepsAddUsesTheRequestedIDAndDoneReportsWhetherItChanged(t *testing.T) {
	home, _ := startOnlyHome(t, nil)
	addTask(t, home, "release checklist")

	added := runHomeDesk(t, home, "steps", "T1", "add", "--id", "release", "publish notes")
	requireSuccess(t, added)
	if added.stdout != "release [ ] publish notes\n" {
		t.Fatalf("steps add output = %q, want its requested id and text", added.stdout)
	}
	for _, want := range []string{"changed\n", "unchanged\n"} {
		result := runHomeDesk(t, home, "steps", "T1", "done", "release")
		requireSuccess(t, result)
		if result.stdout != want {
			t.Fatalf("steps done output = %q, want %q", result.stdout, want)
		}
	}

	result := runHomeDesk(t, home, "show", "T1", "--json")
	requireSuccess(t, result)
	var detail store.TaskDetail
	if err := json.Unmarshal([]byte(result.stdout), &detail); err != nil {
		t.Fatalf("decode task after done: %v; output=%q", err, result.stdout)
	}
	if len(detail.Task.Steps) != 1 || detail.Task.Steps[0].ShortID != "release" || !detail.Task.Steps[0].Done {
		t.Fatalf("steps after done = %#v, want one completed requested-id step", detail.Task.Steps)
	}
}

func TestW6StepsRejectsIDOutsideAddAndNamesEveryOperationInUsage(t *testing.T) {
	home, _ := startOnlyHome(t, nil)
	addTask(t, home, "guard the parser")

	result := runHomeDesk(t, home, "steps", "T1", "toggle", "--id", "not-allowed", "s1")
	if result.exit != 2 {
		t.Fatalf("steps toggle --id exit = %d, want 2; stderr=%q", result.exit, result.stderr)
	}
	if result.stdout != "" {
		t.Fatalf("steps toggle --id stdout = %q, want empty", result.stdout)
	}
	for _, operation := range []string{"add [--id <id>] <text>", "toggle <id>", "done <id>", "rename <id> <text>", "remove <id>"} {
		if !strings.Contains(result.stderr, operation) {
			t.Errorf("usage error = %q, want operation %q", result.stderr, operation)
		}
	}
}
