package config_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
		"TickerLock":  {p.TickerLock(), "/state/herdr-desk/ticker.lock"},
		"TickerInfo":  {p.TickerInfo(), "/state/herdr-desk/ticker.json"},
		"Log":         {p.Log(), "/state/herdr-desk/herdr-desk.log"},
		"BackupLock":  {p.BackupLock(), "/state/herdr-desk/backup.lock"},
		"Outbox":      {p.Outbox(), "/state/herdr-desk/outbox.jsonl"},
		"SessionsDir": {p.SessionsDir(), "/state/herdr-desk/sessions"},
		"RunMessage":  {p.RunMessage(7), "/state/herdr-desk/runs/run-7.md"},
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

// stateDirOf returns a state folder path exactly n bytes long, ending in /herdr-desk as ResolvePaths makes it.
func stateDirOf(t *testing.T, n int) string {
	t.Helper()
	const tail = "/herdr-desk"
	dir := "/" + strings.Repeat("s", n-1-len(tail)) + tail
	if len(dir) != n {
		t.Fatalf("stateDirOf(%d) is %d bytes", n, len(dir))
	}
	return dir
}

// ssh binds the control socket at ControlPath plus a dot and 16 random characters, and macOS takes 103 bytes.
func TestControlPathLeavesRoomForSSHsBindSuffixUpToA73ByteStateFolder(t *testing.T) {
	t.Parallel()

	sum := sha256.Sum256([]byte("you@home-host"))
	for _, n := range []int{50, 60, 73} {
		dir := stateDirOf(t, n)
		got, err := config.Paths{StateDir: dir}.ControlPath("you@home-host")
		if err != nil {
			t.Fatalf("ControlPath under a %d-byte state folder: %v", n, err)
		}
		if want := dir + "/ssh-" + hex.EncodeToString(sum[:])[:8]; got != want {
			t.Errorf("ControlPath under a %d-byte state folder = %q; want %q", n, got, want)
		}
		if bound := len(got) + 17; bound > 103 {
			t.Errorf("ssh binds %d bytes under a %d-byte state folder; want at most 103", bound, n)
		}
	}
}

func TestControlPathRefusesA74ByteStateFolderNamingTheLengthLimitAndFixes(t *testing.T) {
	t.Parallel()

	_, err := config.Paths{StateDir: stateDirOf(t, 74)}.ControlPath("you@home-host")
	if err == nil {
		t.Fatal("ControlPath under a 74-byte state folder error = nil; want a refusal")
	}
	for _, part := range []string{"104 bytes", "103-byte limit", "XDG_STATE_HOME", "without {control}"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("refusal %q does not name %q", err, part)
		}
	}
}

func TestControlPathGivesEachHomeItsOwnSocket(t *testing.T) {
	t.Parallel()

	p := config.Paths{StateDir: "/state/herdr-desk"}
	one, err1 := p.ControlPath("you@home-one")
	two, err2 := p.ControlPath("you@home-two")
	again, err3 := p.ControlPath("you@home-one")
	if err := errors.Join(err1, err2, err3); err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Errorf("two homes share the control path %q", one)
	}
	if one != again {
		t.Errorf("one home's control path changed: %q then %q", one, again)
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
	if c.Runner.Enabled || c.Runner.Cap != 1 || c.Runner.MaxRunsPerDay != 20 || c.Runner.MaxRunMinutes != 180 || c.Runner.OnMerged != "review" {
		t.Errorf("Default().Runner = %#v; want disabled runner with documented limits", c.Runner)
	}
	if c.Coordinator.StartRuns != config.StartRunsPropose {
		t.Errorf("Default().Coordinator.StartRuns = %q; want propose", c.Coordinator.StartRuns)
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
	c.Coordinator.StartRuns = config.StartRunsAuto
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
	field := strings.Index(string(text), "start_runs")
	if warning < 0 || field < 0 || warning > field {
		t.Fatalf("saved config does not put WARNING before start_runs:\n%s", text)
	}
	between := string(text[warning:field])
	if strings.Count(between, "\n") != 1 {
		t.Errorf("WARNING is not directly above start_runs: %q", between)
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

func TestValidateRefusesAnOptionLikeClientHomeAndInvalidEnumValues(t *testing.T) {
	for _, tt := range []struct {
		name   string
		adjust func(*config.Config)
	}{
		{"client home starting with a dash", func(c *config.Config) { c.Client.Home = "-oProxyCommand=x" }},
		{"client home with a space", func(c *config.Config) { c.Client.Home = "my home" }},
		{"client home with a control character", func(c *config.Config) { c.Client.Home = "home\x07" }},
		{"client home over 255 bytes", func(c *config.Config) { c.Client.Home = strings.Repeat("h", 256) }},
		{"bad on merged value", func(c *config.Config) { c.Runner.OnMerged = "start" }},
		{"bad root isolation", func(c *config.Config) { c.Roots = []config.Root{{Path: "/repo", Isolation: "container"}} }},
		{"relative root path", func(c *config.Config) { c.Roots = []config.Root{{Path: "repo", Isolation: "self"}} }},
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
	c.Client.Home = "me@home.example:2222"
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
	c.Client.Home = "home"
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
