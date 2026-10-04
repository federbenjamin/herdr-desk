// Package config owns desk's files on disk: where they live, the config file, and the token.
package config

import (
	"os"
	"path/filepath"
	"time"
)

// Paths are desk's four XDG directories; each ends in /desk.
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
		return filepath.Join(base, "desk")
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

// ConfigChangedSince reports whether the config file was written after t. A missing file was not.
func (p Paths) ConfigChangedSince(t time.Time) bool {
	info, err := os.Stat(p.ConfigFile())
	return err == nil && info.ModTime().After(t)
}

// TokenFile is ConfigDir/token.
func (p Paths) TokenFile() string { return filepath.Join(p.ConfigDir, "token") }

// Socket is StateDir/desk.sock.
func (p Paths) Socket() string { return filepath.Join(p.StateDir, "desk.sock") }

// LockFile is StateDir/daemon.lock.
func (p Paths) LockFile() string { return filepath.Join(p.StateDir, "daemon.lock") }

// DaemonInfo is StateDir/daemon.json.
func (p Paths) DaemonInfo() string { return filepath.Join(p.StateDir, "daemon.json") }

// DaemonLog is StateDir/daemon.log.
func (p Paths) DaemonLog() string { return filepath.Join(p.StateDir, "daemon.log") }

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
