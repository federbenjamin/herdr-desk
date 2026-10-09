package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/sys/unix"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

// Config is the file $XDG_CONFIG_HOME/herdr-desk/config.toml.
type Config struct {
	Client      Client      `toml:"client"`
	Runner      Runner      `toml:"runner"`
	Roots       []Root      `toml:"roots"`
	Agent       Agent       `toml:"agent"`
	Coordinator Coordinator `toml:"coordinator"`
	Notify      Notify      `toml:"notify"`
	SecretScan  SecretScan  `toml:"secret_scan"`
	Backup      Backup      `toml:"backup"`
}

// Client is set on client machines only. Home is an ssh target; Command is the argv template that carries one
// request to the home, with {home} expanded to Home and {control} to Paths.ControlPath(Home), the ssh control socket
// <state folder>/ssh-<8 hex>.
type Client struct {
	Home    string   `toml:"home"`
	Command []string `toml:"command"` // empty → DefaultClientCommand()
}

// DefaultClientCommand is the [client] command a config that sets none uses: ssh in batch mode with a 5-second
// connect timeout, reusing one connection through a control socket for a minute.
func DefaultClientCommand() []string {
	return []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "ControlMaster=auto", "-o", "ControlPath={control}", "-o", "ControlPersist=60", "{home}", "herdr-desk", "rpc"}
}

// Runner: Enabled is shown by the board; OnMerged is read by the store. The two limits are written by setup and
// read by the runner.
type Runner struct {
	Enabled       bool   `toml:"enabled"`
	Cap           int    `toml:"cap"`
	MaxRunsPerDay int    `toml:"max_runs_per_day"`
	OnMerged      string `toml:"on_merged"` // "review" | "done"
	// Deprecated: the removed run deadline's key, read so an old config still loads; Load clears it, so a save drops it.
	MaxRunMinutes int `toml:"max_run_minutes,omitempty"`
}

// Root: written by `herdr-desk roots` or by hand; read by run start's route. FirstMessage is the last tier of a run's
// first_message, after run start's flag and the task's own field: when a run has one, it is the worker's {message},
// with {task_file} replaced by the path of a file that holds the first message the worker would otherwise get.
// AgentsMayStart lets agent sessions other than the coordinator start runs in this root; `roots add` lets only a
// person set it.
type Root struct {
	Path           string `toml:"path"`
	About          string `toml:"about"`
	Isolation      string `toml:"isolation"` // "" | self | worktree | in-place
	FirstMessage   string `toml:"first_message,omitempty"`
	AgentsMayStart bool   `toml:"agents_may_start,omitempty"`
}

// TaskFileMessage is a first_message template with {task_file} replaced by path, each placeholder in one pass.
func TaskFileMessage(template, path string) string {
	return Expand([]string{template}, map[string]string{model.TaskFile: path})[0]
}

// Agent: Worker, Coordinator, and Models are written by a profile and read by the runner. SessionEnv is read by
// the CLI.
type Agent struct {
	Worker      []string `toml:"worker"`
	Coordinator []string `toml:"coordinator"`
	SessionEnv  string   `toml:"session_env"`
	Models      []string `toml:"models"` // the models a run may use; the first is the default; the worker's {model}
}

// The values of [coordinator] start_runs.
const (
	StartRunsPropose = "propose"
	StartRunsAuto    = "auto"
)

// Coordinator: StartRuns is "propose" (the coordinator proposes runs and waits for a go-ahead) or "auto" (it starts
// them unasked, and an agent may set a task ready). Another agent may start a run only in a root with AgentsMayStart.
type Coordinator struct {
	StartRuns string `toml:"start_runs" comment:"WARNING: auto lets the coordinator start runs unasked and agents set tasks ready, which spends your quota; another agent may start a run only in a root with agents_may_start = true"`
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

// Default is the config of a fresh herdr-desk: runner off, cap 1, 20 runs a day, on_merged "review", start_runs
// "propose".
func Default() Config {
	return Config{
		Runner: Runner{
			Cap:           1,
			MaxRunsPerDay: 20,
			OnMerged:      "review",
		},
		Coordinator: Coordinator{StartRuns: StartRunsPropose},
	}
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
		var missing *toml.StrictMissingError
		if errors.As(err, &missing) {
			var keys []string
			for _, e := range missing.Errors {
				keys = append(keys, strings.Join(e.Key(), "."))
			}
			return Config{}, fmt.Errorf("%s: unknown key %s", path, strings.Join(keys, ", "))
		}
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	c.Runner.MaxRunMinutes = 0
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save writes the config to path, 0600, through a temp file and a rename. The comment above
// start_runs is part of the struct, so every save keeps it. A file that already holds these bytes at 0600 is
// left alone: there is nothing to write.
func (c Config) Save(path string) error {
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm() == 0o600 {
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
			return nil
		}
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

// ErrLocked is TryLock finding the lock held by another open file description.
var ErrLocked = errors.New("locked")

// TryLock takes an exclusive flock on path without waiting, creating the file 0600 and its folder 0700. It returns
// ErrLocked when another holder has it. The lock lasts until unlock or the process ends.
func TryLock(path string) (unlock func() error, err error) {
	return lock(path, unix.LOCK_EX|unix.LOCK_NB)
}

// Lock is TryLock that waits for the lock.
func Lock(path string) (unlock func() error, err error) {
	return lock(path, unix.LOCK_EX)
}

func lock(path string, how int) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() error { return errors.Join(unix.Flock(int(f.Fd()), unix.LOCK_UN), f.Close()) }, nil
}

// LockHeld reports whether another holder has the lock on path; a file that does not exist is not held. It
// creates nothing.
func LockHeld(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
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

// Validate checks on_merged, start_runs, that the runner's two limits are at least 1, each root's isolation, that a
// root's first_message is empty or holds {task_file}, and that client.home can be an ssh target: it is one argv
// element of the [client] command, so it may not start with "-", hold whitespace or a control character, or be over
// 255 bytes. An empty on_merged reads as "review", an empty start_runs as "propose".
func (c Config) Validate() error {
	if _, ok := model.OnMergedStatus(c.Runner.OnMerged); !ok {
		return fmt.Errorf("runner.on_merged must be \"review\" or \"done\", not %q", c.Runner.OnMerged)
	}
	switch c.Coordinator.StartRuns {
	case "", StartRunsPropose, StartRunsAuto:
	default:
		return fmt.Errorf("coordinator.start_runs must be %q or %q, not %q", StartRunsPropose, StartRunsAuto, c.Coordinator.StartRuns)
	}
	for _, l := range []struct {
		key string
		n   int
	}{
		{"runner.cap", c.Runner.Cap},
		{"runner.max_runs_per_day", c.Runner.MaxRunsPerDay},
	} {
		if l.n < 1 {
			return fmt.Errorf("%s must be at least 1, not %d", l.key, l.n)
		}
	}
	for _, r := range c.Roots {
		if !filepath.IsAbs(r.Path) {
			return fmt.Errorf("roots: root path %q is not absolute", r.Path)
		}
		if !model.ValidIsolation(r.Isolation) {
			return fmt.Errorf("roots %s: isolation must be self, worktree, or in-place, not %q", r.Path, r.Isolation)
		}
		if !model.ValidFirstMessage(r.FirstMessage) {
			return fmt.Errorf("roots %s: first_message must hold {%s}, the path of the task's file, or the worker gets no task", r.Path, model.TaskFile)
		}
	}
	if err := checkSSHTarget(c.Client.Home); err != nil {
		return fmt.Errorf("client.home: %w", err)
	}
	return nil
}

// checkSSHTarget accepts "" (a home) and any target ssh can take as one argument that is not an option.
func checkSSHTarget(home string) error {
	switch {
	case strings.HasPrefix(home, "-"):
		return fmt.Errorf("%q starts with \"-\", which ssh reads as an option", home)
	case len(home) > 255:
		return fmt.Errorf("%q is over 255 bytes", home)
	case strings.IndexFunc(home, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		return fmt.Errorf("%q holds whitespace or a control character", home)
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
	if !model.ValidIsolation(r.Isolation) {
		return fmt.Errorf("isolation must be self, worktree, or in-place, not %q", r.Isolation)
	}
	if !model.ValidFirstMessage(r.FirstMessage) {
		return fmt.Errorf("first_message must hold {%s}, the path of the task's file, not %q", model.TaskFile, r.FirstMessage)
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

// Expand substitutes {name} in each element of an argv template with vars[name], each element in one pass: a
// substituted value is never scanned again. A placeholder vars does not hold is left as written.
func Expand(template []string, vars map[string]string) []string {
	out := make([]string, len(template))
	for i, elem := range template {
		var b strings.Builder
		for rest := elem; rest != ""; {
			open := strings.IndexByte(rest, '{')
			if open < 0 {
				b.WriteString(rest)
				break
			}
			b.WriteString(rest[:open])
			rest = rest[open:]
			if end := strings.IndexByte(rest, '}'); end > 0 {
				if v, ok := vars[rest[1:end]]; ok {
					b.WriteString(v)
					rest = rest[end+1:]
					continue
				}
			}
			b.WriteByte('{')
			rest = rest[1:]
		}
		out[i] = b.String()
	}
	return out
}
