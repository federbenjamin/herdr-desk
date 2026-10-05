package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/cli"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/daemon"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestACommandOnAnOverlongSocketPathPrintsTheReasonNotAPointerToTheLog(t *testing.T) {
	m := testutil.NewMachine(t)
	state := filepath.Join(filepath.Dir(filepath.Dir(m.Paths.ConfigDir)), strings.Repeat("s", 100))
	getenv := m.Getenv(map[string]string{})
	long := func(k string) string {
		if k == "XDG_STATE_HOME" {
			return state
		}
		return getenv(k)
	}
	socket := config.ResolvePaths(long).Socket()

	var stdout, stderr bytes.Buffer
	exit := cli.Run(context.Background(), []string{"list"}, cli.Env{Stdout: &stdout, Stderr: &stderr, Getenv: long, Cwd: t.TempDir(), Spawn: daemon.Spawn})
	if exit != 3 {
		t.Fatalf("list exit = %d, want 3; stderr = %q", exit, stderr.String())
	}
	for _, want := range []string{socket, strconv.Itoa(len(socket)) + " bytes", "103"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to hold %q", stderr.String(), want)
		}
	}
	if strings.Contains(stderr.String(), "daemon.log") {
		t.Errorf("stderr = %q, want the reason itself, not a pointer to the log", stderr.String())
	}
}
