// Package secretscan finds secrets in text before desk stores it.
package secretscan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Scanner reports the name of the pattern text matches, "" when it is clean, and an error when the scan
// could not run. It never returns the matched text.
type Scanner func(ctx context.Context, text string) (pattern string, err error)

type pattern struct {
	name string
	re   *regexp.Regexp
}

var builtin = []pattern{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"aws-access-key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)},
	{"github-token", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}\b`)},
	{"anthropic-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
}

// Builtin scans with the built-in patterns: private-key, aws-access-key, github-token, anthropic-key,
// slack-token.
func Builtin() Scanner {
	return func(_ context.Context, text string) (string, error) {
		for _, p := range builtin {
			if p.re.MatchString(text) {
				return p.name, nil
			}
		}
		return "", nil
	}
}

// Command scans by running argv with the text on stdin: exit 0 is clean, 1 a hit (pattern "command"),
// anything else, or a command that cannot start, an error.
func Command(argv []string) Scanner {
	argv = append([]string(nil), argv...)
	return func(ctx context.Context, text string) (string, error) {
		if len(argv) == 0 {
			return "", errors.New("secret scan command is empty")
		}
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(text)
		// The scanner's output may quote the secret, so it is never kept.
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		cmd.WaitDelay = time.Second
		err := cmd.Run()
		if err == nil {
			return "", nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "command", nil
		}
		return "", fmt.Errorf("secret scan command %q failed: %w", argv[0], err)
	}
}

// FromConfig returns Builtin() for an empty argv, else Command(argv).
func FromConfig(argv []string) Scanner {
	if len(argv) == 0 {
		return Builtin()
	}
	return Command(argv)
}
