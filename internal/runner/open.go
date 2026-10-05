package runner

import (
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/secretscan"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// Open opens the store at p.DB() with the options the config sets and returns a runner over it. It is the one
// place a command, the ticker, or the rpc server opens the store; Close closes it.
func Open(p config.Paths, c config.Config) (*Runner, error) {
	st, err := store.Open(p.DB(), store.Options{
		Scanner:   secretscan.FromConfig(c.SecretScan.Command),
		AutoStart: c.Coordinator.StartRuns == config.StartRunsAuto,
		OnMerged:  model.Status(c.Runner.OnMerged),
	})
	if err != nil {
		return nil, err
	}
	r := New(Options{Store: st, Config: c, Paths: p})
	r.owned = true
	return r, nil
}

// Close closes the store Open opened. A Runner from New closes nothing.
func (r *Runner) Close() error {
	if !r.owned {
		return nil
	}
	r.owned = false
	return r.o.Store.Close()
}

// Store is the runner's store.
func (r *Runner) Store() *store.Store { return r.o.Store }
