// Package testutil gives tests real desk machines: a home running in the test's process and clients of it,
// each on its own short temp directories. It is imported only by tests.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/daemon"
)

// Machine is one machine's four XDG directories under a short temp dir.
type Machine struct{ Paths config.Paths }

// NewMachine returns a machine on a fresh temp dir, removed at test cleanup. The dir sits under /tmp when it
// can, because a unix socket path is limited to 104 bytes and a test's own temp dir can be longer.
func NewMachine(t testing.TB) *Machine {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "dk")
	if err != nil {
		root, err = os.MkdirTemp("", "dk")
	}
	if err != nil {
		t.Fatalf("testutil: temp dir: %v", err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatalf("testutil: temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	m := &Machine{Paths: config.Paths{ConfigDir: filepath.Join(root, "config", "desk")}}
	m.Paths = config.ResolvePaths(m.Getenv(nil))
	return m
}

// Getenv returns a lookup that answers the XDG variables for this machine, then extra, else "".
func (m *Machine) Getenv(extra map[string]string) func(string) string {
	root := filepath.Dir(filepath.Dir(m.Paths.ConfigDir))
	xdg := map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(root, "config"),
		"XDG_STATE_HOME":  filepath.Join(root, "state"),
		"XDG_DATA_HOME":   filepath.Join(root, "data"),
		"XDG_CACHE_HOME":  filepath.Join(root, "cache"),
	}
	return func(k string) string {
		if v, ok := xdg[k]; ok {
			return v
		}
		return extra[k]
	}
}

// HomeOptions configures StartHome.
type HomeOptions struct {
	Config config.Config // the zero value means config.Default()
	Listen bool          // also serve TCP on 127.0.0.1, any free port, with a fresh token
}

// Home is a desk home running in this process on a Machine. It stops at test cleanup.
type Home struct {
	*Machine
	Addr  string // host:port when Listen
	Token string
	cfg   config.Config
	inst  *daemon.Instance
}

// StartHome writes the home's config (and its token when Listen) and starts its daemon in this process.
func StartHome(t testing.TB, o HomeOptions) *Home {
	t.Helper()
	cfg := o.Config
	if reflect.DeepEqual(cfg, config.Config{}) {
		cfg = config.Default()
	}
	h := &Home{Machine: NewMachine(t)}
	if o.Listen {
		cfg.Home.Listen = "127.0.0.1:0"
		token, err := config.RotateToken(h.Paths)
		if err != nil {
			t.Fatalf("testutil: token: %v", err)
		}
		h.Token = token
	}
	h.cfg = cfg
	h.start(t)
	t.Cleanup(h.Stop)
	if o.Listen {
		h.Addr = h.inst.Listen()
		h.cfg.Home.Listen = h.Addr
	}
	if err := h.cfg.Save(h.Paths.ConfigFile()); err != nil {
		t.Fatalf("testutil: save config: %v", err)
	}
	return h
}

func (h *Home) start(t testing.TB) {
	t.Helper()
	inst, err := daemon.Start(context.Background(), h.Paths, h.cfg)
	if err != nil {
		t.Fatalf("testutil: start the home: %v", err)
	}
	h.inst = inst
}

// Stop takes the home down; its files stay.
func (h *Home) Stop() {
	if h.inst == nil {
		return
	}
	h.inst.Close()
	h.inst = nil
}

// Restart brings the home up again on the same socket and address.
func (h *Home) Restart(t testing.TB) {
	t.Helper()
	h.Stop()
	h.start(t)
}

// Client returns a client on the home machine itself (unix socket).
func (h *Home) Client() *api.Client {
	return api.NewClient(api.ClientOptions{Paths: h.Paths, Config: h.cfg})
}

// NewClientMachine returns a second machine set up as a client of h, through config.Save and config.WriteToken.
func NewClientMachine(t testing.TB, h *Home) *Machine {
	t.Helper()
	if h.Addr == "" {
		t.Fatalf("testutil: NewClientMachine needs a home started with Listen")
	}
	m := NewMachine(t)
	cfg := config.Default()
	cfg.Client.Home = h.Addr
	if err := cfg.Save(m.Paths.ConfigFile()); err != nil {
		t.Fatalf("testutil: save client config: %v", err)
	}
	if err := config.WriteToken(m.Paths, h.Token); err != nil {
		t.Fatalf("testutil: write client token: %v", err)
	}
	return m
}

// ClientFor returns a client for a machine from its saved config. It panics when the config cannot be read.
func ClientFor(m *Machine) *api.Client {
	cfg, err := config.Load(m.Paths.ConfigFile())
	if err != nil {
		panic("testutil: " + err.Error())
	}
	return api.NewClient(api.ClientOptions{Paths: m.Paths, Config: cfg})
}
