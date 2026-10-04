package config_test

import (
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/config"
)

func TestResolvePathsUsesEachXDGVariableAndConstructsNamedFiles(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"XDG_CONFIG_HOME": "/cfg",
		"XDG_STATE_HOME":  "/state",
		"XDG_DATA_HOME":   "/data",
		"XDG_CACHE_HOME":  "/cache",
	}
	p := config.ResolvePaths(func(name string) string { return env[name] })
	if p.ConfigDir != "/cfg/desk" || p.StateDir != "/state/desk" || p.DataDir != "/data/desk" || p.CacheDir != "/cache/desk" {
		t.Fatalf("ResolvePaths(XDG) = %#v", p)
	}

	files := map[string]struct{ got, want string }{
		"ConfigFile":  {p.ConfigFile(), "/cfg/desk/config.toml"},
		"TokenFile":   {p.TokenFile(), "/cfg/desk/token"},
		"Socket":      {p.Socket(), "/state/desk/desk.sock"},
		"LockFile":    {p.LockFile(), "/state/desk/daemon.lock"},
		"DaemonInfo":  {p.DaemonInfo(), "/state/desk/daemon.json"},
		"DaemonLog":   {p.DaemonLog(), "/state/desk/daemon.log"},
		"Outbox":      {p.Outbox(), "/state/desk/outbox.jsonl"},
		"SessionsDir": {p.SessionsDir(), "/state/desk/sessions"},
		"BackupState": {p.BackupState(), "/state/desk/backup.json"},
		"DB":          {p.DB(), "/data/desk/desk.db"},
		"ScratchRoot": {p.ScratchRoot(), "/data/desk/scratch"},
		"BackupDir":   {p.BackupDir(), "/data/desk/backup"},
		"Snapshot":    {p.Snapshot(), "/cache/desk/snapshot.json"},
	}
	for name, path := range files {
		if path.got != path.want {
			t.Errorf("%s() = %q; want %q", name, path.got, path.want)
		}
	}
}

func TestResolvePathsFallsBackToHomeWhenXDGVariablesAreAbsent(t *testing.T) {
	t.Parallel()

	p := config.ResolvePaths(func(name string) string {
		if name == "HOME" {
			return "/home/desk"
		}
		return ""
	})
	want := config.Paths{
		ConfigDir: "/home/desk/.config/desk",
		StateDir:  "/home/desk/.local/state/desk",
		DataDir:   "/home/desk/.local/share/desk",
		CacheDir:  "/home/desk/.cache/desk",
	}
	if p != want {
		t.Errorf("ResolvePaths(HOME fallback) = %#v; want %#v", p, want)
	}
}

func TestDefaultStartsRunnerWithPublishedLimits(t *testing.T) {
	t.Parallel()

	c := config.Default()
	if c.Runner.Enabled || c.Runner.Cap != 1 || c.Runner.MaxRunsPerDay != 20 || c.Runner.MaxRunMinutes != 180 || c.Runner.PollSeconds != 30 || c.Runner.OnMerged != "review" {
		t.Errorf("Default().Runner = %#v; want disabled runner with documented limits", c.Runner)
	}
}

func TestLoadMissingFileReturnsDefaultAndRejectsUnknownOrInvalidConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.toml")
	got, err := config.Load(missing)
	if err != nil {
		t.Fatalf("Load(missing) error = %v", err)
	}
	if !reflect.DeepEqual(got, config.Default()) {
		t.Errorf("Load(missing) = %#v; want Default() %#v", got, config.Default())
	}

	for name, contents := range map[string]string{
		"unknown key":   "[runner]\nunknown = true\n",
		"invalid value": "[runner]\non_merged = \"later\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(path); err == nil {
				t.Fatal("Load(invalid config) error = nil; want error")
			}
		})
	}

	valid := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(valid, []byte("[runner]\ncap = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(valid)
	if err != nil {
		t.Fatalf("Load(valid config) error = %v", err)
	}
	if loaded.Runner.Cap != 3 || loaded.Runner.OnMerged != "review" {
		t.Errorf("Load(valid partial config).Runner = %#v; want cap 3 and default on_merged review", loaded.Runner)
	}

	if _, err := config.Load(t.TempDir()); err == nil {
		t.Error("Load(directory) error = nil; want filesystem error")
	}
}

func TestSaveReplacesExistingFileSecurelyAndKeepsWarningWithItsField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldLink := filepath.Join(filepath.Dir(path), "old-config-link")
	if err := os.Link(path, oldLink); err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Runner.AgentsMayArm = true
	if err := c.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("saved file permissions = %04o; want 0600", got)
	}
	linkedText, err := os.ReadFile(oldLink)
	if err != nil {
		t.Fatal(err)
	}
	if string(linkedText) != "old" {
		t.Errorf("Save changed an existing hard link to %q; want rename to leave it as old", linkedText)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	warning := strings.Index(string(text), "WARNING:")
	field := strings.Index(string(text), "agents_may_arm")
	if warning < 0 || field < 0 || warning > field {
		t.Fatalf("saved config does not put WARNING before agents_may_arm:\n%s", text)
	}
	between := string(text[warning:field])
	if strings.Count(between, "\n") != 1 {
		t.Errorf("WARNING is not directly above agents_may_arm: %q", between)
	}

	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(filepath.Join(blocked, "config.toml")); err == nil {
		t.Error("Save(path below a regular file) error = nil; want filesystem error")
	}
}

func TestSaveLeavesAnUnchangedFileAndItsMtimeAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := config.Default()
	if err := c.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(path); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("Save of the same config moved the mtime to %s; want %s", info.ModTime(), old)
	}

	c.Runner.Cap = 2
	if err := c.Save(path); err != nil {
		t.Fatalf("changed Save() error = %v", err)
	}
	if info, err = os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().After(old) {
		t.Error("Save of a changed config left the old mtime; want the file rewritten")
	}
}

func TestValidateRefusesWildcardListenAndInvalidEnumValues(t *testing.T) {
	for _, tt := range []struct {
		name   string
		adjust func(*config.Config)
	}{
		{"empty listen host", func(c *config.Config) { c.Home.Listen = ":8080" }},
		{"IPv4 wildcard listen host", func(c *config.Config) { c.Home.Listen = "0.0.0.0:8080" }},
		{"IPv6 wildcard listen host", func(c *config.Config) { c.Home.Listen = "[::]:8080" }},
		{"bad on merged value", func(c *config.Config) { c.Runner.OnMerged = "start" }},
		{"bad root isolation", func(c *config.Config) { c.Roots = []config.Root{{Path: "/repo", Isolation: "container"}} }},
		{"bad client home", func(c *config.Config) { c.Client.Home = "not-a-host-port" }},
		{"bad port", func(c *config.Config) { c.Client.Home = "localhost:0" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := config.Default()
			tt.adjust(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("Validate() error = nil; want refusal")
			}
		})
	}

	c := config.Default()
	c.Home.Listen = "127.0.0.1:8080"
	c.Client.Home = "localhost:8081"
	c.Runner.OnMerged = "done"
	c.Roots = []config.Root{{Path: "/repo", Isolation: "worktree"}, {Path: "/another", Isolation: "in-place"}}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate(valid config) error = %v", err)
	}
}

func TestIsClientFollowsWhetherAHomeIsConfigured(t *testing.T) {
	t.Parallel()

	if config.Default().IsClient() {
		t.Error("Default().IsClient() = true; want false")
	}
	c := config.Default()
	c.Client.Home = "127.0.0.1:8080"
	if !c.IsClient() {
		t.Error("Config with Client.Home.IsClient() = false; want true")
	}
}

func TestAddRootReplacesByPathAndRequiresAnAbsolutePath(t *testing.T) {
	c := config.Default()
	if err := c.AddRoot(config.Root{Path: "relative", About: "bad"}); err == nil {
		t.Fatal("AddRoot(relative path) error = nil; want error")
	}
	if err := c.AddRoot(config.Root{Path: "/repo", About: "old", Isolation: "self"}); err != nil {
		t.Fatalf("AddRoot(first root) error = %v", err)
	}
	if err := c.AddRoot(config.Root{Path: "/invalid", Isolation: "container"}); err == nil {
		t.Fatal("AddRoot(invalid isolation) error = nil; want error")
	}
	if err := c.AddRoot(config.Root{Path: "/repo", About: "replacement", Isolation: "worktree"}); err != nil {
		t.Fatalf("AddRoot(replacement) error = %v", err)
	}
	if len(c.Roots) != 1 || c.Roots[0].About != "replacement" || c.Roots[0].Isolation != "worktree" {
		t.Errorf("AddRoot(replacement) left roots %#v; want one replacement", c.Roots)
	}
}

func TestRemoveRootRemovesOnlyKnownPath(t *testing.T) {
	c := config.Default()
	c.Roots = []config.Root{{Path: "/one"}, {Path: "/two"}}
	if err := c.RemoveRoot("/one"); err != nil {
		t.Fatalf("RemoveRoot(known path) error = %v", err)
	}
	if len(c.Roots) != 1 || c.Roots[0].Path != "/two" {
		t.Errorf("RemoveRoot(/one) left roots %#v; want only /two", c.Roots)
	}
	if err := c.RemoveRoot("/missing"); err == nil {
		t.Fatal("RemoveRoot(missing path) error = nil; want error")
	}
}

func TestTokenOperationsKeepSingleTokenFilePrivate(t *testing.T) {
	paths := config.Paths{ConfigDir: t.TempDir()}
	if _, err := config.ReadToken(paths); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadToken(missing) error = %v; want wrapped fs.ErrNotExist", err)
	}
	if err := os.WriteFile(paths.TokenFile(), []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.ReadToken(paths); err == nil {
		t.Fatal("ReadToken(empty) error = nil; want error")
	}
	if err := config.WriteToken(paths, "not a token"); err == nil {
		t.Fatal("WriteToken(with whitespace) error = nil; want error")
	}

	if err := config.WriteToken(paths, "known-token"); err != nil {
		t.Fatalf("WriteToken() error = %v", err)
	}
	if got, err := config.ReadToken(paths); err != nil || got != "known-token" {
		t.Fatalf("ReadToken() = %q, %v; want known-token, nil", got, err)
	}
	info, err := os.Stat(paths.TokenFile())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("token permissions after WriteToken = %04o; want 0600", got)
	}

	rotated, err := config.RotateToken(paths)
	if err != nil {
		t.Fatalf("RotateToken() error = %v", err)
	}
	if len(rotated) != 64 || rotated == "known-token" {
		t.Errorf("RotateToken() = %q; want a new 32-byte hex token", rotated)
	}
	if _, err := hex.DecodeString(rotated); err != nil {
		t.Errorf("RotateToken() = %q; want hexadecimal: %v", rotated, err)
	}
	stored, err := config.ReadToken(paths)
	if err != nil || stored != rotated {
		t.Errorf("ReadToken after rotation = %q, %v; want returned token", stored, err)
	}
	info, err = os.Stat(paths.TokenFile())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("token permissions after RotateToken = %04o; want 0600", got)
	}
}

func TestConfigChangedSinceComparesTheFileTimeAndTreatsAMissingFileAsUnchanged(t *testing.T) {
	t.Parallel()

	p := config.Paths{ConfigDir: filepath.Join(t.TempDir(), "desk")}
	started := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	if p.ConfigChangedSince(started) {
		t.Error("ConfigChangedSince(no file) = true, want false")
	}
	if err := config.Default().Save(p.ConfigFile()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		mtime time.Time
		want  bool
	}{
		{"written after the start", started.Add(time.Second), true},
		{"written before the start", started.Add(-time.Second), false},
		{"written at the start", started, false},
	} {
		if err := os.Chtimes(p.ConfigFile(), test.mtime, test.mtime); err != nil {
			t.Fatal(err)
		}
		if got := p.ConfigChangedSince(started); got != test.want {
			t.Errorf("ConfigChangedSince(%s) = %t, want %t", test.name, got, test.want)
		}
	}
}
