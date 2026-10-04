// Package daemon runs desk's one writer: it holds the lock, owns the store, and serves the API.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/backup"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/secretscan"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/version"
)

// Info is written once, at start, to the daemon info file.
type Info struct {
	PID       int       `json:"pid"`
	Version   string    `json:"version"`
	StartedTS time.Time `json:"started_ts"`
	Socket    string    `json:"socket"`
	Listen    string    `json:"listen"` // the address actually bound, "" when local only
}

// ErrAlreadyRunning is Start's answer when another daemon holds the lock.
var ErrAlreadyRunning = errors.New("daemon already running")

// ErrClient is Start's answer on a client machine.
var ErrClient = errors.New("this machine is a client; it runs no daemon")

// maxSocketPath is macOS's limit on a unix socket path, 104 bytes with the terminator.
const maxSocketPath = 103

var backupTick = time.Hour

// checkSocketPath refuses a state folder whose socket path is over the limit, naming the path, its length, and the
// limit.
func checkSocketPath(p config.Paths) error {
	if sock := p.Socket(); len(sock) > maxSocketPath {
		return fmt.Errorf("the socket path %s is %d bytes, over the %d a unix socket allows; set XDG_STATE_HOME to a shorter folder", sock, len(sock), maxSocketPath)
	}
	return nil
}

// Instance is a running daemon.
type Instance struct {
	p       config.Paths
	lock    *os.File
	st      *store.Store
	servers []*http.Server
	listen  string
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	once    sync.Once
	err     error
	failed  chan error // a listener that stopped serving on its own
}

// Start takes the lock, opens the store, serves the socket (and the TCP listener when configured), writes the
// info file, and starts the backup tick. It returns ErrAlreadyRunning when the lock is held and ErrClient on a client.
func Start(ctx context.Context, p config.Paths, c config.Config) (*Instance, error) {
	if c.IsClient() {
		return nil, ErrClient
	}
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(p.LockFile(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	i := &Instance{p: p, lock: lock, failed: make(chan error, 2)}
	ok := false
	defer func() {
		if !ok {
			i.Close()
		}
	}()

	if err := checkSocketPath(p); err != nil {
		return nil, err
	}
	sock := p.Socket()
	if i.st, err = store.Open(p.DB(), store.Options{
		Scanner:      secretscan.FromConfig(c.SecretScan.Command),
		AgentsMayArm: c.Runner.AgentsMayArm,
		OnMerged:     model.Status(c.Runner.OnMerged),
	}); err != nil {
		return nil, err
	}
	if err := os.Remove(sock); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	unixLn, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		unixLn.Close()
		return nil, err
	}
	var tcpLn net.Listener
	if c.Home.Listen != "" {
		if tcpLn, err = net.Listen("tcp", c.Home.Listen); err != nil {
			unixLn.Close()
			return nil, err
		}
		i.listen = tcpLn.Addr().String()
		c.Home.Listen = i.listen
	}

	started := time.Now().UTC()
	var runBackup func(context.Context) (backup.Result, error)
	if remote := c.Backup.GitRemote; remote != "" {
		var mu sync.Mutex
		runBackup = func(ctx context.Context) (backup.Result, error) {
			mu.Lock()
			defer mu.Unlock()
			return backup.Run(ctx, i.st, p, remote)
		}
	}
	srv := api.NewServer(api.ServerOptions{Store: i.st, Config: c, Paths: p, StartedTS: started, Backup: runBackup})
	i.serve(unixLn, srv.Handler(true))
	if tcpLn != nil {
		i.serve(tcpLn, srv.Handler(false))
	}

	if err := writeInfo(p, Info{PID: os.Getpid(), Version: version.Version, StartedTS: started, Socket: sock, Listen: i.listen}); err != nil {
		return nil, err
	}

	tickCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	i.cancel = cancel
	if runBackup != nil {
		i.wg.Go(func() { tick(tickCtx, p, runBackup) })
	}
	ok = true
	return i, nil
}

// serve answers ln until Close. A listener that fails on its own is reported to wait, which closes the whole
// daemon, so the lock is released and the next command starts a daemon that serves.
func (i *Instance) serve(ln net.Listener, h http.Handler) {
	s := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	i.servers = append(i.servers, s)
	i.wg.Go(func() {
		if err := s.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("desk daemon: serve %s: %v; stopping", ln.Addr(), err)
			select {
			case i.failed <- fmt.Errorf("serve %s: %w", ln.Addr(), err):
			default:
			}
		}
	})
}

// tick runs the backup each hour when one is due.
func tick(ctx context.Context, p config.Paths, run func(context.Context) (backup.Result, error)) {
	t := time.NewTicker(backupTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if !backup.Due(p, now) {
				continue
			}
			if _, err := run(ctx); err != nil {
				log.Printf("desk daemon: backup: %v", err)
			}
		}
	}
}

// Listen returns the TCP address bound, "" when none.
func (i *Instance) Listen() string { return i.listen }

// Close stops serving, closes the store, removes the socket and the info file, releases the lock.
func (i *Instance) Close() error {
	i.once.Do(func() { i.err = i.close() })
	return i.err
}

func (i *Instance) close() error {
	var errs []error
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range i.servers {
		if err := s.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if i.cancel != nil {
		i.cancel()
	}
	i.wg.Wait()
	if i.st != nil {
		errs = append(errs, i.st.Close())
	}
	for _, f := range []string{i.p.Socket(), i.p.DaemonInfo()} {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	errs = append(errs, unix.Flock(int(i.lock.Fd()), unix.LOCK_UN), i.lock.Close())
	return errors.Join(errs...)
}

// Run is Start, then wait for SIGINT/SIGTERM or ctx, then Close.
func Run(ctx context.Context, p config.Paths, c config.Config) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	i, err := Start(ctx, p, c)
	if err != nil {
		return err
	}
	return i.wait(ctx)
}

// wait closes the daemon when ctx ends or a listener fails, and returns the failure.
func (i *Instance) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return i.Close()
	case err := <-i.failed:
		return errors.Join(err, i.Close())
	}
}

func writeInfo(p config.Paths, info Info) error {
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(p.DaemonInfo(), b, 0o600)
}

// ReadInfo reads the daemon info file.
func ReadInfo(p config.Paths) (Info, error) {
	var info Info
	b, err := os.ReadFile(p.DaemonInfo())
	if err != nil {
		return info, err
	}
	return info, json.Unmarshal(b, &info)
}

// Running returns the info of the daemon that holds the lock; ok is false when none does or its info file cannot
// be read.
func Running(p config.Paths) (info Info, ok bool) {
	if held, err := lockHeld(p); err != nil || !held {
		return Info{}, false
	}
	info, err := ReadInfo(p)
	return info, err == nil
}

// lockHeld reports whether a daemon holds the lock. No lock file means none does.
func lockHeld(p config.Paths) (bool, error) {
	f, err := os.OpenFile(p.LockFile(), os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return true, nil
		}
		return false, err
	}
	return false, unix.Flock(int(f.Fd()), unix.LOCK_UN)
}

// Stop signals the running daemon and waits for the lock to be free. It signals only when the lock is held,
// so a stale info file never gets another process killed. No daemon is not an error.
func Stop(p config.Paths, timeout time.Duration) error {
	held, err := lockHeld(p)
	if err != nil || !held {
		return err
	}
	info, err := ReadInfo(p)
	if err != nil {
		return fmt.Errorf("a daemon holds %s but its info file cannot be read: %w", p.LockFile(), err)
	}
	if err := syscall.Kill(info.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal the daemon (pid %d): %w", info.PID, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		held, err := lockHeld(p)
		if err != nil || !held {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the daemon (pid %d) still holds %s after %s", info.PID, p.LockFile(), timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
