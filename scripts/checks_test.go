package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// toolStub logs its name and arguments; gofmt prints $GOFMT_OUT, as gofmt -l prints the files it would change.
const toolStub = `#!/bin/sh
echo "$(basename "$0") $*" >>"$CHECKS_LOG"
if [ "$(basename "$0")" = gofmt ]; then printf '%s' "${GOFMT_OUT:-}"; fi
`

// runChecks runs a copy of checks.sh beside a coverage.sh stub, with go, gofmt, and shellcheck stubbed, and
// returns its exit code, the tools it ran in order, and its stderr.
func runChecks(t *testing.T, gofmtOut string, args ...string) (int, []string, string) {
	t.Helper()
	dir := t.TempDir()
	scripts := filepath.Join(dir, "scripts")
	stubs := filepath.Join(dir, "stubs")
	for _, d := range []string{scripts, stubs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	src, err := os.ReadFile("checks.sh")
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "checks.log")
	files := map[string]string{
		filepath.Join(scripts, "checks.sh"):   string(src),
		filepath.Join(scripts, "coverage.sh"): "echo \"coverage.sh $*\" >>\"$CHECKS_LOG\"\n",
	}
	for _, tool := range []string{"go", "gofmt", "shellcheck"} {
		files[filepath.Join(stubs, tool)] = toolStub
	}
	for path, text := range files {
		if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", append([]string{filepath.Join(scripts, "checks.sh")}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CHECKS_LOG="+log, "GOFMT_OUT="+gofmtOut)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run checks.sh: %v", err)
		}
		code = exit.ExitCode()
	}
	b, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var ran []string
	for line := range strings.Lines(string(b)) {
		f := strings.Fields(line)
		if f[0] == "go" {
			f[0] += " " + f[1]
		}
		ran = append(ran, f[0])
	}
	return code, ran, stderr.String()
}

func TestChecksRunsEveryCheckInOrderAndSwapsTheCoverageGateForGoTest(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, "go vet,coverage.sh,gofmt,shellcheck"},
		{"no coverage", []string{"--no-coverage"}, "go vet,go test,gofmt,shellcheck"},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, ran, stderr := runChecks(t, "", test.args...)
			if code != 0 || strings.Join(ran, ",") != test.want {
				t.Fatalf("checks.sh %v = exit %d, ran %q (%s), want exit 0 and %q", test.args, code, ran, stderr, test.want)
			}
		})
	}
}

func TestChecksFailsOnUnformattedFilesAndRefusesAnUnknownFlag(t *testing.T) {
	code, ran, stderr := runChecks(t, "internal/x.go\n")
	if code != 1 || strings.Join(ran, ",") != "go vet,coverage.sh,gofmt" || !strings.Contains(stderr, "internal/x.go") {
		t.Fatalf("checks.sh with an unformatted file = exit %d, ran %q, stderr %q; want exit 1 naming the file before shellcheck", code, ran, stderr)
	}
	for _, args := range [][]string{{"--bogus"}, {""}, {"--no-coverage", "--bogus"}, {"--no-coverage", "--no-coverage"}} {
		code, ran, _ = runChecks(t, "", args...)
		if code != 2 || len(ran) != 0 {
			t.Errorf("checks.sh %q = exit %d, ran %q; want exit 2 and nothing run", args, code, ran)
		}
	}
}
