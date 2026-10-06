package scripts_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
)

const (
	testVersion = "1.2.3"
	testBaseURL = "https://example.invalid/releases"
	prebuilt    = "#!/bin/sh\necho prebuilt\n"
)

// curlStub copies the file under $FIXTURE that matches the URL's path below $DESK_BASE_URL, and
// fails like curl -f on a 404 when there is none.
const curlStub = `#!/bin/sh
while [ $# -gt 0 ]; do
  case "$1" in
    -o) dest=$2; shift 2 ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
rel=${url#"$DESK_BASE_URL"/}
[ -f "$FIXTURE/$rel" ] || exit 22
cp "$FIXTURE/$rel" "$dest"
`

// goStub records its working directory and arguments, and writes a marker binary where -o points.
const goStub = `#!/bin/sh
{ pwd; echo "$@"; } >>"$GO_LOG"
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    printf '#!/bin/sh\necho built\n' >"$2"
    chmod +x "$2"
  fi
  shift
done
`

const deskStub = `#!/bin/sh
if IFS= read -r _; then
  echo "herdr-desk read stdin" >>"$COMMAND_LOG"
  exit 97
fi
echo "herdr-desk $*" >>"$COMMAND_LOG"
echo "setup output"
echo "setup stderr" >&2
exit "${DESK_EXIT:-0}"
`

const claudeStub = `#!/bin/sh
if IFS= read -r _; then
  echo "claude $* read stdin" >>"$COMMAND_LOG"
  exit 97
fi
echo "claude $*" >>"$COMMAND_LOG"
echo "claude $* output"
case "$1 $2 $3" in
  "plugin marketplace add") exit "${CLAUDE_MARKETPLACE_EXIT:-0}" ;;
  "plugin install herdr-desk@herdr-desk") exit "${CLAUDE_INSTALL_EXIT:-0}" ;;
esac
exit 0
`

type rig struct {
	dir     string
	fixture string
	out     string
	install string
	goLog   string
	env     []string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	switch runtime.GOOS {
	case "darwin", "linux":
	default:
		t.Skipf("no release for %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "arm64", "amd64":
	default:
		t.Skipf("no release for %s", runtime.GOARCH)
	}

	dir := t.TempDir()
	r := &rig{
		dir:     dir,
		fixture: filepath.Join(dir, "fixture"),
		out:     filepath.Join(dir, "out", "herdr-desk"),
		install: filepath.Join(dir, "inst"),
		goLog:   filepath.Join(dir, "go.log"),
	}
	stubs := filepath.Join(dir, "stubs")
	repo := filepath.Join(dir, "repo")
	for _, d := range []string{stubs, repo, filepath.Join(r.fixture, "v"+testVersion)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExec(t, filepath.Join(stubs, "curl"), curlStub)
	writeExec(t, filepath.Join(stubs, "go"), goStub)
	r.env = []string{
		"HOME=" + dir,
		"PATH=" + stubs + ":/usr/bin:/bin",
		"FIXTURE=" + r.fixture,
		"GO_LOG=" + r.goLog,
		"DESK_REPO_ROOT=" + repo,
		"DESK_VERSION=" + testVersion,
		"DESK_BASE_URL=" + testBaseURL,
		"DESK_OUT=" + r.out,
		"DESK_INSTALL_DIR=" + r.install,
		"DESK_GO=" + filepath.Join(stubs, "go"),
	}
	return r
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// release puts the archive and checksums.txt where the curl stub will find them.
func (r *rig) release(t *testing.T) {
	t.Helper()
	asset := fmt.Sprintf("herdr-desk_%s_%s_%s.tar.gz", testVersion, runtime.GOOS, runtime.GOARCH)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "herdr-desk", Mode: 0o755, Size: int64(len(prebuilt))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(prebuilt)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	folder := filepath.Join(r.fixture, "v"+testVersion)
	if err := os.WriteFile(filepath.Join(folder, asset), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sums := fmt.Sprintf("%x  %s\n", sha256.Sum256(buf.Bytes()), asset)
	if err := os.WriteFile(filepath.Join(folder, "checksums.txt"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) run(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("sh", "fetch-or-build.sh")
	cmd.Env = r.env
	cmd.Stdin = strings.NewReader("this input must not reach setup or claude\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fetch-or-build.sh failed: %v\n%s", err, out)
	}
	return string(out)
}

func (r *rig) commandLog(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, "commands.log"))
	if err == nil {
		return string(b)
	}
	if os.IsNotExist(err) {
		return ""
	}
	t.Fatal(err)
	return ""
}

func (r *rig) addDesk(t *testing.T) {
	t.Helper()
	writeExec(t, filepath.Join(r.dir, "stubs", "herdr-desk"), deskStub)
	r.env = append(r.env, "COMMAND_LOG="+filepath.Join(r.dir, "commands.log"))
}

func (r *rig) addClaude(t *testing.T) string {
	t.Helper()
	path := filepath.Join(r.dir, "stubs", "claude")
	writeExec(t, path, claudeStub)
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFetchOrBuildPrebuilt(t *testing.T) {
	r := newRig(t)
	r.release(t)

	out := r.run(t)

	if got := readFile(t, r.out); got != prebuilt {
		t.Errorf("DESK_OUT holds %q, want the released binary", got)
	}
	if got := readFile(t, filepath.Join(r.install, "herdr-desk")); got != prebuilt {
		t.Errorf("the install dir holds %q, want the released binary", got)
	}
	if _, err := os.Stat(r.goLog); err == nil {
		t.Error("go was called although a verified release exists")
	}
	if !strings.Contains(out, "checksum verified") {
		t.Errorf("output does not say the checksum was verified:\n%s", out)
	}
}

func TestFetchOrBuildFallsBackToSource(t *testing.T) {
	r := newRig(t)

	out := r.run(t)

	if got := readFile(t, r.out); !strings.Contains(got, "built") {
		t.Errorf("DESK_OUT holds %q, want the binary the go stub wrote", got)
	}
	log := readFile(t, r.goLog)
	if !strings.Contains(log, "-o "+r.out+" ./cmd/herdr-desk") || !strings.Contains(log, "build ") {
		t.Errorf("go was not asked to build ./cmd/herdr-desk into DESK_OUT:\n%s", log)
	}
	if want := "-X github.com/federbenjamin/herdr-desk/internal/version.Version=" + testVersion + "+src"; !strings.Contains(log, want) {
		t.Errorf("go was not asked to set the version to the manifest's with a +src suffix (%s):\n%s", want, log)
	}
	if !strings.Contains(log, filepath.Join(r.dir, "repo")) {
		t.Errorf("go did not run in DESK_REPO_ROOT:\n%s", log)
	}
	if !strings.Contains(out, "building from source") {
		t.Errorf("output does not say it fell back to source:\n%s", out)
	}
}

func TestFetchOrBuildReplacesTheDeskItInstalledBefore(t *testing.T) {
	r := newRig(t)
	r.release(t)
	if err := os.MkdirAll(r.install, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(r.install, "herdr-desk")
	writeExec(t, old, "#!/bin/sh\necho old\n")
	before, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	r.env = append(r.env, "PATH="+r.install+":"+filepath.Join(r.dir, "stubs")+":/usr/bin:/bin")

	out := r.run(t)

	if got := readFile(t, old); got != prebuilt {
		t.Errorf("the install dir holds %q, want the released binary", got)
	}
	after, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Error("the installed herdr-desk was written in place; want a new file renamed over it")
	}
	if !strings.Contains(out, "updated "+old) || !strings.Contains(out, "Run `herdr-desk ticker stop`; herdr's next start runs the new ticker.") {
		t.Errorf("output does not say the herdr-desk was updated and the ticker needs a stop:\n%s", out)
	}
}

func TestFetchOrBuildLeavesADeskFromElsewhereAlone(t *testing.T) {
	r := newRig(t)
	r.release(t)
	other := filepath.Join(r.dir, "stubs", "herdr-desk")
	writeExec(t, other, "#!/bin/sh\necho other\n")

	out := r.run(t)

	if got := readFile(t, other); !strings.Contains(got, "echo other") {
		t.Errorf("the herdr-desk from elsewhere was changed to %q", got)
	}
	if _, err := os.Stat(filepath.Join(r.install, "herdr-desk")); err == nil {
		t.Error("a herdr-desk was installed although another install owns the one on PATH")
	}
	if !strings.Contains(out, other) {
		t.Errorf("output does not name the herdr-desk on PATH (%s):\n%s", other, out)
	}
}

func TestFetchOrBuildFailsWhenItCannotInstall(t *testing.T) {
	r := newRig(t)
	r.release(t)
	blocker := filepath.Join(r.dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(blocker, "bin")
	r.env = append(r.env, "DESK_INSTALL_DIR="+install)

	cmd := exec.Command("sh", "fetch-or-build.sh")
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("fetch-or-build.sh exited 0 although %s cannot be created:\n%s", install, out)
	}
	if !strings.Contains(string(out), install+"/herdr-desk") {
		t.Errorf("the failure does not name the install destination:\n%s", out)
	}
}

func TestFetchOrBuildPrebuiltSetsUpWithoutClaudeOrPlugin(t *testing.T) {
	r := newRig(t)
	r.release(t)
	r.addDesk(t)

	out := r.run(t)

	if got := r.commandLog(t); got != "herdr-desk setup\n" {
		t.Errorf("setup command log = %q, want only the no-profile setup command", got)
	}
	if !strings.Contains(out, "herdr-desk: no claude on PATH; setup uses no profile. To start another agent, set [agent] in the herdr-desk config named below (README: herdr with another agent).") {
		t.Errorf("output does not explain the no-claude setup profile:\n%s", out)
	}
	if !strings.Contains(out, "herdr-desk: no claude on PATH, so the Claude Code plugin was not installed. Inside Claude Code: /plugin marketplace add federbenjamin/herdr-desk, then /plugin install herdr-desk@herdr-desk") {
		t.Errorf("output does not explain how to install the plugin without claude:\n%s", out)
	}
}

func TestFetchOrBuildSourceSetsUpAndInstallsClaudePlugin(t *testing.T) {
	r := newRig(t)
	r.addDesk(t)
	claudePath := r.addClaude(t)

	out := r.run(t)

	if got, want := r.commandLog(t), "herdr-desk setup --profile claude-code\nclaude plugin marketplace add federbenjamin/herdr-desk\nclaude plugin install herdr-desk@herdr-desk\n"; got != want {
		t.Errorf("commands = %q, want %q", got, want)
	}
	if !strings.Contains(out, "herdr-desk: claude found at "+claudePath+"; setup uses the claude-code profile.") {
		t.Errorf("output does not name the claude binary and profile:\n%s", out)
	}
	if !strings.Contains(out, "herdr-desk: Claude Code plugin installed (marketplace federbenjamin/herdr-desk, plugin herdr-desk@herdr-desk).") {
		t.Errorf("output does not confirm the plugin install:\n%s", out)
	}
	for _, want := range []string{
		"setup output",
		"claude plugin marketplace add federbenjamin/herdr-desk output",
		"claude plugin install herdr-desk@herdr-desk output",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not pass through %q:\n%s", want, out)
		}
	}
}

func TestFetchOrBuildContinuesToClaudePluginAfterSetupFails(t *testing.T) {
	r := newRig(t)
	r.release(t)
	r.addDesk(t)
	r.addClaude(t)
	r.env = append(r.env, "DESK_EXIT=31")

	out := r.run(t)

	if got, want := r.commandLog(t), "herdr-desk setup --profile claude-code\nclaude plugin marketplace add federbenjamin/herdr-desk\nclaude plugin install herdr-desk@herdr-desk\n"; got != want {
		t.Errorf("commands = %q, want plugin installation after the failed setup", got)
	}
	if !strings.Contains(out, "herdr-desk: setup did not finish (exit 31; see above). After fixing it, run: herdr-desk setup --profile claude-code") {
		t.Errorf("output does not give the failed setup recovery command:\n%s", out)
	}
}

func TestFetchOrBuildStopsClaudePluginAfterMarketplaceFailure(t *testing.T) {
	r := newRig(t)
	r.release(t)
	r.addDesk(t)
	r.addClaude(t)
	r.env = append(r.env, "CLAUDE_MARKETPLACE_EXIT=41")

	out := r.run(t)

	if got, want := r.commandLog(t), "herdr-desk setup --profile claude-code\nclaude plugin marketplace add federbenjamin/herdr-desk\n"; got != want {
		t.Errorf("commands = %q, want the failed marketplace command to stop the plugin install", got)
	}
	if !strings.Contains(out, "herdr-desk: Claude Code plugin not installed (claude plugin marketplace add exited 41; see above). Inside Claude Code: /plugin marketplace add federbenjamin/herdr-desk, then /plugin install herdr-desk@herdr-desk") {
		t.Errorf("output does not explain the failed marketplace step:\n%s", out)
	}
}

func TestFetchOrBuildReportsPluginInstallFailureWithoutFailingTheBuild(t *testing.T) {
	r := newRig(t)
	r.release(t)
	r.addDesk(t)
	r.addClaude(t)
	r.env = append(r.env, "CLAUDE_INSTALL_EXIT=42")

	out := r.run(t)

	if got, want := r.commandLog(t), "herdr-desk setup --profile claude-code\nclaude plugin marketplace add federbenjamin/herdr-desk\nclaude plugin install herdr-desk@herdr-desk\n"; got != want {
		t.Errorf("commands = %q, want each Claude step once", got)
	}
	if !strings.Contains(out, "herdr-desk: Claude Code plugin not installed (claude plugin install exited 42; see above). Inside Claude Code: /plugin marketplace add federbenjamin/herdr-desk, then /plugin install herdr-desk@herdr-desk") {
		t.Errorf("output does not explain the failed plugin install step:\n%s", out)
	}
	if strings.Contains(out, "Claude Code plugin installed") {
		t.Errorf("output says the plugin was installed after its install step failed:\n%s", out)
	}
}

// installLog is where the script keeps its report for the rig's env: <state>/install.log, the state folder being the
// one herdr-desk itself resolves.
func (r *rig) installLog() string {
	env := map[string]string{}
	for _, kv := range r.env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return filepath.Join(config.ResolvePaths(func(k string) string { return env[k] }).StateDir, "install.log")
}

func TestFetchOrBuildKeepsItsReportInTheInstallLog(t *testing.T) {
	r := newRig(t)
	r.release(t)
	r.addDesk(t)
	r.addClaude(t)
	r.env = append(r.env, "DESK_EXIT=31", "CLAUDE_INSTALL_EXIT=42")

	out := r.run(t)

	log := readFile(t, r.installLog())
	inOrder := []string{
		"herdr-desk: claude found at ",
		"setup output",
		"herdr-desk: setup did not finish (exit 31; see above). After fixing it, run: herdr-desk setup --profile claude-code",
		"claude plugin install herdr-desk@herdr-desk output",
		"herdr-desk: Claude Code plugin not installed (claude plugin install exited 42; see above).",
	}
	at := 0
	for _, want := range inOrder {
		i := strings.Index(log[at:], want)
		if i < 0 {
			t.Fatalf("the install log lacks %q after byte %d:\n%s", want, at, log)
		}
		at += i + len(want)
	}
	if !strings.Contains(log, "setup stderr") {
		t.Errorf("the install log lacks setup's stderr:\n%s", log)
	}
	if !strings.Contains(out, log) {
		t.Errorf("the output does not hold the install log's report\noutput:\n%s\nlog:\n%s", out, log)
	}
	info, err := os.Stat(r.installLog())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("install log mode = %04o, want 0600", got)
	}
}

func TestFetchOrBuildInstallLogHoldsOnlyTheLatestInstall(t *testing.T) {
	r := newRig(t)
	r.release(t)
	r.addDesk(t)
	r.addClaude(t)
	state := filepath.Join(r.dir, "xdg-state")
	r.env = append(r.env, "XDG_STATE_HOME="+state, "CLAUDE_MARKETPLACE_EXIT=41")
	r.run(t)
	if log := filepath.Join(state, "herdr-desk", "install.log"); r.installLog() != log {
		t.Fatalf("the rig's install log is %s, want %s under XDG_STATE_HOME", r.installLog(), log)
	}
	if !strings.Contains(readFile(t, r.installLog()), "not installed") {
		t.Fatalf("the first install's log does not report the failed step:\n%s", readFile(t, r.installLog()))
	}

	r.env = append(r.env, "CLAUDE_MARKETPLACE_EXIT=0")
	r.run(t)

	log := readFile(t, r.installLog())
	if strings.Contains(log, "not installed") || !strings.Contains(log, "Claude Code plugin installed") {
		t.Errorf("the install log does not hold just the latest install:\n%s", log)
	}
}

func TestFetchOrBuildReportsWithoutAnInstallLogWhenItCannotWriteOne(t *testing.T) {
	r := newRig(t)
	r.release(t)
	r.addDesk(t)
	r.addClaude(t)
	blocker := filepath.Join(r.dir, "state-is-a-file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r.env = append(r.env, "XDG_STATE_HOME="+blocker, "CLAUDE_INSTALL_EXIT=42")

	out := r.run(t)

	for _, want := range []string{"setup output", "herdr-desk: Claude Code plugin not installed (claude plugin install exited 42; see above)."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q with no install log:\n%s", want, out)
		}
	}
}

func TestFetchOrBuildInstallsThePluginTheMarketplaceFileNames(t *testing.T) {
	var market struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name string `json:"name"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join("..", ".claude-plugin", "marketplace.json"))), &market); err != nil {
		t.Fatal(err)
	}
	if market.Name == "" || len(market.Plugins) != 1 || market.Plugins[0].Name == "" {
		t.Fatalf("marketplace.json names no single plugin: %+v", market)
	}
	id := market.Plugins[0].Name + "@" + market.Name

	r := newRig(t)
	r.release(t)
	r.addDesk(t)
	r.addClaude(t)
	out := r.run(t)
	if !strings.Contains(r.commandLog(t), "claude plugin install "+id+"\n") {
		t.Errorf("the script did not install %s, the plugin marketplace.json names:\n%s", id, r.commandLog(t))
	}
	if !strings.Contains(out, "plugin "+id+").") {
		t.Errorf("the success line does not name %s:\n%s", id, out)
	}

	r = newRig(t)
	r.release(t)
	r.addDesk(t)
	out = r.run(t)
	if !strings.Contains(out, "/plugin install "+id) {
		t.Errorf("the slash-command pointer does not name %s:\n%s", id, out)
	}
}
