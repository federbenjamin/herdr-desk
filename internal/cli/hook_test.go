package cli_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/testutil"
)

func TestClaudeHookWritesAPrivateJournalViewForEveryRenderingSource(t *testing.T) {
	for _, source := range []string{"startup", "resume", "clear", "fork"} {
		t.Run(source, func(t *testing.T) {
			home := testutil.StartHome(t, testutil.HomeOptions{})
			sessionID := "hook-" + source
			input := fmt.Sprintf(`{"source":%q,"session_id":%q}`, source, sessionID)
			result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"hook", "start", "--format", "claude-code"}, input, nil, nil)
			if result.exit != 0 {
				t.Fatalf("hook %s exit = %d, stderr = %q", source, result.exit, result.stderr)
			}

			path := filepath.Join(home.Paths.SessionsDir(), sessionID+".md")
			lines := strings.Split(strings.TrimSuffix(result.stdout, "\n"), "\n")
			if len(lines) != 3 {
				t.Fatalf("hook %s stdout lines = %q, want exactly three lines", source, result.stdout)
			}
			if lines[0] != "desk journal for this session: "+path {
				t.Fatalf("hook %s first line = %q, want journal path %q", source, lines[0], path)
			}
			if strings.TrimSpace(lines[1]) == "" || strings.TrimSpace(lines[2]) == "" {
				t.Fatalf("hook %s instructions = %q, want two non-empty record instructions", source, result.stdout)
			}
			view, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read hook %s view %q: %v", source, path, err)
			}
			if len(view) == 0 {
				t.Fatalf("hook %s wrote an empty view", source)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("hook %s view mode = %v, %v; want 0600", source, info.Mode(), err)
			}
		})
	}
}

func TestClaudeHookCompactAppendsBeforeWritingTheView(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	input := `{"source":"compact","session_id":"hook-compact"}`
	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"hook", "start", "--format", "claude-code"}, input, nil, nil)
	if result.exit != 0 {
		t.Fatalf("hook compact exit = %d, stderr = %q", result.exit, result.stderr)
	}
	view, err := home.Client().SessionView(context.Background(), "hook-compact")
	if err != nil {
		t.Fatalf("read compacted session: %v", err)
	}
	if len(view.Events) != 1 || view.Events[0].Kind != model.KindCompacted {
		t.Fatalf("compact events = %#v, want one compacted event", view.Events)
	}
	contents, err := os.ReadFile(filepath.Join(home.Paths.SessionsDir(), "hook-compact.md"))
	if err != nil {
		t.Fatalf("read compact view: %v", err)
	}
	if !strings.Contains(string(contents), "compacted") {
		t.Fatalf("compact view = %q, want compacted marker", contents)
	}
}

func TestClaudeHookOffSwitchDoesNotReadOrWriteAnything(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"hook", "start", "--format", "claude-code"}, "not JSON", map[string]string{"DESK_HOOKS": "off"}, nil)
	if result.exit != 0 || result.stdout != "" || result.stderr != "" {
		t.Fatalf("disabled hook = (%d, %q, %q), want silent success", result.exit, result.stdout, result.stderr)
	}
	if _, err := os.Stat(home.Paths.SessionsDir()); !errorsIsNotExist(err) {
		t.Fatalf("disabled hook created session directory: %v", err)
	}
}

func TestClaudeHookUnreachableHomePrintsOneLineAndSucceeds(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.NewClientMachine(t, home)
	home.Stop()
	result := runDeskWithEnv(t, client, t.TempDir(), []string{"hook", "start", "--format", "claude-code"}, `{"source":"startup","session_id":"offline-hook"}`, nil, nil)
	lines := strings.Split(strings.TrimSuffix(result.stdout, "\n"), "\n")
	if result.exit != 0 || len(lines) != 1 || !strings.Contains(lines[0], "did not answer") {
		t.Fatalf("offline hook = (%d, %q, %q), want one unreachable line", result.exit, result.stdout, result.stderr)
	}
	if _, err := os.Stat(filepath.Join(client.Paths.SessionsDir(), "offline-hook.md")); !errorsIsNotExist(err) {
		t.Fatalf("offline hook wrote a view: %v", err)
	}
}

func TestClaudeHookRejectsInvalidSessionIDsAndOtherFormatsWithoutWriting(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	for _, item := range []struct {
		name  string
		args  []string
		input string
		file  string
	}{
		{name: "invalid session", args: []string{"hook", "start", "--format", "claude-code"}, input: `{"source":"startup","session_id":"../escape"}`, file: "../escape.md"},
		{name: "other format", args: []string{"hook", "start", "--format", "other"}, input: `{"source":"startup","session_id":"valid-session"}`, file: "valid-session.md"},
	} {
		t.Run(item.name, func(t *testing.T) {
			result := runDeskWithEnv(t, home.Machine, t.TempDir(), item.args, item.input, nil, nil)
			if result.exit != 2 {
				t.Fatalf("%s exit = %d, want 2; stderr = %q", item.name, result.exit, result.stderr)
			}
			if result.stdout != "" {
				t.Fatalf("%s stdout = %q, want no output", item.name, result.stdout)
			}
			path := filepath.Join(home.Paths.SessionsDir(), item.file)
			if _, err := os.Stat(path); !errorsIsNotExist(err) {
				t.Fatalf("%s wrote %q: %v", item.name, path, err)
			}
		})
	}
}

func errorsIsNotExist(err error) bool {
	return err != nil && (os.IsNotExist(err) || errors.Is(err, fs.ErrNotExist))
}
