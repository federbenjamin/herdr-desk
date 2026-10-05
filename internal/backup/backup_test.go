package backup_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/backup"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestRunExportsEventsCommitsOnlyChangesPushesMainAndRecordsItsRun(t *testing.T) {
	machine := testutil.NewMachine(t)
	paths := machine.Paths
	remote := filepath.Join(paths.DataDir, "remote.git")
	git(t, "init", "--bare", remote)
	t.Setenv("GIT_AUTHOR_NAME", "desk test")
	t.Setenv("GIT_AUTHOR_EMAIL", "desk-test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "desk test")
	t.Setenv("GIT_COMMITTER_EMAIL", "desk-test@example.invalid")

	st, err := store.Open(paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{
		TaskData: model.TaskData{Title: "back up this task"},
	}); err != nil {
		t.Fatalf("add task: %v", err)
	}

	first, err := backup.Run(context.Background(), st, paths, remote)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if first.Events != 1 {
		t.Fatalf("first Run() events = %d, want 1", first.Events)
	}
	if !first.Committed {
		t.Fatal("first Run() did not report a commit")
	}
	if !first.Pushed {
		t.Fatal("first Run() did not report a push")
	}

	exported, err := os.ReadFile(filepath.Join(paths.BackupDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}
	var event model.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(exported))), &event); err != nil {
		t.Fatalf("decode events.jsonl: %v", err)
	}
	if event.Kind != model.KindTask || event.Task != 1 {
		t.Fatalf("exported event = kind %q task %d, want task event for T1", event.Kind, event.Task)
	}
	git(t, "--git-dir", remote, "show-ref", "--verify", "refs/heads/main")
	pushed, err := exec.Command("git", "--git-dir", remote, "show", "main:events.jsonl").Output()
	if err != nil {
		t.Fatalf("read events.jsonl from remote main: %v", err)
	}
	if string(pushed) != string(exported) {
		t.Fatal("remote main events.jsonl differs from the exported file")
	}

	headBefore := gitOutput(t, "-C", paths.BackupDir(), "rev-parse", "HEAD")
	second, err := backup.Run(context.Background(), st, paths, remote)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if second.Committed {
		t.Fatal("unchanged second Run() reported a commit")
	}
	if headAfter := gitOutput(t, "-C", paths.BackupDir(), "rev-parse", "HEAD"); headAfter != headBefore {
		t.Fatalf("unchanged second Run() changed HEAD from %s to %s", headBefore, headAfter)
	}
	if backup.Due(paths, time.Now().Add(23*time.Hour)) {
		t.Fatal("backup is due less than 24 hours after Run()")
	}
	if !backup.Due(paths, time.Now().Add(25*time.Hour)) {
		t.Fatal("backup is not due more than 24 hours after Run()")
	}
}

func TestDueIsTrueWhenNoRunHasBeenRecorded(t *testing.T) {
	machine := testutil.NewMachine(t)
	if !backup.Due(machine.Paths, time.Now()) {
		t.Fatal("Due() = false without a recorded backup")
	}
}

func TestDueTreatsMalformedAndEmptyStateAsNoRecordedRun(t *testing.T) {
	for _, contents := range []string{"not json", "{}"} {
		t.Run(contents, func(t *testing.T) {
			machine := testutil.NewMachine(t)
			if err := os.MkdirAll(machine.Paths.StateDir, 0o700); err != nil {
				t.Fatalf("make state directory: %v", err)
			}
			if err := os.WriteFile(machine.Paths.BackupState(), []byte(contents), 0o600); err != nil {
				t.Fatalf("write backup state: %v", err)
			}
			if !backup.Due(machine.Paths, time.Now()) {
				t.Fatalf("Due() = false with state %q", contents)
			}
		})
	}
}

func TestRunExportsNewEventsInANewCommit(t *testing.T) {
	machine := testutil.NewMachine(t)
	paths := machine.Paths
	remote := filepath.Join(paths.DataDir, "remote.git")
	git(t, "init", "--bare", remote)
	t.Setenv("GIT_AUTHOR_NAME", "desk test")
	t.Setenv("GIT_AUTHOR_EMAIL", "desk-test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "desk test")
	t.Setenv("GIT_COMMITTER_EMAIL", "desk-test@example.invalid")

	st, err := store.Open(paths.DB(), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "first"}}); err != nil {
		t.Fatalf("add first task: %v", err)
	}
	if _, err := backup.Run(context.Background(), st, paths, remote); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	headBefore := gitOutput(t, "-C", paths.BackupDir(), "rev-parse", "HEAD")
	if _, err := st.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "second"}}); err != nil {
		t.Fatalf("add second task: %v", err)
	}

	result, err := backup.Run(context.Background(), st, paths, remote)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if result.Events != 2 || !result.Committed || !result.Pushed {
		t.Fatalf("second Run() = %+v, want two events, committed, and pushed", result)
	}
	if headAfter := gitOutput(t, "-C", paths.BackupDir(), "rev-parse", "HEAD"); headAfter == headBefore {
		t.Fatal("Run() did not create a new commit after the event export changed")
	}
}

func git(t *testing.T, args ...string) {
	t.Helper()
	if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitOutput(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
