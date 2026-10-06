package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestStepsAddWithAnExistingIDPrintsTheStepsThenUnchanged(t *testing.T) {
	home, _ := startOnlyHome(t, nil)
	addTask(t, home, "resume a pipeline")

	first := runHomeDesk(t, home, "steps", "T1", "add", "--id", "u1", "unit one")
	requireSuccess(t, first)
	if first.stdout != "u1 [ ] unit one\n" {
		t.Fatalf("first add output = %q, want the steps alone", first.stdout)
	}
	again := runHomeDesk(t, home, "steps", "T1", "add", "--id", "u1", "unit one again")
	requireSuccess(t, again)
	if again.stdout != "u1 [ ] unit one\nunchanged\n" {
		t.Fatalf("second add output = %q, want the steps, then unchanged", again.stdout)
	}
}

func TestStepsJSONPrintsTheTaskAndWhetherEachOpChangedIt(t *testing.T) {
	home, _ := startOnlyHome(t, nil)
	addTask(t, home, "report every op")

	for _, test := range []struct {
		args    []string
		changed bool
		steps   int
	}{
		{[]string{"add", "generated"}, true, 1},
		{[]string{"add", "--id", "u1", "caller id"}, true, 2},
		{[]string{"add", "--id", "u1", "caller id again"}, false, 2},
		{[]string{"done", "u1"}, true, 2},
		{[]string{"done", "u1"}, false, 2},
		{[]string{"toggle", "s1"}, true, 2},
		{[]string{"rename", "s1", "renamed"}, true, 2},
		{[]string{"remove", "s1"}, true, 1},
	} {
		args := append([]string{"steps", "T1"}, test.args...)
		result := runHomeDesk(t, home, append(args, "--json")...)
		requireSuccess(t, result)
		var got api.StepResult
		decoder := json.NewDecoder(strings.NewReader(result.stdout))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&got); err != nil {
			t.Fatalf("%v: decode %q: %v", test.args, result.stdout, err)
		}
		if got.Task.Number != 1 || got.Changed != test.changed || len(got.Task.Steps) != test.steps {
			t.Fatalf("%v --json = task T%d, changed %v, %d steps; want T1, changed %v, %d steps",
				test.args, got.Task.Number, got.Changed, len(got.Task.Steps), test.changed, test.steps)
		}
	}
}

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
