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

	"github.com/federbenjamin/herdr-desk/internal/config"
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
	if p.ConfigDir != "/cfg/herdr-desk" || p.StateDir != "/state/herdr-desk" || p.DataDir != "/data/herdr-desk" || p.CacheDir != "/cache/herdr-desk" {
		t.Fatalf("ResolvePaths(XDG) = %#v", p)
	}

	files := map[string]struct{ got, want string }{
		"ConfigFile":  {p.ConfigFile(), "/cfg/herdr-desk/config.toml"},
		"TokenFile":   {p.TokenFile(), "/cfg/herdr-desk/token"},
		"Socket":      {p.Socket(), "/state/herdr-desk/desk.sock"},
		"LockFile":    {p.LockFile(), "/state/herdr-desk/daemon.lock"},
		"DaemonInfo":  {p.DaemonInfo(), "/state/herdr-desk/daemon.json"},
		"DaemonLog":   {p.DaemonLog(), "/state/herdr-desk/daemon.log"},
		"Outbox":      {p.Outbox(), "/state/herdr-desk/outbox.jsonl"},
		"SessionsDir": {p.SessionsDir(), "/state/herdr-desk/sessions"},
		"BackupState": {p.BackupState(), "/state/herdr-desk/backup.json"},
		"DB":          {p.DB(), "/data/herdr-desk/desk.db"},
		"ScratchRoot": {p.ScratchRoot(), "/data/herdr-desk/scratch"},
		"BackupDir":   {p.BackupDir(), "/data/herdr-desk/backup"},
		"Snapshot":    {p.Snapshot(), "/cache/herdr-desk/snapshot.json"},
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
		ConfigDir: "/home/desk/.config/herdr-desk",
		StateDir:  "/home/desk/.local/state/herdr-desk",
		DataDir:   "/home/desk/.local/share/herdr-desk",
		CacheDir:  "/home/desk/.cache/herdr-desk",
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
		"relative root": "[[roots]]\npath = \".\"\nisolation = \"self\"\n",
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

func TestSaveLeavesAnUnchangedFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := config.Default()
	if err := c.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(path); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	second, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(first, second) {
		t.Error("Save of the same config replaced the file; want it left alone")
	}

	c.Runner.Cap = 2
	if err := c.Save(path); err != nil {
		t.Fatalf("changed Save() error = %v", err)
	}
	third, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(second, third) {
		t.Error("Save of a changed config left the old file; want the file rewritten")
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
		{"relative root path", func(c *config.Config) { c.Roots = []config.Root{{Path: "repo", Isolation: "self"}} }},
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

func TestConfigChangedComparesWhatTheFileHoldsWithWhatTheDaemonStartedWith(t *testing.T) {
	t.Parallel()

	started := config.Default()
	started.Backup.GitRemote = "somewhere:desk-backup.git"
	digest := started.Digest()
	longAgo := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, test := range []struct {
		name  string
		write func(t *testing.T, p config.Paths)
		want  bool
	}{
		{"the file the daemon started with", func(t *testing.T, p config.Paths) { save(t, p, started) }, false},
		{"touched", func(t *testing.T, p config.Paths) {
			save(t, p, started)
			if err := os.Chtimes(p.ConfigFile(), longAgo, longAgo); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"rewritten with the same bytes", func(t *testing.T, p config.Paths) {
			save(t, p, started)
			b, err := os.ReadFile(p.ConfigFile())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p.ConfigFile(), b, 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"reformatted to the same config", func(t *testing.T, p config.Paths) {
			write(t, p, "# my notes\n[backup]\ngit_remote = 'somewhere:desk-backup.git'\n")
		}, false},
		{"changed straight after the start", func(t *testing.T, p config.Paths) {
			save(t, p, started)
			changed := started
			changed.Runner.Cap = 2
			save(t, p, changed)
		}, true},
		{"emptied back to the defaults", func(t *testing.T, p config.Paths) { save(t, p, config.Default()) }, true},
		{"removed", func(t *testing.T, p config.Paths) {}, true},
		{"no longer valid", func(t *testing.T, p config.Paths) { write(t, p, "no = [such key") }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := config.Paths{ConfigDir: filepath.Join(t.TempDir(), "herdr-desk")}
			test.write(t, p)
			if got := p.ConfigChanged(digest); got != test.want {
				t.Errorf("ConfigChanged = %t, want %t", got, test.want)
			}
		})
	}

	p := config.Paths{ConfigDir: filepath.Join(t.TempDir(), "herdr-desk")}
	if p.ConfigChanged(config.Default().Digest()) {
		t.Error("ConfigChanged(no file, daemon started on the defaults) = true, want false")
	}
	save(t, p, started)
	if p.ConfigChanged("") {
		t.Error("ConfigChanged(no digest recorded) = true, want false")
	}
}

func save(t *testing.T, p config.Paths, c config.Config) {
	t.Helper()
	if err := c.Save(p.ConfigFile()); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p config.Paths, text string) {
	t.Helper()
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ConfigFile(), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDigestSeparatesConfigsByContent(t *testing.T) {
	t.Parallel()

	a, b := config.Default(), config.Default()
	if a.Digest() == "" || a.Digest() != b.Digest() {
		t.Fatalf("Digest of equal configs = %q and %q, want one non-empty value", a.Digest(), b.Digest())
	}
	b.Home.Listen = "127.0.0.1:7411"
	if a.Digest() == b.Digest() {
		t.Error("Digest of configs with different listen addresses is equal")
	}
}
