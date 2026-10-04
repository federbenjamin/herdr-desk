package daemon

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/federbenjamin/desk/internal/config"
)

const spawnWait = 5 * time.Second

// Spawn starts `<this executable> daemon run` detached, output to the daemon log, and waits up to 5s for the socket.
// A socket that already answers starts nothing. A child that exits because another daemon holds the lock (two
// commands spawning at once) is not a failure: Spawn waits for that daemon's socket instead.
func Spawn(p config.Paths) error {
	if socketAnswers(p) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.DaemonLog()), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(p.DaemonLog(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon", "run")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.Now().Add(spawnWait)
	for {
		if socketAnswers(p) {
			return nil
		}
		select {
		case err := <-exited:
			if held, _ := lockHeld(p); !held && !socketAnswers(p) {
				return fmt.Errorf("the daemon exited before its socket answered (%v); see %s", err, p.DaemonLog())
			}
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the daemon's socket did not answer within %s; see %s", spawnWait, p.DaemonLog())
		}
	}
}

func socketAnswers(p config.Paths) bool {
	conn, err := net.DialTimeout("unix", p.Socket(), 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
