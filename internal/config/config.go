package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/pelletier/go-toml/v2"
)

// Config is the file $XDG_CONFIG_HOME/desk/config.toml.
type Config struct {
	Home       Home       `toml:"home"`
	Client     Client     `toml:"client"`
	Runner     Runner     `toml:"runner"`
	Roots      []Root     `toml:"roots"`
	Agent      Agent      `toml:"agent"`
	Notify     Notify     `toml:"notify"`
	SecretScan SecretScan `toml:"secret_scan"`
	Backup     Backup     `toml:"backup"`
}

// Home is set on the home machine only.
type Home struct {
	Listen string `toml:"listen"`
}

// Client is set on client machines only.
type Client struct {
	Home string `toml:"home"`
}

// Runner: Enabled is shown by the board; AgentsMayArm and OnMerged are read by the store. The four
// limits are written by setup and read by the runner (U2).
type Runner struct {
	Enabled       bool   `toml:"enabled"`
	Cap           int    `toml:"cap"`
	MaxRunsPerDay int    `toml:"max_runs_per_day"`
	MaxRunMinutes int    `toml:"max_run_minutes"`
	PollSeconds   int    `toml:"poll_seconds"`
	AgentsMayArm  bool   `toml:"agents_may_arm" comment:"WARNING: true lets any agent start unattended runs that spend your quota"`
	OnMerged      string `toml:"on_merged"` // "review" | "done"
}

// Root: written by `desk roots`; read by the router (U2).
type Root struct {
	Path      string `toml:"path"`
	About     string `toml:"about"`
	Isolation string `toml:"isolation"` // "" | self | worktree | in-place
}

// Agent: Router and Worker are written by a profile and read by the runner (U2). SessionEnv is read by the CLI.
type Agent struct {
	Router     []string `toml:"router"`
	Worker     []string `toml:"worker"`
	SessionEnv string   `toml:"session_env"`
}

// Notify: written by setup; read by the runner (U2).
type Notify struct {
	Command []string `toml:"command"`
}

// SecretScan names an external scanner; empty uses the built-in patterns.
type SecretScan struct {
	Command []string `toml:"command"`
}

// Backup names the git remote the nightly export is pushed to; empty turns backup off.
type Backup struct {
	GitRemote string `toml:"git_remote"`
}

// Default is the config of a fresh desk: runner off, cap 1, 20 runs a day, 180 minutes, poll 30,
// on_merged "review".
func Default() Config {
	return Config{Runner: Runner{
		Cap:           1,
		MaxRunsPerDay: 20,
		MaxRunMinutes: 180,
		PollSeconds:   30,
		OnMerged:      "review",
	}}
}

// Load reads the config at path. A missing file is Default(), nil; unknown keys and bad values are errors.
// A key the file leaves out keeps its default.
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return Config{}, err
	}
	if err := toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save writes the config to path, 0600, through a temp file and a rename. The comment above
// agents_may_arm is part of the struct, so every save keeps it.
func (c Config) Save(path string) error {
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, b)
}

// WriteFileAtomic writes b to path at 0600 in a 0700 folder, through a temp file and a rename. The config, the
// client's snapshot, and the backup export all write through it.
func WriteFileAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var isolations = []string{"", "self", "worktree", "in-place"}

// ValidIsolation reports whether s is an isolation value: "", self, worktree, or in-place.
func ValidIsolation(s string) bool { return slices.Contains(isolations, s) }

// Validate checks on_merged, each root's isolation, and that listen and client.home are host:port, with
// listen never on a wildcard host. An empty on_merged reads as "review".
func (c Config) Validate() error {
	switch c.Runner.OnMerged {
	case "", "review", "done":
	default:
		return fmt.Errorf("runner.on_merged must be \"review\" or \"done\", not %q", c.Runner.OnMerged)
	}
	for _, r := range c.Roots {
		if !ValidIsolation(r.Isolation) {
			return fmt.Errorf("roots %s: isolation must be self, worktree, or in-place, not %q", r.Path, r.Isolation)
		}
	}
	if c.Home.Listen != "" {
		if err := checkHostPort(c.Home.Listen, true); err != nil {
			return fmt.Errorf("home.listen: %w", err)
		}
	}
	if c.Client.Home != "" {
		if err := checkHostPort(c.Client.Home, false); err != nil {
			return fmt.Errorf("client.home: %w", err)
		}
	}
	return nil
}

// checkHostPort accepts host:port with a named host. A listen address may use port 0 (any free port) and
// never a wildcard host, which would serve the API on every network.
func checkHostPort(addr string, listen bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q is not host:port", addr)
	}
	if host == "" {
		return fmt.Errorf("%q names no host", addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 || (n == 0 && !listen) {
		return fmt.Errorf("%q has a bad port", addr)
	}
	if listen {
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			return fmt.Errorf("%q is a wildcard address; name one interface", addr)
		}
	}
	return nil
}

// IsClient reports whether this machine is a client of another home.
func (c Config) IsClient() bool { return c.Client.Home != "" }

// AddRoot adds a root or, when one with that path exists, replaces it. The path must be absolute.
func (c *Config) AddRoot(r Root) error {
	if !filepath.IsAbs(r.Path) {
		return fmt.Errorf("root path %q is not absolute", r.Path)
	}
	if !ValidIsolation(r.Isolation) {
		return fmt.Errorf("isolation must be self, worktree, or in-place, not %q", r.Isolation)
	}
	r.Path = filepath.Clean(r.Path)
	for i := range c.Roots {
		if filepath.Clean(c.Roots[i].Path) == r.Path {
			c.Roots[i] = r
			return nil
		}
	}
	c.Roots = append(c.Roots, r)
	return nil
}

// RemoveRoot removes the root with that path; an unknown path is an error.
func (c *Config) RemoveRoot(path string) error {
	path = filepath.Clean(path)
	for i := range c.Roots {
		if filepath.Clean(c.Roots[i].Path) == path {
			c.Roots = slices.Delete(c.Roots, i, i+1)
			return nil
		}
	}
	return fmt.Errorf("no root %q", path)
}
