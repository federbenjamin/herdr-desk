package herdr_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/herdr"
)

func TestCreateWorkspaceReturnsIDs(t *testing.T) {
	client := newFakeClient(t)
	cwd := t.TempDir()
	created, err := client.CreateWorkspace(context.Background(), cwd, "test workspace", []string{"FROM_WORKSPACE=green"})
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	if created.Workspace == "" || created.Pane == "" {
		t.Fatalf("CreateWorkspace() = %#v, want workspace and pane IDs", created)
	}
}

func TestRunStartsTheCommandInTheWorkspaceCwdAndEnvironment(t *testing.T) {
	client := newFakeClient(t)
	cwd := t.TempDir()
	created, err := client.CreateWorkspace(context.Background(), cwd, "test workspace", []string{"FROM_WORKSPACE=green"})
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	t.Cleanup(func() { _ = client.ClosePane(context.Background(), created.Pane) })
	output := filepath.Join(cwd, "ran.txt")
	command := fmt.Sprintf("printf '%%s:%%s:%%s' \"$PWD\" \"$FROM_WORKSPACE\" \"$HERDR_PANE_ID\" > %q", output)
	if err := client.Run(context.Background(), created.Pane, command); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	pollUntil(t, 2*time.Second, func() bool {
		_, err := os.Stat(output)
		return err == nil
	})
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read command output: %v", err)
	}
	want := cwd + ":green:" + created.Pane
	if string(got) != want {
		t.Errorf("command environment = %q, want %q", got, want)
	}
}

func TestPanesReportAgentStateAndRemoveExitedCommands(t *testing.T) {
	client := newFakeClient(t)
	created := createWorkspace(t, client)

	panes, err := client.Panes(context.Background())
	if err != nil {
		t.Fatalf("Panes() error = %v", err)
	}
	pane := findPane(panes, created.Pane)
	if pane.Status != "unknown" || pane.Session != "" {
		t.Fatalf("new pane = %#v, want status unknown and no session", pane)
	}

	runFake(t, "pane", "report-agent", created.Pane, "--source", "test", "--agent", "test", "--state", "idle", "--agent-session-id", "session-123")
	pollUntil(t, 2*time.Second, func() bool {
		panes, err := client.Panes(context.Background())
		if err != nil {
			return false
		}
		pane := findPane(panes, created.Pane)
		return pane.Status == "idle" && pane.Session == "session-123"
	})

	exited := createWorkspace(t, client)
	if err := client.Run(context.Background(), exited.Pane, "exit 0"); err != nil {
		t.Fatalf("Run(exited pane) error = %v", err)
	}
	pollUntil(t, 2*time.Second, func() bool {
		panes, err := client.Panes(context.Background())
		return err == nil && !hasPane(panes, exited.Pane)
	})
}

func TestProcessesReportTheRunningProcessGroup(t *testing.T) {
	client := newFakeClient(t)
	created := createWorkspace(t, client)
	t.Cleanup(func() { _ = client.ClosePane(context.Background(), created.Pane) })
	if err := client.Run(context.Background(), created.Pane, "sleep 30"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var got herdr.Processes
	pollUntil(t, 2*time.Second, func() bool {
		var err error
		got, err = client.Processes(context.Background(), created.Pane)
		return err == nil && got.Group > 0 && len(got.PIDs) > 0
	})
	if got.Group <= 0 || len(got.PIDs) == 0 {
		t.Fatalf("Processes() = %#v, want a group and live PIDs", got)
	}
}

func TestClosePaneKillsItsProcessesAndRemovesThePane(t *testing.T) {
	client := newFakeClient(t)
	created := createWorkspace(t, client)
	if err := client.Run(context.Background(), created.Pane, "sleep 30"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var processes herdr.Processes
	pollUntil(t, 2*time.Second, func() bool {
		var err error
		processes, err = client.Processes(context.Background(), created.Pane)
		return err == nil && len(processes.PIDs) > 0
	})
	if err := client.ClosePane(context.Background(), created.Pane); err != nil {
		t.Fatalf("ClosePane() error = %v", err)
	}
	pollUntil(t, 2*time.Second, func() bool {
		panes, err := client.Panes(context.Background())
		return err == nil && !hasPane(panes, created.Pane)
	})
	for _, pid := range processes.PIDs {
		pid := pid
		pollUntil(t, 2*time.Second, func() bool { return !processAlive(pid) })
	}
}

func TestClientErrorsNameTheFailedSubcommand(t *testing.T) {
	t.Run("fake failure", func(t *testing.T) {
		client := newFakeClient(t)
		t.Setenv("FAKE_HERDR_FAIL", "workspace->create")
		_, err := client.CreateWorkspace(context.Background(), t.TempDir(), "test", nil)
		if err == nil || !strings.Contains(err.Error(), "workspace create") {
			t.Fatalf("CreateWorkspace() error = %v, want workspace create", err)
		}
	})

	t.Run("missing binary", func(t *testing.T) {
		client := herdr.Client{Bin: filepath.Join(t.TempDir(), "missing-herdr")}
		_, err := client.Panes(context.Background())
		if err == nil || !strings.Contains(err.Error(), "pane list") {
			t.Fatalf("Panes() error = %v, want pane list", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "slow-herdr")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 1\n"), 0o700); err != nil {
			t.Fatalf("write slow herdr: %v", err)
		}
		client := herdr.Client{Bin: bin, Timeout: 10 * time.Millisecond}
		_, err := client.Panes(context.Background())
		if err == nil || !strings.Contains(err.Error(), "pane list") {
			t.Fatalf("Panes() error = %v, want pane list", err)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		bin := writeHerdrStub(t, "printf 'not JSON'\n")
		client := herdr.Client{Bin: bin}
		if _, err := client.Panes(context.Background()); err == nil || !strings.Contains(err.Error(), "pane list") {
			t.Fatalf("Panes() error = %v, want pane list", err)
		}
	})

	t.Run("stderr is capped", func(t *testing.T) {
		bin := writeHerdrStub(t, "head -c 500 /dev/zero | tr '\\0' x >&2\nexit 1")
		client := herdr.Client{Bin: bin}
		_, err := client.Panes(context.Background())
		if err == nil || !strings.Contains(err.Error(), "pane list") {
			t.Fatalf("Panes() error = %v, want pane list", err)
		}
		if got := strings.Count(err.Error(), "x"); got > 300 {
			t.Fatalf("Panes() error holds %d stderr bytes, want at most 300: %q", got, err)
		}
	})
}

func writeHerdrStub(t *testing.T, body string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "herdr-stub")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write herdr stub: %v", err)
	}
	return bin
}

func newFakeClient(t *testing.T) herdr.Client {
	t.Helper()
	bin, err := filepath.Abs(filepath.Join("..", "..", "scripts", "e2e", "fake-herdr.py"))
	if err != nil {
		t.Fatalf("make fake-herdr path absolute: %v", err)
	}
	t.Setenv("FAKE_HERDR_DIR", t.TempDir())
	return herdr.Client{Bin: bin, Timeout: 2 * time.Second}
}

func createWorkspace(t *testing.T, client herdr.Client) herdr.Created {
	t.Helper()
	created, err := client.CreateWorkspace(context.Background(), t.TempDir(), "test workspace", nil)
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	return created
}

func runFake(t *testing.T, args ...string) {
	t.Helper()
	client := newFakeClientPath(t)
	command := exec.Command(client, args...)
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fake-herdr %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func newFakeClientPath(t *testing.T) string {
	t.Helper()
	bin, err := filepath.Abs(filepath.Join("..", "..", "scripts", "e2e", "fake-herdr.py"))
	if err != nil {
		t.Fatalf("make fake-herdr path absolute: %v", err)
	}
	return bin
}

func findPane(panes []herdr.Pane, id string) herdr.Pane {
	for _, pane := range panes {
		if pane.ID == id {
			return pane
		}
	}
	return herdr.Pane{}
}

func hasPane(panes []herdr.Pane, id string) bool {
	return findPane(panes, id).ID != ""
}

func pollUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	if condition() {
		return
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("condition did not become true before the deadline")
		case <-poll.C:
			if condition() {
				return
			}
		}
	}
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
