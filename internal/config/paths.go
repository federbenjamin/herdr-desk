// Package config owns herdr-desk's files on disk: where they live, the config file, and the locks.
package config

import "path/filepath"

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

// ControlPath is StateDir/ssh-%C: the ssh control socket of the default [client] command. ssh expands %C to a hash
// of the connection, so the path stays short.
func (p Paths) ControlPath() string { return filepath.Join(p.StateDir, "ssh-%C") }

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

// RouterSystemFile is StateDir/router-system.md: where the built-in system prompt is written for the router to read.
func (p Paths) RouterSystemFile() string { return filepath.Join(p.StateDir, "router-system.md") }

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
