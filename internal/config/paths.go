// Package config owns herdr-desk's files on disk: where they live, the config file, and the locks.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Paths are herdr-desk's four XDG directories; each ends in /herdr-desk.
type Paths struct{ ConfigDir, StateDir, DataDir, CacheDir string }

// ResolvePaths reads XDG_CONFIG_HOME, XDG_STATE_HOME, XDG_DATA_HOME, XDG_CACHE_HOME, falling back to
// HOME/.config, HOME/.local/state, HOME/.local/share, HOME/.cache.
func ResolvePaths(getenv func(string) string) Paths {
	home := getenv("HOME")
	dir := func(env string, fallback ...string) string {
		base := getenv(env)
		if base == "" {
			base = filepath.Join(append([]string{home}, fallback...)...)
		}
		return filepath.Join(base, "herdr-desk")
	}
	return Paths{
		ConfigDir: dir("XDG_CONFIG_HOME", ".config"),
		StateDir:  dir("XDG_STATE_HOME", ".local", "state"),
		DataDir:   dir("XDG_DATA_HOME", ".local", "share"),
		CacheDir:  dir("XDG_CACHE_HOME", ".cache"),
	}
}

// ConfigFile is ConfigDir/config.toml.
func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "config.toml") }

// TickerLock is StateDir/ticker.lock: the ticker holds it while it runs.
func (p Paths) TickerLock() string { return filepath.Join(p.StateDir, "ticker.lock") }

// TickerInfo is StateDir/ticker.json: the running ticker's pid and start time.
func (p Paths) TickerInfo() string { return filepath.Join(p.StateDir, "ticker.json") }

// Log is StateDir/herdr-desk.log.
func (p Paths) Log() string { return filepath.Join(p.StateDir, "herdr-desk.log") }

// OpenLog opens Log for appending, 0600 in a 0700 folder: the one opener of the log that runner.Open, the ticker, and
// the event hook write to.
func (p Paths) OpenLog() (*os.File, error) {
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(p.Log(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
}

// CoordinatorLock is StateDir/coordinator.lock: opening the coordinator holds it, so two opens make one coordinator.
func (p Paths) CoordinatorLock() string { return filepath.Join(p.StateDir, "coordinator.lock") }

// maxSocketPath is the longest unix socket path, in bytes, that macOS binds (104 with the closing NUL); Linux allows
// 107, so a path that fits macOS fits both.
const maxSocketPath = 103

// sshControlSuffix is what ssh adds to a ControlPath while it binds the socket: a dot and 16 random characters.
const sshControlSuffix = 17

// ControlPath is StateDir/ssh-<8 hex>, the hex the start of home's SHA-256: the ssh control socket of the default
// [client] command, one per home. A state folder over 73 bytes leaves no room for ssh's bind suffix; ControlPath then
// refuses with the length, the limit, and the two fixes, since ssh's own error reads as an unreachable home.
func (p Paths) ControlPath(home string) (string, error) {
	sum := sha256.Sum256([]byte(home))
	path := filepath.Join(p.StateDir, "ssh-"+hex.EncodeToString(sum[:4]))
	if n := len(path) + sshControlSuffix; n > maxSocketPath {
		return "", fmt.Errorf("the ssh control socket %s is %d bytes with ssh's %d-byte bind suffix, over the %d-byte limit of a "+
			"unix socket path: set XDG_STATE_HOME to a shorter folder, or set a [client] command without {control}",
			path, n, sshControlSuffix, maxSocketPath)
	}
	return path, nil
}

// BackupLock is StateDir/backup.lock: a backup run holds it.
func (p Paths) BackupLock() string { return filepath.Join(p.StateDir, "backup.lock") }

// Outbox is StateDir/outbox.jsonl.
func (p Paths) Outbox() string { return filepath.Join(p.StateDir, "outbox.jsonl") }

// SessionsDir is StateDir/sessions.
func (p Paths) SessionsDir() string { return filepath.Join(p.StateDir, "sessions") }

// BackupState is StateDir/backup.json (written and read by internal/backup only).
func (p Paths) BackupState() string { return filepath.Join(p.StateDir, "backup.json") }

// DB is DataDir/desk.db.
func (p Paths) DB() string { return filepath.Join(p.DataDir, "desk.db") }

// ScratchRoot is DataDir/scratch.
func (p Paths) ScratchRoot() string { return filepath.Join(p.DataDir, "scratch") }

// BackupDir is DataDir/backup.
func (p Paths) BackupDir() string { return filepath.Join(p.DataDir, "backup") }

// Snapshot is CacheDir/snapshot.json.
func (p Paths) Snapshot() string { return filepath.Join(p.CacheDir, "snapshot.json") }

// RunnerPause is StateDir/runner-paused: the runner is paused while this file exists.
func (p Paths) RunnerPause() string { return filepath.Join(p.StateDir, "runner-paused") }

// Env returns the four XDG variables that resolve to these paths, as KEY=VALUE in the order CONFIG, STATE, DATA,
// CACHE: the inverse of ResolvePaths.
func (p Paths) Env() []string {
	return []string{
		"XDG_CONFIG_HOME=" + filepath.Dir(p.ConfigDir),
		"XDG_STATE_HOME=" + filepath.Dir(p.StateDir),
		"XDG_DATA_HOME=" + filepath.Dir(p.DataDir),
		"XDG_CACHE_HOME=" + filepath.Dir(p.CacheDir),
	}
}
