package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// claudeRig is a user's machine in a temp folder: a native claude install, ~/.local/bin/claude linked into
// ~/.local/share/claude/versions, a claude that writes the environment it was given, and a herdr-desk that does the same.
type claudeRig struct {
	dir, home, link, out string
}

func newClaudeRig(t *testing.T) *claudeRig {
	t.Helper()
	dir := t.TempDir()
	r := &claudeRig{dir: dir, home: filepath.Join(dir, "user"), out: filepath.Join(dir, "out")}
	version := filepath.Join(r.home, ".local", "share", "claude", "versions", "1.0.0")
	r.link = filepath.Join(r.home, ".local", "bin", "claude")
	for _, d := range []string{filepath.Dir(version), filepath.Dir(r.link), r.out} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExec(t, version, "#!/bin/sh\nenv >'"+r.out+"/claude.env'\nexec herdr-desk hook start\n")
	if err := os.Symlink(version, r.link); err != nil {
		t.Fatal(err)
	}
	return r
}

// run runs the driver after sourcing lib.sh, with the caller's environment env, and returns its exit code and stderr.
func (r *claudeRig) run(t *testing.T, driver string, env ...string) (int, string) {
	t.Helper()
	lib, err := filepath.Abs(filepath.Join("e2e", "lib.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(r.dir, "driver.sh")
	if err := os.WriteFile(script, []byte("source '"+lib+"'\n"+driver), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Env = append([]string{
		"HOME=" + r.home, "TMPDIR=" + r.dir, "OUT_DIR=" + r.out,
		"PATH=" + filepath.Dir(r.link) + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run the driver: %v", err)
		}
		code = exit.ExitCode()
	}
	return code, stderr.String()
}

// envOf reads a file `env` wrote into a map.
func envOf(t *testing.T, path string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, line := range strings.Split(readFile(t, path), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			m[k] = v
		}
	}
	return m
}

// The real claude never sees a machine's temp XDG folders: it runs on the caller's own (unset where the caller had
// none), with no inherited session marker and its updater off, while the herdr-desk it runs reaches the home's temp
// folders.
func TestClaudeWrapperRunsClaudeOnTheCallersOwnFolders(t *testing.T) {
	r := newClaudeRig(t)
	code, stderr := r.run(t, `mkdir -p "$BIN"
printf '#!/bin/sh\nenv >"%s/desk.env"\n' "$OUT_DIR" >"$BIN/herdr-desk"
chmod +x "$BIN/herdr-desk"
claude_wrapper session
machine_env home
env "${MACHINE_ENV[@]:1}" CLAUDE_CODE_CHILD_SESSION=1 CLAUDE_CODE_ENTRYPOINT=cli CLAUDECODE=1 "$CLAUDE_WRAP" -p hello
printf '%s %s\n' "$(claude_calls session)" "$E2E" >"$OUT_DIR/calls"
`, "XDG_CONFIG_HOME=/caller/config", "XDG_STATE_HOME=/caller/state")
	if code != 0 {
		t.Fatalf("driver exit %d: %s", code, stderr)
	}
	calls, e2e, _ := strings.Cut(strings.TrimSpace(readFile(t, filepath.Join(r.out, "calls"))), " ")
	if calls != "1" {
		t.Fatalf("claude_calls session = %q, want 1", calls)
	}
	claude := envOf(t, filepath.Join(r.out, "claude.env"))
	for k, want := range map[string]string{
		"XDG_CONFIG_HOME": "/caller/config", "XDG_STATE_HOME": "/caller/state", "DISABLE_AUTOUPDATER": "1",
	} {
		if claude[k] != want {
			t.Errorf("claude saw %s=%q, want %q", k, claude[k], want)
		}
	}
	for k := range claude {
		if k == "XDG_DATA_HOME" || k == "XDG_CACHE_HOME" || k == "CLAUDECODE" || strings.HasPrefix(k, "CLAUDE_CODE_") {
			t.Errorf("claude saw %s=%q, want it unset", k, claude[k])
		}
	}
	if first, _, _ := strings.Cut(claude["PATH"], ":"); first != filepath.Join(e2e, "agent-bin") {
		t.Errorf("claude's PATH starts with %q, want %s/agent-bin", first, e2e)
	}
	desk := envOf(t, filepath.Join(r.out, "desk.env"))
	for _, k := range []string{"config", "state", "data", "cache"} {
		name := "XDG_" + strings.ToUpper(k) + "_HOME"
		if want := filepath.Join(e2e, "home", k); desk[name] != want {
			t.Errorf("the herdr-desk claude ran saw %s=%q, want the home's %q", name, desk[name], want)
		}
	}
}

// A script fails (env) when the user's claude does not resolve before it starts one, or resolves elsewhere at exit.
func TestClaudeGuardFailsWhenTheUsersClaudeIsMissingOrMoved(t *testing.T) {
	r := newClaudeRig(t)
	moved := `claude_guard
ln -sfn "$HOME/.local/share/claude/versions/gone" "$HOME/.local/bin/claude"
say done
`
	if code, stderr := r.run(t, moved); code != 1 || !strings.Contains(stderr, "E2E FAIL: (env) the user's claude moved during the test") {
		t.Fatalf("a script whose claude moved: exit %d, stderr %q; want exit 1 and the (env) failure", code, stderr)
	}
	if code, stderr := r.run(t, "claude_guard\nsay started\n"); code != 1 || !strings.Contains(stderr, "E2E FAIL: (env) the user's claude does not resolve") {
		t.Fatalf("a script whose claude did not resolve at the start: exit %d, stderr %q; want exit 1 and the (env) failure", code, stderr)
	}
	if err := os.Remove(r.link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(r.home, ".local", "share", "claude", "versions", "1.0.0"), r.link); err != nil {
		t.Fatal(err)
	}
	if code, stderr := r.run(t, "claude_guard\nsay started\n"); code != 0 {
		t.Fatalf("a script whose claude stayed put: exit %d, stderr %q; want 0", code, stderr)
	}
}
