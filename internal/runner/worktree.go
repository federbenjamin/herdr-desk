package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/federbenjamin/herdr-desk/internal/gitcmd"
)

// worktreesRel is the folder inside a root that holds its task worktrees. Nested, a worktree stays under its
// main checkout, which a root's dev tooling can require (a setup script that derives ports from the nesting
// refuses one beside it); the name is the one Claude Code uses, so roots that already ignore worktrees ignore this folder.
const worktreesRel = ".claude/worktrees"

// worktreeDir is task's worktree for worktree isolation in root: <root>/.claude/worktrees/T<task>.
func worktreeDir(root string, task int) string {
	return filepath.Join(filepath.Clean(root), filepath.FromSlash(worktreesRel), "T"+strconv.Itoa(task))
}

// legacyWorktreeDir is where runs started before worktrees nested kept theirs: <parent of root>/<base of root>-T<task>.
func legacyWorktreeDir(root string, task int) string {
	root = filepath.Clean(root)
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+"-T"+strconv.Itoa(task))
}

// taskWorktree returns the work tree task already has in root, at its current path or its legacy one; ok is false
// when it has none. A tree that could not be checked is the error.
func taskWorktree(ctx context.Context, root string, task int) (dir string, ok bool, err error) {
	for _, dir := range []string{worktreeDir(root, task), legacyWorktreeDir(root, task)} {
		ok, err := isWorkTree(ctx, dir)
		if err != nil {
			return dir, false, err
		}
		if ok {
			return dir, true, nil
		}
	}
	return "", false, nil
}

// hideWorktrees makes git ignore root's worktrees folder when no ignore rule does, through the repository's own
// info/exclude, so a root that does not ignore it shows no untracked folder and no tracked file changes.
func hideWorktrees(ctx context.Context, root string) error {
	_, err := gitcmd.Run(ctx, root, gitTimeout, "check-ignore", "-q", "--", worktreesRel+"/T0/")
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return err
	}
	path, err := gitcmd.Run(ctx, root, gitTimeout, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	prior, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	line := "/" + worktreesRel + "/\n"
	if len(prior) > 0 && !strings.HasSuffix(string(prior), "\n") {
		line = "\n" + line
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
