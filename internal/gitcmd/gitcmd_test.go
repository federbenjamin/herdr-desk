package gitcmd_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/gitcmd"
)

func TestRunUsesTheDirectoryNotTheRepositoryTheEnvironmentNames(t *testing.T) {
	repo, other := t.TempDir(), t.TempDir()
	for _, dir := range []string{repo, other} {
		if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init %s: %v\n%s", dir, err, out)
		}
	}
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_COMMON_DIR", filepath.Join(other, ".git"))

	got, err := gitcmd.Run(context.Background(), repo, 10*time.Second, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(repo, ".git"))
	if resolved, _ := filepath.EvalSymlinks(got); resolved != want {
		t.Fatalf("Run() = %q, want the repository of the directory, %q", got, want)
	}
}

func TestRunErrorsTellNotARepositoryFromOtherFailures(t *testing.T) {
	plain := t.TempDir()
	_, err := gitcmd.Run(context.Background(), plain, 10*time.Second, "rev-parse", "--git-common-dir")
	if !gitcmd.IsNotRepo(err) {
		t.Fatalf("rev-parse in a plain dir: IsNotRepo(%v) = false, want true", err)
	}

	t.Setenv("PATH", t.TempDir())
	_, err = gitcmd.Run(context.Background(), plain, 10*time.Second, "rev-parse", "--git-common-dir")
	if err == nil || gitcmd.IsNotRepo(err) {
		t.Fatalf("git missing: err = %v, want an error that is not not-a-repo", err)
	}
	var execErr *exec.Error
	if !errors.As(err, &execErr) {
		t.Fatalf("git missing: err = %v, want it to wrap the exec error", err)
	}
}

func TestRunErrorNamesTheSubcommandAndHidesURLCredentials(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	url := "https://desk-user:s3cret-token@example.invalid/repo.git"
	// check-ref-format echoes the name it refuses, as a failing push or fetch can echo its remote.
	_, err := gitcmd.Run(context.Background(), repo, 10*time.Second, "-c", "user.name=x", "check-ref-format", "--branch", url)
	if err == nil {
		t.Fatal("check-ref-format of a URL succeeded")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "git check-ref-format: ") || !strings.Contains(msg, "example.invalid/repo.git") {
		t.Fatalf("error = %q, want it to name the subcommand after the -c pairs", msg)
	}
	if strings.Contains(msg, "s3cret-token") || strings.Contains(msg, "desk-user") {
		t.Fatalf("error = %q, want the URL's user and password removed", msg)
	}
}
