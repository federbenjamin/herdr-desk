package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/cli"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
)

func w7Run(t *testing.T, home *testutil.Home, args []string, stdin string, stdinTTY, stdoutTTY bool) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exit := cli.Run(context.Background(), args, cli.Env{
		Stdin:     strings.NewReader(stdin),
		Stdout:    &stdout,
		Stderr:    &stderr,
		Getenv:    home.Getenv(nil),
		Cwd:       t.TempDir(),
		StdinTTY:  stdinTTY,
		StdoutTTY: stdoutTTY,
	})
	return exit, stdout.String(), stderr.String()
}

func TestW7TerminalBareDeskUsesTheAlternateScreenUntilQuit(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})

	exit, stdout, stderr := w7Run(t, home, nil, "q", true, true)
	if exit != 0 {
		t.Fatalf("terminal desk exit = %d, want 0; stdout=%q stderr=%q", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "\x1b[?1049h") {
		t.Errorf("terminal desk stdout = %q, want the alternate-screen sequence", stdout)
	}
	if strings.Contains(stdout, "desk · home · runner") {
		t.Errorf("terminal desk stdout = %q, want the interactive board instead of the static board", stdout)
	}
}

func TestW7BareDeskKeepsTheStaticBoardWhenEitherStreamIsNotATerminal(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	if _, err := home.Client().AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "static task"}}); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		stdinTTY  bool
		stdoutTTY bool
	}{
		{name: "stdin is not a terminal", stdinTTY: false, stdoutTTY: true},
		{name: "stdout is not a terminal", stdinTTY: true, stdoutTTY: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			exit, stdout, stderr := w7Run(t, home, nil, "q", test.stdinTTY, test.stdoutTTY)
			if exit != 0 {
				t.Fatalf("static desk exit = %d, want 0; stdout=%q stderr=%q", exit, stdout, stderr)
			}
			for _, want := range []string{"desk · home · runner", "NEEDS YOU", "IN MOTION", "ON DECK", "T1  open  static task"} {
				if !strings.Contains(stdout, want) {
					t.Errorf("static desk stdout = %q, want %q", stdout, want)
				}
			}
			if strings.Contains(stdout, "\x1b[?1049h") {
				t.Errorf("static desk stdout = %q, must not enter the alternate screen", stdout)
			}
		})
	}
}

func TestW7JSONBareDeskStaysStaticEvenWithTwoTerminalStreams(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	if _, err := home.Client().AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "JSON task"}}); err != nil {
		t.Fatal(err)
	}

	exit, stdout, stderr := w7Run(t, home, []string{"--json"}, "q", true, true)
	if exit != 0 {
		t.Fatalf("desk --json exit = %d, want 0; stdout=%q stderr=%q", exit, stdout, stderr)
	}
	if strings.Contains(stdout, "\x1b[?1049h") {
		t.Errorf("desk --json stdout = %q, must not enter the alternate screen", stdout)
	}
	var list api.TaskList
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("desk --json stdout = %q, want TaskList JSON: %v", stdout, err)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].Title != "JSON task" {
		t.Errorf("desk --json tasks = %#v, want the static board task list", list.Tasks)
	}
}

func TestW7TerminalCaptureShowsTheCaptureBoxAndPrintsTheNewTask(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})

	exit, stdout, stderr := w7Run(t, home, []string{"capture"}, "from the capture box\n", true, true)
	if exit != 0 {
		t.Fatalf("terminal capture exit = %d, want 0; stdout=%q stderr=%q", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "capture: ") {
		t.Errorf("terminal capture stdout = %q, want the capture box", stdout)
	}
	if !strings.Contains(stdout, "T1") {
		t.Errorf("terminal capture stdout = %q, want the landed task number", stdout)
	}
	if strings.Contains(stdout, "\x1b[?1049h") {
		t.Errorf("terminal capture stdout = %q, must not use the alternate screen", stdout)
	}
	if stderr != "" {
		t.Errorf("terminal capture stderr = %q, want the capture UI on stdout", stderr)
	}
}
