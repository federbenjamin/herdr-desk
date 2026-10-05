// Package testutil gives tests real desk machines: a home whose own client answers in the test's process, and
// clients of it whose [client] command runs the test binary as `herdr-desk rpc`, each on its own short temp
// directories. It is imported only by tests.
package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
)

// rpcEnv names the home root a test binary run as an rpc helper answers for.
const rpcEnv = "DESK_TESTUTIL_RPC"

// A test binary that links this package never finds the real herdr: DESK_HERDR names a path that does not exist, so
// herdr.Find fails instead of searching PATH. A test that needs the fake herdr calls FakeHerdr. An rpc helper keeps
// the DESK_HERDR of the test that started it.
func init() {
	if os.Getenv(rpcEnv) == "" {
		os.Setenv("DESK_HERDR", "/nonexistent/desk-tests-never-run-the-real-herdr")
	}
}

// servesRPC is set by ServeRPCIfAsked: the package's TestMain lets the test binary act as a home's rpc helper.
var servesRPC bool

// ServeRPCIfAsked, called first in TestMain, makes this test binary a home's rpc helper when a client machine's
// [client] command started it: it answers one request on stdin through api.ServeRPC and exits, 255 while the home is
// stopped, as ssh does when it cannot connect. Otherwise it returns at once.
func ServeRPCIfAsked() {
	servesRPC = true
	root := os.Getenv(rpcEnv)
	if root == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(root, "down")); err == nil {
		fmt.Fprintln(os.Stderr, "testutil: the home is stopped")
		os.Exit(255)
	}
	m := &Machine{Paths: config.Paths{ConfigDir: filepath.Join(root, "config", "herdr-desk")}}
	m.Paths = config.ResolvePaths(m.Getenv(nil))
	cfg, err := config.Load(m.Paths.ConfigFile())
	if err == nil {
		err = api.ServeRPC(context.Background(), m.Paths, cfg, os.Stdin, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "testutil: rpc:", err)
		os.Exit(3)
	}
	os.Exit(0)
}

// FakeHerdr points DESK_HERDR at scripts/e2e/fake-herdr.py and FAKE_HERDR_DIR at a fresh temp dir, both restored at
// test cleanup, and returns that dir. It uses t.Setenv, so the test must not be parallel.
func FakeHerdr(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("testutil: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("testutil: no go.mod above the test's directory")
		}
		dir = parent
	}
	state := t.TempDir()
	t.Setenv("DESK_HERDR", filepath.Join(dir, "scripts", "e2e", "fake-herdr.py"))
	t.Setenv("FAKE_HERDR_DIR", state)
	return state
}

// Machine is one machine's four XDG directories under a short temp dir.
type Machine struct{ Paths config.Paths }

// NewMachine returns a machine on a fresh temp dir, removed at test cleanup. The dir sits under /tmp when it
// can, because a unix socket path (an ssh control socket) is limited to 104 bytes and a test's own temp dir can be
// longer.
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
	m := &Machine{Paths: config.Paths{ConfigDir: filepath.Join(root, "config", "herdr-desk")}}
	m.Paths = config.ResolvePaths(m.Getenv(nil))
	return m
}

// root is the temp dir the machine's four directories sit in.
func (m *Machine) root() string { return filepath.Dir(filepath.Dir(m.Paths.ConfigDir)) }

// Getenv returns a lookup that answers the XDG variables for this machine, then extra, else "".
func (m *Machine) Getenv(extra map[string]string) func(string) string {
	root := m.root()
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
}

// Home is a desk home on a Machine: its config file, and clients that answer in this process.
type Home struct {
	*Machine
	t testing.TB
}

// StartHome writes the home's config and starts nothing: every command on a home opens the store itself.
func StartHome(t testing.TB, o HomeOptions) *Home {
	t.Helper()
	cfg := o.Config
	if reflect.DeepEqual(cfg, config.Config{}) {
		cfg = config.Default()
	}
	h := &Home{Machine: NewMachine(t), t: t}
	if err := cfg.Save(h.Paths.ConfigFile()); err != nil {
		t.Fatalf("testutil: save config: %v", err)
	}
	return h
}

// down is the file whose presence makes the home's rpc helper exit 255.
func (h *Home) down() string { return filepath.Join(h.root(), "down") }

// Stop makes the home unreachable from its client machines; its files stay, and its own client still answers.
func (h *Home) Stop() {
	if err := os.WriteFile(h.down(), nil, 0o600); err != nil {
		h.t.Fatalf("testutil: stop the home: %v", err)
	}
}

// Restart makes the home reachable again.
func (h *Home) Restart(t testing.TB) {
	t.Helper()
	if err := os.Remove(h.down()); err != nil && !os.IsNotExist(err) {
		t.Fatalf("testutil: restart the home: %v", err)
	}
}

// Client returns a client on the home machine itself, on the config file as it is now, through the local
// transport. It is closed at test cleanup.
func (h *Home) Client() *api.Client {
	h.t.Helper()
	cfg, err := config.Load(h.Paths.ConfigFile())
	if err != nil {
		h.t.Fatalf("testutil: load the home's config: %v", err)
	}
	c := api.NewClient(api.ClientOptions{Paths: h.Paths, Config: cfg})
	h.t.Cleanup(func() { c.Close() })
	return c
}

// NewClientMachine returns a second machine set up as a client of h. Its [client] command runs this test binary as
// h's rpc helper, so the package's TestMain must call ServeRPCIfAsked first.
func NewClientMachine(t testing.TB, h *Home) *Machine {
	t.Helper()
	if !servesRPC {
		t.Fatal("testutil: NewClientMachine needs the package's TestMain to call testutil.ServeRPCIfAsked() first")
	}
	m := NewMachine(t)
	cfg := config.Default()
	cfg.Client.Home = "home"
	cfg.Client.Command = []string{"env", rpcEnv + "=" + h.root(), os.Args[0], "-test.run=^$"}
	if err := cfg.Save(m.Paths.ConfigFile()); err != nil {
		t.Fatalf("testutil: save client config: %v", err)
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
