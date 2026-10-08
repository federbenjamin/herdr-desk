package ticker_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
	"github.com/federbenjamin/herdr-desk/internal/ticker"
)

const tickerHelper = "HERDR_DESK_TICKER_HELPER"

func TestStartKeepsOneTickerAndRemovesItsStatusOnClose(t *testing.T) {
	machine := testutil.NewMachine(t)
	if err := config.Default().Save(machine.Paths.ConfigFile()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	started := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.FixedZone("test", -4*60*60))
	tk, err := ticker.Start(context.Background(), ticker.Options{
		Paths: machine.Paths,
		Every: time.Hour,
		Now:   func() time.Time { return started },
		Logf:  func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	info, running := ticker.Running(machine.Paths)
	if !running {
		t.Fatal("Running() = false while Start() holds the ticker lock")
	}
	if info.PID != os.Getpid() {
		t.Fatalf("Running() pid = %d, want %d", info.PID, os.Getpid())
	}
	if !info.StartedTS.Equal(started.UTC()) {
		t.Fatalf("Running() started_at = %s, want %s", info.StartedTS, started.UTC())
	}

	second, err := ticker.Start(context.Background(), ticker.Options{Paths: machine.Paths, Logf: func(string, ...any) {}})
	if second != nil || !errors.Is(err, ticker.ErrRunning) {
		t.Fatalf("second Start() = (%v, %v), want (nil, ErrRunning)", second, err)
	}
	if err := tk.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, running := ticker.Running(machine.Paths); running {
		t.Fatal("Running() = true after Close() released the ticker lock")
	}
	if _, err := os.Stat(machine.Paths.TickerInfo()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ticker info after Close() stat error = %v, want not exist", err)
	}
}

func TestTickReadsFreshConfigAndSkipsJobsOnAClient(t *testing.T) {
	machine := testutil.NewMachine(t)
	cfg := config.Default()
	if err := cfg.Save(machine.Paths.ConfigFile()); err != nil {
		t.Fatalf("save home config: %v", err)
	}
	jobs := make(chan struct{}, 2)
	tk, err := ticker.Start(context.Background(), ticker.Options{
		Paths: machine.Paths,
		Every: time.Hour,
		Logf:  func(string, ...any) {},
		Jobs: func(context.Context, config.Config) {
			jobs <- struct{}{}
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	waitFor(t, "first ticker job", func() bool {
		select {
		case <-jobs:
			return true
		default:
			return false
		}
	})

	cfg.Client.Home = "desk-home"
	if err := cfg.Save(machine.Paths.ConfigFile()); err != nil {
		t.Fatalf("save client config: %v", err)
	}
	tk.Tick(context.Background())
	select {
	case <-jobs:
		t.Fatal("Tick() ran jobs after the fresh config made this machine a client")
	default:
	}
}

func TestTickSpacesFailedBackupChecksAnHourApart(t *testing.T) {
	machine := testutil.NewMachine(t)
	cfg := config.Default()
	cfg.Backup.GitRemote = machine.Paths.DataDir + "/missing-remote.git"
	if err := cfg.Save(machine.Paths.ConfigFile()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	var mu sync.Mutex
	var logs []string
	jobs := make(chan struct{}, 2)
	tk, err := ticker.Start(context.Background(), ticker.Options{
		Paths: machine.Paths,
		Every: time.Hour,
		Now:   func() time.Time { return time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC) },
		Logf: func(format string, args ...any) {
			mu.Lock()
			logs = append(logs, strings.TrimSpace(format))
			mu.Unlock()
		},
		Jobs: func(context.Context, config.Config) {
			jobs <- struct{}{}
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	waitFor(t, "first ticker job after the failed backup", func() bool {
		select {
		case <-jobs:
			return true
		default:
			return false
		}
	})
	mu.Lock()
	first := strings.Join(logs, "\n")
	logs = nil
	mu.Unlock()
	if !strings.Contains(first, "backup:") {
		t.Fatalf("first tick logs = %q, want its due backup failure", first)
	}

	tk.Tick(context.Background())
	waitFor(t, "second ticker job", func() bool {
		select {
		case <-jobs:
			return true
		default:
			return false
		}
	})
	mu.Lock()
	second := strings.Join(logs, "\n")
	mu.Unlock()
	if strings.Contains(second, "backup:") {
		t.Fatalf("second tick logs = %q, want no backup retry within an hour", second)
	}
}

func TestStopSignalsASeparateRunningTicker(t *testing.T) {
	if os.Getenv(tickerHelper) == "1" {
		paths := config.Paths{
			ConfigDir: os.Getenv("HERDR_DESK_TEST_CONFIG_DIR"),
			StateDir:  os.Getenv("HERDR_DESK_TEST_STATE_DIR"),
			DataDir:   os.Getenv("HERDR_DESK_TEST_DATA_DIR"),
			CacheDir:  os.Getenv("HERDR_DESK_TEST_CACHE_DIR"),
		}
		tk, err := ticker.Start(context.Background(), ticker.Options{Paths: paths, Every: time.Hour, Logf: func(string, ...any) {}})
		if err != nil {
			t.Fatalf("helper Start() error = %v", err)
		}
		defer tk.Close()
		select {}
	}

	machine := testutil.NewMachine(t)
	if err := config.Default().Save(machine.Paths.ConfigFile()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestStopSignalsASeparateRunningTicker$")
	cmd.Env = append(os.Environ(),
		tickerHelper+"=1",
		"HERDR_DESK_TEST_CONFIG_DIR="+machine.Paths.ConfigDir,
		"HERDR_DESK_TEST_STATE_DIR="+machine.Paths.StateDir,
		"HERDR_DESK_TEST_DATA_DIR="+machine.Paths.DataDir,
		"HERDR_DESK_TEST_CACHE_DIR="+machine.Paths.CacheDir,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ticker helper: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	waitFor(t, "ticker helper to hold its lock", func() bool {
		info, running := ticker.Running(machine.Paths)
		return running && info.PID == cmd.Process.Pid
	})

	if err := ticker.Stop(machine.Paths, 2*time.Second); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("ticker helper exited successfully, want it to have received SIGTERM")
	}
	if held, err := config.LockHeld(machine.Paths.TickerLock()); err != nil || held {
		t.Fatalf("ticker lock after Stop() = (%t, %v), want (false, nil)", held, err)
	}
}

// runner.enabled = false stops new runs, not the checks of live ones: a running run whose pane closed is ended by the
// ticker whatever the switch says.
func TestTickReconcilesALiveRunWithTheRunnerOff(t *testing.T) {
	testutil.FakeHerdr(t)
	machine := testutil.NewMachine(t)
	cfg := config.Default()
	cfg.Runner.Enabled = false
	if err := cfg.Save(machine.Paths.ConfigFile()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	ctx := context.Background()
	hourAgo := time.Now().Add(-time.Hour)
	st, err := store.Open(machine.Paths.DB(), store.Options{Now: func() time.Time { return hourAgo }})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	task, err := st.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "pane closed"}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	run, err := st.StartRun(ctx, task.Number, store.RunRoute{Root: t.TempDir(), Isolation: "self"}, store.RunCaps{Slots: 1, PerDay: 1000})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	h := &herdr.Client{Bin: os.Getenv("DESK_HERDR")}
	created, err := h.CreateWorkspace(ctx, t.TempDir(), "desk T1", nil)
	if err != nil {
		t.Fatalf("open the run's pane: %v", err)
	}
	if err := h.ClosePane(ctx, created.Pane); err != nil {
		t.Fatalf("close the run's pane: %v", err)
	}
	if ok, err := st.UpdateRun(ctx, run.ID, model.RunStarting, store.RunUpdate{State: model.RunRunning, Session: "s1",
		Workspace: created.Workspace, Pane: created.Pane}); err != nil || !ok {
		t.Fatalf("make the run running = (%t, %v)", ok, err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	tk, err := ticker.Start(ctx, ticker.Options{Paths: machine.Paths, Every: time.Hour, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	waitFor(t, "the ticker to end the run whose pane closed", func() bool {
		ro, err := store.OpenReadOnly(machine.Paths.DB(), store.Options{})
		if err != nil {
			return false
		}
		defer ro.Close()
		cur, ok, err := ro.CurrentRun(ctx, task.Number)
		return err == nil && ok && cur.State == model.RunEnded
	})
}

// A crashed ticker leaves its info file; a new ticker that holds the lock but has not yet written its own must not
// get the old pid signalled, which may by now name an unrelated process.
func TestStopDoesNotSignalTheStalePidOfACrashedTicker(t *testing.T) {
	machine := testutil.NewMachine(t)
	bystander := exec.Command("sleep", "30")
	if err := bystander.Start(); err != nil {
		t.Fatalf("start a bystander process: %v", err)
	}
	exited := make(chan struct{})
	go func() { _ = bystander.Wait(); close(exited) }()
	t.Cleanup(func() { _ = bystander.Process.Kill(); <-exited })
	stale, err := json.Marshal(ticker.Info{PID: bystander.Process.Pid, StartedTS: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatalf("encode stale info: %v", err)
	}
	if err := config.WriteFileAtomic(machine.Paths.TickerInfo(), stale); err != nil {
		t.Fatalf("leave the crashed ticker's info: %v", err)
	}
	unlock, err := config.TryLock(machine.Paths.TickerLock())
	if err != nil {
		t.Fatalf("take the lock as the new ticker: %v", err)
	}
	t.Cleanup(func() { _ = unlock() })

	if err := ticker.Stop(machine.Paths, 300*time.Millisecond); err == nil {
		t.Fatal("Stop() error = nil, want it to give up on a holder that published no pid")
	}
	select {
	case <-exited:
		t.Fatal("Stop() signalled the crashed ticker's stale pid")
	case <-time.After(200 * time.Millisecond):
	}
}

func waitFor(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		case <-poll.C:
		}
	}
}
