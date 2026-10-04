package cli_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/testutil"
)

func TestOfflineLiveListsUseTheSnapshotAndMarkJSONOffline(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	clientMachine := testutil.NewClientMachine(t, home)
	getenv := clientMachine.Getenv(nil)
	cwd := t.TempDir()

	for _, args := range [][]string{
		{"add", "-t", "open task"},
		{"add", "-t", "ready task", "--status", "ready"},
	} {
		result := runDesk(t, getenv, cwd, "", args...)
		requireSuccess(t, result)
	}
	result := runDesk(t, getenv, cwd, "", "list", "--ready")
	requireSuccess(t, result)
	if !strings.Contains(result.stdout, "ready task") {
		t.Fatalf("online ready list = %q, want ready task", result.stdout)
	}

	home.Stop()
	result = runDesk(t, getenv, cwd, "", "list", "--open", "--json")
	requireSuccess(t, result)
	if !strings.Contains(result.stderr, "showing the snapshot") {
		t.Errorf("offline list stderr = %q, want a snapshot notice", result.stderr)
	}
	var list api.TaskList
	if err := json.Unmarshal([]byte(result.stdout), &list); err != nil {
		t.Fatalf("decode offline list JSON: %v; output=%q", err, result.stdout)
	}
	if !list.Offline || list.SnapshotTS == nil || len(list.Tasks) != 1 || list.Tasks[0].Title != "open task" {
		t.Errorf("offline open list = %#v, want the snapshot's one open task marked offline", list)
	}
}

func TestOfflineBareDeskUsesSnapshotBannerAndLiveBoard(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	clientMachine := testutil.NewClientMachine(t, home)
	getenv := clientMachine.Getenv(nil)
	cwd := t.TempDir()

	requireSuccess(t, runDesk(t, getenv, cwd, "", "add", "-t", "needs review", "--status", "review"))
	requireSuccess(t, runDesk(t, getenv, cwd, "", "list"))
	home.Stop()

	for _, test := range []struct {
		name string
		age  time.Duration
		want string
	}{
		{"seconds", 0, "0s old"},
		{"minutes", 5 * time.Minute, "5m old"},
		{"hours", 3 * time.Hour, "3h old"},
		{"days", 48 * time.Hour, "2d old"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setSnapshotAge(t, clientMachine.Paths.Snapshot(), test.age)
			result := runDesk(t, getenv, cwd, "")
			requireSuccess(t, result)
			if !strings.HasPrefix(result.stdout, "desk · offline (snapshot "+test.want+")") {
				t.Errorf("offline board first line = %q, want snapshot age %q", firstLine(result.stdout), test.want)
			}
			if !strings.Contains(result.stdout, "NEEDS YOU") || !strings.Contains(result.stdout, "needs review") {
				t.Errorf("offline board = %q, want the snapshotted live board", result.stdout)
			}
			if !strings.Contains(result.stderr, "showing the snapshot") {
				t.Errorf("offline board stderr = %q, want a snapshot notice", result.stderr)
			}
		})
	}
}

func setSnapshotAge(t *testing.T, path string, age time.Duration) {
	t.Helper()
	setSnapshotTime(t, path, time.Now().Add(-age))
}

func setSnapshotTime(t *testing.T, path string, ts time.Time) {
	t.Helper()
	var snap struct {
		TS    time.Time    `json:"ts"`
		Tasks []model.Task `json:"tasks"`
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if err := json.Unmarshal(contents, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	snap.TS = ts
	contents, err = json.Marshal(snap)
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
}

func TestOfflineRefusesNonLiveReadsAndTaskWrites(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	clientMachine := testutil.NewClientMachine(t, home)
	getenv := clientMachine.Getenv(nil)
	cwd := t.TempDir()

	requireSuccess(t, runDesk(t, getenv, cwd, "", "add", "-t", "saved task"))
	requireSuccess(t, runDesk(t, getenv, cwd, "", "list"))
	home.Stop()

	for _, test := range []struct {
		name    string
		args    []string
		command string
	}{
		{"all list", []string{"list", "--all"}, "list"},
		{"show", []string{"show", "T1"}, "show"},
		{"add", []string{"add", "-t", "must not queue"}, "add"},
		{"set", []string{"set", "T1", "ready"}, "set"},
		{"steps", []string{"steps", "T1", "add", "step"}, "steps"},
		{"edit", []string{"edit", "T1", "--title", "changed"}, "edit"},
		{"capture", []string{"capture"}, "capture"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := runDesk(t, getenv, cwd, "captured while offline\n", test.args...)
			requireRefusal(t, result, test.command, model.CodeHomeUnreachable, 3)
		})
	}
}

func firstLine(text string) string {
	if at := strings.IndexByte(text, '\n'); at >= 0 {
		return text[:at]
	}
	return text
}

func TestOfflineNoticeNamesTheSnapshotTimeInUTCWithAZ(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	clientMachine := testutil.NewClientMachine(t, home)
	getenv := clientMachine.Getenv(nil)
	cwd := t.TempDir()
	requireSuccess(t, runDesk(t, getenv, cwd, "", "add", "-t", "saved task"))
	requireSuccess(t, runDesk(t, getenv, cwd, "", "list"))
	home.Stop()

	// The stamp is written in another zone; the line must still read as UTC, like every other time desk prints.
	setSnapshotTime(t, clientMachine.Paths.Snapshot(), time.Date(2026, 10, 4, 21, 29, 54, 0, time.FixedZone("west", -7*3600)))
	result := runDesk(t, getenv, cwd, "", "list")
	requireSuccess(t, result)
	if want := "showing the snapshot from 2026-10-05 04:29Z\n"; !strings.HasSuffix(result.stderr, want) {
		t.Errorf("offline notice = %q, want it to end with %q", result.stderr, want)
	}
}
