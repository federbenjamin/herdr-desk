// Package gitcmd runs git the one way desk allows: no prompt, a timeout, and a repository chosen by the
// directory argument alone, never by the caller's environment.
package gitcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

// repoEnv are the variables that point git at another repository than the one -C names: the list
// `git rev-parse --local-env-vars` prints. A git hook exports some of them.
var repoEnv = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	"GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE",
	"GIT_INDEX_FILE", "GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX", "GIT_SHALLOW_FILE",
	"GIT_COMMON_DIR",
}

// Error is a git command that failed. Its text names the subcommand, how git ended, and git's stderr with
// every URL credential removed.
type Error struct {
	Sub    string // the git subcommand
	Err    error  // the exec error: an *exec.ExitError, a timeout, or git not found
	Stderr string // git's stderr, trimmed, URL credentials removed
}

func (e *Error) Error() string {
	s := "git " + e.Sub + ": " + e.Err.Error()
	if e.Stderr != "" {
		s += ": " + e.Stderr
	}
	return s
}

func (e *Error) Unwrap() error { return e.Err }

// IsNotRepo reports whether err is git saying the directory is in no repository.
func IsNotRepo(err error) bool {
	var ge *Error
	return errors.As(err, &ge) && strings.Contains(ge.Stderr, "not a git repository")
}

// Run runs `git -C dir args…` within timeout and returns its trimmed stdout. args may start with -c pairs
// before the subcommand.
func Run(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env(os.Environ())
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", &Error{Sub: subcommand(args), Err: err, Stderr: redact(strings.TrimSpace(errOut.String()))}
	}
	return strings.TrimSpace(out.String()), nil
}

// env is environ without the repository variables, with prompts off and git's messages in English, so
// IsNotRepo can read them.
func env(environ []string) []string {
	out := make([]string, 0, len(environ)+2)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(repoEnv, name) {
			out = append(out, kv)
		}
	}
	return append(out, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
}

func subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		return args[i]
	}
	return ""
}

var userinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@]+@`)

// redact removes the user and password from every URL in s.
func redact(s string) string {
	return userinfo.ReplaceAllString(s, "${1}")
}
