package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/cli"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/testutil"
)

func runDeskOnTerminal(t *testing.T, home *testutil.Home, stdin string, args ...string) commandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return commandResult{
		exit: cli.Run(context.Background(), args, cli.Env{
			Stdin:    strings.NewReader(stdin),
			Stdout:   &stdout,
			Stderr:   &stderr,
			Getenv:   home.Getenv(nil),
			Cwd:      t.TempDir(),
			StdinTTY: true,
		}),
		stdout: stdout.String(),
		stderr: stderr.String(),
	}
}

// prompts counts the "capture: " prompts in stderr, which are not the "desk capture: " that starts an error line.
func prompts(stderr string) int {
	return strings.Count(stderr, "capture: ") - strings.Count(stderr, "desk capture: ")
}

func TestCaptureOnATerminalShowsARefusalAndAsksAgain(t *testing.T) {
	for _, test := range []struct {
		name       string
		stdin      string
		wantRefuse []string
		wantStdout string
	}{
		{"unknown project then an empty line", "oops @nosuch\n\n", []string{model.CodeUnknownProject}, ""},
		{"empty title then end of input", "#only\n", []string{model.CodeEmptyTitle}, ""},
		{"a secret then a good line", "AKIAIOSFODNN7EXAMPLE\nfix the popup #ops\n", []string{model.CodeSecretDetected}, "T1\n"},
		{"two refusals then a good line", "a @nosuch\n#only\nfix the popup\n", []string{model.CodeUnknownProject, model.CodeEmptyTitle}, "T1\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := testutil.StartHome(t, testutil.HomeOptions{})
			result := runDeskOnTerminal(t, home, test.stdin, "capture")
			requireSuccess(t, result)
			if result.stdout != test.wantStdout {
				t.Errorf("stdout = %q, want %q", result.stdout, test.wantStdout)
			}
			for _, code := range test.wantRefuse {
				if !strings.Contains(result.stderr, "desk capture: "+code+": ") {
					t.Errorf("stderr = %q, want the refusal %s shown", result.stderr, code)
				}
			}
			if got := prompts(result.stderr); got != len(test.wantRefuse)+1 {
				t.Errorf("stderr = %q, holds %d prompts, want %d: one more after each refusal", result.stderr, got, len(test.wantRefuse)+1)
			}
		})
	}
}

func TestCaptureOffATerminalStopsAtTheFirstRefusal(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	result := runDesk(t, home.Getenv(nil), t.TempDir(), "oops @nosuch\nsecond line\n", "capture")
	requireRefusal(t, result, "capture", model.CodeUnknownProject, 1)
	if got := prompts(result.stderr); got != 0 {
		t.Errorf("stderr = %q, holds %d prompts, want none off a terminal", result.stderr, got)
	}
	if list := runHomeDesk(t, home, "list"); list.stdout != "" {
		t.Errorf("tasks after a refused capture = %q, want none", list.stdout)
	}
}
