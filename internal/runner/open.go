package runner

import (
	"errors"
	"log"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/secretscan"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// Open opens the store at p.DB() with the options the config sets and returns a runner over it. It is the one
// place a command, the ticker, or the rpc server opens the store. The runner logs to p.Log(), whichever process opened
// it, so a line the runner writes while serving a client's request or a hook lands where the owner reads; a log that
// cannot be opened leaves the runner on the standard logger. Close closes both.
func Open(p config.Paths, c config.Config) (*Runner, error) {
	st, err := store.Open(p.DB(), store.Options{
		Scanner:  secretscan.FromConfig(c.SecretScan.Command),
		OnMerged: model.Status(c.Runner.OnMerged),
	})
	if err != nil {
		return nil, err
	}
	o := Options{Store: st, Config: c, Paths: p}
	f, err := p.OpenLog()
	if err == nil {
		o.Logf = log.New(f, "", log.LstdFlags).Printf
	}
	r := New(o)
	r.owned = true
	if f != nil {
		r.closeLog = f.Close
	}
	return r, nil
}

// Close closes the store and the log Open opened. A Runner from New closes nothing.
func (r *Runner) Close() error {
	if !r.owned {
		return nil
	}
	r.owned = false
	err := r.o.Store.Close()
	if r.closeLog != nil {
		err = errors.Join(err, r.closeLog())
	}
	return err
}

// Store is the runner's store.
func (r *Runner) Store() *store.Store { return r.o.Store }
