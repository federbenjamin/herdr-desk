// Package ticker is herdr-desk's one long-lived process on a home: started by herdr, it holds a lock so only one
// runs, and once a minute it reads the config and does the timed jobs.
package ticker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/backup"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/runner"
)

// ErrRunning is Start finding another ticker holding the lock.
var ErrRunning = errors.New("a ticker is already running")

// backupEvery is how often the ticker checks whether a backup is due, so a failing remote is not retried every tick.
const backupEvery = time.Hour

// Options configures a Ticker.
type Options struct {
	Paths config.Paths
	Every time.Duration                              // 0 → time.Minute
	Now   func() time.Time                           // nil → time.Now
	Logf  func(format string, args ...any)           // nil → a logger on Paths.Log()
	Jobs  func(ctx context.Context, c config.Config) // nil → the runner's run jobs when runner.enabled
}

// Info is the ticker info file.
type Info struct {
	PID       int       `json:"pid"`
	StartedTS time.Time `json:"started_ts"`
}

// Ticker is a running ticker.
type Ticker struct {
	o      Options
	unlock func() error
	logf   *os.File  // the log Logf writes to when the caller gave none
	stdLog io.Writer // the standard logger's output before Start pointed it at logf
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
	err    error

	tickMu     sync.Mutex // one tick at a time
	backupSeen time.Time  // the last tick that checked whether a backup is due
}

// Start takes the lock, writes the info file, ticks at once, then every o.Every until Close. It returns ErrRunning
// when another ticker holds the lock.
func Start(ctx context.Context, o Options) (*Ticker, error) {
	if o.Every <= 0 {
		o.Every = time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	unlock, err := config.TryLock(o.Paths.TickerLock())
	if errors.Is(err, config.ErrLocked) {
		return nil, ErrRunning
	}
	if err != nil {
		return nil, err
	}
	t := &Ticker{o: o, unlock: unlock}
	if t.o.Logf == nil {
		f, err := os.OpenFile(o.Paths.Log(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
		if err != nil {
			unlock()
			return nil, err
		}
		// The runner logs through the standard logger, so it goes to the ticker's log too until Close.
		t.logf, t.stdLog = f, log.Writer()
		log.SetOutput(f)
		t.o.Logf = log.New(f, "herdr-desk ticker: ", log.LstdFlags).Printf
	}
	b, err := json.Marshal(Info{PID: os.Getpid(), StartedTS: o.Now().UTC()})
	if err == nil {
		err = config.WriteFileAtomic(o.Paths.TickerInfo(), b)
	}
	if err != nil {
		t.Close()
		return nil, err
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	t.cancel = cancel
	t.wg.Go(func() { t.loop(loopCtx) })
	return t, nil
}

func (t *Ticker) loop(ctx context.Context) {
	tk := time.NewTicker(t.o.Every)
	defer tk.Stop()
	for {
		t.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

// Tick does the timed jobs once: it reads the config file afresh (one that fails to load is logged and the tick
// skipped), checks the backup on the first tick and then once an hour, then runs the run jobs. The store is opened
// only when a job needs it and closed before Tick returns.
func (t *Ticker) Tick(ctx context.Context) {
	t.tickMu.Lock()
	defer t.tickMu.Unlock()
	p := t.o.Paths
	c, err := config.Load(p.ConfigFile())
	if err != nil {
		t.o.Logf("the config does not load, so this tick does nothing: %v", err)
		return
	}
	if c.IsClient() {
		return
	}
	var r *runner.Runner
	open := func() bool {
		if r == nil {
			if r, err = runner.Open(p, c); err != nil {
				t.o.Logf("open the store: %v", err)
				return false
			}
		}
		return true
	}
	defer func() {
		if r != nil {
			if err := r.Close(); err != nil {
				t.o.Logf("close the store: %v", err)
			}
		}
	}()
	if remote := c.Backup.GitRemote; remote != "" {
		now := t.o.Now()
		if t.backupSeen.IsZero() || now.Sub(t.backupSeen) >= backupEvery {
			t.backupSeen = now
			if backup.Due(p, now) && open() {
				if _, err := backup.Run(ctx, r.Store(), p, remote); err != nil {
					t.o.Logf("backup: %v", err)
				}
			}
		}
	}
	if t.o.Jobs != nil {
		t.o.Jobs(ctx, c)
		return
	}
	if c.Runner.Enabled && open() {
		r.Tick(ctx)
	}
}

// Close stops the ticks, removes the info file, and releases the lock.
func (t *Ticker) Close() error {
	t.once.Do(func() {
		if t.cancel != nil {
			t.cancel()
		}
		t.wg.Wait()
		var errs []error
		if err := os.Remove(t.o.Paths.TickerInfo()); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
		errs = append(errs, t.unlock())
		if t.logf != nil {
			log.SetOutput(t.stdLog)
			errs = append(errs, t.logf.Close())
		}
		t.err = errors.Join(errs...)
	})
	return t.err
}

// Run is Start, then wait for SIGINT, SIGTERM, or ctx, then Close.
func Run(ctx context.Context, o Options) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	t, err := Start(ctx, o)
	if err != nil {
		return err
	}
	<-ctx.Done()
	return t.Close()
}

// Running returns the info of the ticker that holds the lock; false when none does or its info file cannot be read.
func Running(p config.Paths) (Info, bool) {
	if held, err := config.LockHeld(p.TickerLock()); err != nil || !held {
		return Info{}, false
	}
	info, err := readInfo(p)
	return info, err == nil
}

func readInfo(p config.Paths) (Info, error) {
	var info Info
	b, err := os.ReadFile(p.TickerInfo())
	if err != nil {
		return info, err
	}
	return info, json.Unmarshal(b, &info)
}

// Stop signals the running ticker and waits up to timeout for the lock to be free. It signals only while the lock
// is held, so a stale info file never gets another process killed. No ticker is not an error.
func Stop(p config.Paths, timeout time.Duration) error {
	held, err := config.LockHeld(p.TickerLock())
	if err != nil || !held {
		return err
	}
	info, err := readInfo(p)
	if err != nil {
		return fmt.Errorf("a ticker holds %s but %s cannot be read: %w", p.TickerLock(), filepath.Base(p.TickerInfo()), err)
	}
	if err := syscall.Kill(info.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal the ticker (pid %d): %w", info.PID, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		held, err := config.LockHeld(p.TickerLock())
		if err != nil || !held {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the ticker (pid %d) still holds %s after %s", info.PID, p.TickerLock(), timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
