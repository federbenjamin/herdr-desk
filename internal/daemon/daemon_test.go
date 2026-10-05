package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/daemon"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
	"github.com/federbenjamin/herdr-desk/internal/version"
)

func TestMain(m *testing.M) {
	if os.Getenv("DESK_TEST_DAEMON_CHILD") == "1" {
		paths := config.ResolvePaths(os.Getenv)
		cfg, err := config.Load(paths.ConfigFile())
		if err != nil {
			os.Exit(2)
		}
		if err := daemon.Run(context.Background(), paths, cfg); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestStartServesSecureEndpointsWritesInfoOnceAndCloseCleansUp(t *testing.T) {
	machine := testutil.NewMachine(t)
	paths := machine.Paths
	cfg := config.Default()
	cfg.Home.Listen = "127.0.0.1:0"

	instance, err := daemon.Start(context.Background(), paths, cfg)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = instance.Close()
		}
	})

	socketInfo, err := os.Stat(paths.Socket())
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got := socketInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket permissions = %04o, want 0600", got)
	}

	info, err := daemon.ReadInfo(paths)
	if err != nil {
		t.Fatalf("ReadInfo() error = %v", err)
	}
	if info.PID != os.Getpid() {
		t.Fatalf("info PID = %d, want %d", info.PID, os.Getpid())
	}
	if info.Version != version.Version {
		t.Fatalf("info version = %q, want %q", info.Version, version.Version)
	}
	if info.StartedTS.IsZero() {
		t.Fatal("info StartedTS is zero")
	}
	if info.Socket != paths.Socket() {
		t.Fatalf("info socket = %q, want %q", info.Socket, paths.Socket())
	}
	if info.Listen == "" || info.Listen != instance.Listen() {
		t.Fatalf("info listen = %q, instance Listen() = %q", info.Listen, instance.Listen())
	}
	// The digest is of the config as the file holds it ("127.0.0.1:0"), not of the address the daemon then bound.
	if info.ConfigDigest != cfg.Digest() {
		t.Fatalf("info config digest = %q, want %q (the config the daemon was given)", info.ConfigDigest, cfg.Digest())
	}

	if conn, err := net.DialTimeout("tcp", instance.Listen(), time.Second); err != nil {
		t.Fatalf("dial TCP listener: %v", err)
	} else {
		_ = conn.Close()
	}

	before, err := os.ReadFile(paths.DaemonInfo())
	if err != nil {
		t.Fatalf("read info before status call: %v", err)
	}
	client := api.NewClient(api.ClientOptions{Paths: paths, Config: cfg})
	if _, err := client.Status(context.Background()); err != nil {
		t.Fatalf("Status() through unix socket: %v", err)
	}
	after, err := os.ReadFile(paths.DaemonInfo())
	if err != nil {
		t.Fatalf("read info after status call: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("daemon info changed after start")
	}

	if err := instance.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	closed = true
	for _, path := range []string{paths.Socket(), paths.DaemonInfo()} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s remains after Close(): %v", path, err)
		}
	}
}

func TestStartRejectsClientAndContendedDaemon(t *testing.T) {
	machine := testutil.NewMachine(t)
	paths := machine.Paths
	cfg := config.Default()

	instance, err := daemon.Start(context.Background(), paths, cfg)
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })

	if _, err := daemon.Start(context.Background(), paths, cfg); !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Fatalf("second Start() error = %v, want ErrAlreadyRunning", err)
	}

	clientMachine := testutil.NewMachine(t)
	clientCfg := config.Default()
	clientCfg.Client.Home = "127.0.0.1:44999"
	if _, err := daemon.Start(context.Background(), clientMachine.Paths, clientCfg); !errors.Is(err, daemon.ErrClient) {
		t.Fatalf("client Start() error = %v, want ErrClient", err)
	}
}

func TestStartRemovesStaleSocket(t *testing.T) {
	machine := testutil.NewMachine(t)
	paths := machine.Paths
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		t.Fatalf("make state directory: %v", err)
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: paths.Socket(), Net: "unix"})
	if err != nil {
		t.Fatalf("create stale socket: %v", err)
	}
	stale := paths.Socket() + ".stale"
	if err := os.Rename(paths.Socket(), stale); err != nil {
		t.Fatalf("preserve socket after listener close: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close stale socket listener: %v", err)
	}
	if err := os.Rename(stale, paths.Socket()); err != nil {
		t.Fatalf("restore stale socket path: %v", err)
	}

	instance, err := daemon.Start(context.Background(), paths, config.Default())
	if err != nil {
		t.Fatalf("Start() with stale socket: %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	conn, err := net.DialTimeout("unix", paths.Socket(), time.Second)
	if err != nil {
		t.Fatalf("dial replacement socket: %v", err)
	}
	_ = conn.Close()
}

func TestStartEnforcesSocketPathByteBoundary(t *testing.T) {
	machine := testutil.NewMachine(t)
	for _, n := range []int{103, 104} {
		t.Run(fmt.Sprintf("%d-bytes", n), func(t *testing.T) {
			paths := machine.Paths
			paths.StateDir += strings.Repeat("x", n-len(paths.Socket()))
			if got := len(paths.Socket()); got != n {
				t.Fatalf("socket path length = %d, want %d", got, n)
			}

			instance, err := daemon.Start(context.Background(), paths, config.Default())
			if n == 103 {
				if err != nil {
					t.Fatalf("Start() with %d-byte socket path: %v", n, err)
				}
				t.Cleanup(func() { _ = instance.Close() })
				return
			}
			if err == nil {
				t.Fatalf("Start() with %d-byte socket path succeeded", n)
			}
			if !strings.Contains(err.Error(), paths.Socket()) {
				t.Fatalf("Start() error = %q, want it to name %q", err, paths.Socket())
			}
		})
	}
}

func TestStopDoesNotSignalThePIDInStaleInfoWithoutALock(t *testing.T) {
	machine := testutil.NewMachine(t)
	paths := machine.Paths
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		t.Fatalf("make state directory: %v", err)
	}
	stale, err := json.Marshal(daemon.Info{PID: os.Getpid(), Socket: paths.Socket()})
	if err != nil {
		t.Fatalf("marshal stale info: %v", err)
	}
	if err := os.WriteFile(paths.DaemonInfo(), stale, 0o600); err != nil {
		t.Fatalf("write stale info: %v", err)
	}

	if err := daemon.Stop(paths, time.Second); err != nil {
		t.Fatalf("Stop() with stale info and no lock: %v", err)
	}
}

func TestReadInfoRejectsMalformedJSON(t *testing.T) {
	machine := testutil.NewMachine(t)
	paths := machine.Paths
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		t.Fatalf("make state directory: %v", err)
	}
	if err := os.WriteFile(paths.DaemonInfo(), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write malformed daemon info: %v", err)
	}
	if _, err := daemon.ReadInfo(paths); err == nil {
		t.Fatal("ReadInfo() accepted malformed JSON")
	}
}

func TestBackupRunRefusesWhenNoRemoteIsConfigured(t *testing.T) {
	machine := testutil.NewMachine(t)
	paths := machine.Paths
	cfg := config.Default()
	instance, err := daemon.Start(context.Background(), paths, cfg)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })

	_, err = api.NewClient(api.ClientOptions{Paths: paths, Config: cfg}).Backup(context.Background())
	refusal, ok := model.AsRefusal(err)
	if !ok || refusal.Code != model.CodeBackupOff {
		t.Fatalf("Backup() error = %v, want backup-off refusal", err)
	}
}

func TestSpawnStartsDetachedDaemonAndStopSignalsIt(t *testing.T) {
	machine := testutil.NewMachine(t)
	paths := machine.Paths
	t.Setenv("DESK_TEST_DAEMON_CHILD", "1")
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(paths.ConfigDir))
	t.Setenv("XDG_STATE_HOME", filepath.Dir(paths.StateDir))
	t.Setenv("XDG_DATA_HOME", filepath.Dir(paths.DataDir))
	t.Setenv("XDG_CACHE_HOME", filepath.Dir(paths.CacheDir))

	if err := daemon.Spawn(paths); err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_ = daemon.Stop(paths, 5*time.Second)
		}
	})
	var info daemon.Info
	pollUntil(t, time.Second, func() bool {
		var err error
		info, err = daemon.ReadInfo(paths)
		return err == nil
	})
	if info.PID == os.Getpid() {
		t.Fatalf("spawned daemon PID = %d, want detached process", info.PID)
	}
	logInfo, err := os.Stat(paths.DaemonLog())
	if err != nil {
		t.Fatalf("stat daemon log: %v", err)
	}
	if got := logInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("daemon log permissions = %04o, want 0600", got)
	}

	if err := daemon.Stop(paths, 5*time.Second); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	stopped = true
	for _, path := range []string{paths.Socket(), paths.DaemonInfo()} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s remains after Stop(): %v", path, err)
		}
	}
}

func TestBackupTickRunsDueBackupAndSkipsRecentRun(t *testing.T) {
	restoreTick := daemon.SetBackupTick(5 * time.Millisecond)
	t.Cleanup(restoreTick)

	machine := testutil.NewMachine(t)
	paths := machine.Paths
	remote := filepath.Join(paths.DataDir, "remote.git")
	initBareRepository(t, remote)
	cfg := config.Default()
	cfg.Backup.GitRemote = remote
	instance, err := daemon.Start(context.Background(), paths, cfg)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })

	client := api.NewClient(api.ClientOptions{Paths: paths, Config: cfg})
	if _, err := client.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{
		TaskData: model.TaskData{Title: "included by the due backup"},
	}); err != nil {
		t.Fatalf("add task before due backup: %v", err)
	}
	pollUntil(t, 5*time.Second, func() bool {
		_, err := os.Stat(paths.BackupState())
		return err == nil
	})
	first, err := os.ReadFile(filepath.Join(paths.BackupDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("read due backup export: %v", err)
	}
	if !strings.Contains(string(first), "included by the due backup") {
		t.Fatalf("due backup export = %q, want its task", first)
	}

	if _, err := client.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{
		TaskData: model.TaskData{Title: "must wait for tomorrow"},
	}); err != nil {
		t.Fatalf("add task after backup state: %v", err)
	}
	deadline := time.NewTimer(150 * time.Millisecond)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-poll.C:
			exported, err := os.ReadFile(filepath.Join(paths.BackupDir(), "events.jsonl"))
			if err != nil {
				t.Fatalf("read recent backup export: %v", err)
			}
			if string(exported) != string(first) {
				t.Fatalf("a recent backup ran again: export changed from %q to %q", first, exported)
			}
		}
	}
}

func TestAFailingHourlyBackupShowsInStatusAndIsTriedAgainOnTheNextTick(t *testing.T) {
	restoreTick := daemon.SetBackupTick(5 * time.Millisecond)
	t.Cleanup(restoreTick)

	paths := testutil.NewMachine(t).Paths
	remote := filepath.Join(paths.DataDir, "remote.git")
	cfg := config.Default()
	cfg.Backup.GitRemote = remote
	instance, err := daemon.Start(context.Background(), paths, cfg)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	client := api.NewClient(api.ClientOptions{Paths: paths, Config: cfg})
	status := func() api.Status {
		t.Helper()
		st, err := client.Status(context.Background())
		if err != nil {
			t.Fatalf("Status(): %v", err)
		}
		return st
	}

	pollUntil(t, 5*time.Second, func() bool { return status().BackupError != "" })
	if st := status(); st.BackupTS != nil || !strings.Contains(st.BackupError, "git push") {
		t.Fatalf("status after a failed hourly backup = (%v, %q), want no success and the push's error", st.BackupTS, st.BackupError)
	}

	initBareRepository(t, remote)
	pollUntil(t, 5*time.Second, func() bool { return status().BackupTS != nil })
	if st := status(); st.BackupError != "" {
		t.Fatalf("status after the next tick's success = (%v, %q), want the error cleared", st.BackupTS, st.BackupError)
	}
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

func initBareRepository(t *testing.T, path string) {
	t.Helper()
	output, err := exec.Command("git", "init", "--bare", path).CombinedOutput()
	if err != nil {
		t.Fatalf("initialize bare backup remote: %v\n%s", err, output)
	}
}
