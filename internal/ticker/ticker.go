// Package ticker is herdr-desk's one long-lived process on a home: started by herdr, it holds a lock so only one
// runs, and once a minute it reads the config and does the timed jobs.
package ticker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Jobs  func(ctx context.Context, c config.Config) // nil → the runner's run jobs
}

// Info is the ticker info file.
type Info struct {
	PID       int       `json:"pid"`
	StartedTS time.Time `json:"started_ts"`
}

// Ticker is a running ticker.
type Ticker struct {
	o          Options
	unlock     func() error
	unlockInfo func() error // the lock on the info file: held, it says the file is this ticker's
	logf       *os.File     // the log Logf writes to when the caller gave none
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	once       sync.Once
	err        error

	tickMu     sync.Mutex // one tick at a time
	backupSeen time.Time  // the last tick that checked whether a backup is due
}

// Start takes the lock, writes the info file and then locks it too, ticks at once, then every o.Every until Close.
// It returns ErrRunning when another ticker holds the lock. A crashed ticker's info file is left behind unlocked, so
// Stop never takes its pid for this ticker's.
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
		f, err := o.Paths.OpenLog()
		if err != nil {
			unlock()
			return nil, err
		}
		t.logf = f
		t.o.Logf = log.New(f, "herdr-desk ticker: ", log.LstdFlags).Printf
	}
	b, err := json.Marshal(Info{PID: os.Getpid(), StartedTS: o.Now().UTC()})
	if err == nil {
		err = config.WriteFileAtomic(o.Paths.TickerInfo(), b)
	}
	if err == nil {
		// Lock waits rather than fails: Stop and status probe the lock for an instant.
		t.unlockInfo, err = config.Lock(o.Paths.TickerInfo())
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
// skipped), checks the backup on the first tick and then once an hour, then runs the run jobs. The run jobs run
// whatever runner.enabled says: it stops new runs, not the reconcile or the repairs of live ones. The
// store is opened only when a job needs it and closed before Tick returns.
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
	if open() {
		r.Jobs(ctx)
	}
}

// Close stops the ticks, removes the info file, and releases the locks.
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
		if t.unlockInfo != nil {
			errs = append(errs, t.unlockInfo())
		}
		errs = append(errs, t.unlock())
		if t.logf != nil {
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

// Stop signals the running ticker and waits up to timeout for the lock to be free. It signals only the pid of an
// info file the lock holder has locked: a file a crashed ticker left is not, so Stop waits, up to timeout, for the new
// holder to publish its own, and never signals the old pid. No ticker is not an error.
func Stop(p config.Paths, timeout time.Duration) error {
	held, err := config.LockHeld(p.TickerLock())
	if err != nil || !held {
		return err
	}
	deadline := time.Now().Add(timeout)
	var info Info
	for {
		published, err := config.LockHeld(p.TickerInfo())
		if err != nil {
			return err
		}
		if published {
			if info, err = readInfo(p); err != nil {
				return fmt.Errorf("a ticker holds %s but %s cannot be read: %w", p.TickerLock(), filepath.Base(p.TickerInfo()), err)
			}
			break
		}
		if held, err := config.LockHeld(p.TickerLock()); err != nil || !held {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("a ticker holds %s but has not published its pid in %s", p.TickerLock(), filepath.Base(p.TickerInfo()))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := syscall.Kill(info.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal the ticker (pid %d): %w", info.PID, err)
	}
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
