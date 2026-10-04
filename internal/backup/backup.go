// Package backup exports the event log to a git repo and pushes it to the configured remote.
package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/store"
)

// Result is what one backup run did.
type Result struct {
	Events    int  `json:"events"`
	Committed bool `json:"committed"`
	Pushed    bool `json:"pushed"`
}

// state is the backup state file.
type state struct {
	LastRun time.Time `json:"last_run"`
}

const (
	exportFile = "events.jsonl"
	gitTimeout = 2 * time.Minute
)

// Run exports every event to <BackupDir>/events.jsonl, commits when the file changed, pushes to the remote's
// main branch, and records the time of the run in the backup state file.
func Run(ctx context.Context, st *store.Store, p config.Paths, remote string) (Result, error) {
	var res Result
	dir := p.BackupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if _, err := git(ctx, dir, "init", "--quiet"); err != nil {
			return res, err
		}
		if _, err := git(ctx, dir, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
			return res, err
		}
	}
	n, err := export(ctx, st, filepath.Join(dir, exportFile))
	if err != nil {
		return res, err
	}
	res.Events = n
	changed, err := git(ctx, dir, "status", "--porcelain", "--", exportFile)
	if err != nil {
		return res, err
	}
	if changed != "" {
		if _, err := git(ctx, dir, "add", "--", exportFile); err != nil {
			return res, err
		}
		if _, err := git(ctx, dir, "commit", "--quiet", "-m", fmt.Sprintf("desk backup: %d events", n)); err != nil {
			return res, err
		}
		res.Committed = true
	}
	if _, err := git(ctx, dir, "push", "--quiet", remote, "HEAD:refs/heads/main"); err != nil {
		return res, err
	}
	res.Pushed = true
	return res, writeState(p, state{LastRun: time.Now().UTC()})
}

// Due reports whether no run is recorded or the last recorded run is over 24 hours before now.
func Due(p config.Paths, now time.Time) bool {
	b, err := os.ReadFile(p.BackupState())
	if err != nil {
		return true
	}
	var s state
	if json.Unmarshal(b, &s) != nil || s.LastRun.IsZero() {
		return true
	}
	return now.Sub(s.LastRun) > 24*time.Hour
}

func export(ctx context.Context, st *store.Store, path string) (int, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+exportFile+"-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(f.Name())
	n, err := st.ExportEvents(ctx, f)
	if err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	return n, os.Rename(f.Name(), path)
}

func writeState(p config.Paths, s state) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	path := p.BackupState()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// git runs one git command in dir with no prompt and a timeout, and returns its trimmed stdout. A daemon has no
// terminal, so a signing prompt would hang it, and the machine may have no git identity. The error names the
// subcommand only: the remote's URL may carry a credential.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	global := []string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "user.name=desk", "-c", "user.email=desk@localhost"}
	cmd := exec.CommandContext(ctx, "git", append(global, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
