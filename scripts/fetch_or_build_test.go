package scripts_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fetch-or-build.sh failed: %v\n%s", err, out)
	}
	return string(out)
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
	if !strings.Contains(out, "updated "+old) || !strings.Contains(out, "herdr-desk daemon restart") {
		t.Errorf("output does not say the herdr-desk was updated and the daemon needs a restart:\n%s", out)
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
