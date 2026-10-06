package setup_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/setup"
	"github.com/federbenjamin/herdr-desk/internal/sidebar"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

// TestMain lets this test binary answer as a home's rpc helper, for client machines.
func TestMain(m *testing.M) {
	testutil.ServeRPCIfAsked()
	os.Exit(m.Run())
}

func TestRunKeepsExistingValuesAndCreatesTheHomePrerequisites(t *testing.T) {
	p, getenv := setupPaths(t)
	existing := config.Default()
	existing.Runner.Enabled = true
	existing.Runner.Cap = 7
	existing.SecretScan.Command = []string{"scan-existing"}
	existing.Roots = []config.Root{{Path: t.TempDir(), About: "app", Isolation: "self", FirstMessage: "/build {task_file}"}}
	if err := existing.Save(p.ConfigFile()); err != nil {
		t.Fatalf("save existing config: %v", err)
	}

	var out bytes.Buffer
	if err := setup.Run(context.Background(), setup.Options{
		Paths:  p,
		Getenv: getenv,
		Out:    &out,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatalf("load configured home: %v", err)
	}
	if !got.Runner.Enabled || got.Runner.Cap != 7 || !reflect.DeepEqual(got.SecretScan.Command, []string{"scan-existing"}) || !reflect.DeepEqual(got.Roots, existing.Roots) {
		t.Errorf("Run lost existing config values: %#v", got)
	}
	info, err := os.Stat(p.ConfigFile())
	if err != nil {
		t.Fatalf("stat setup config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("config mode = %04o; want 0600", got)
	}
	if _, err := os.Stat(filepath.Join(p.ScratchRoot(), ".git")); err != nil {
		t.Errorf("scratch root is not a git repository: %v", err)
	}
	if lines := strings.Count(strings.TrimSpace(out.String()), "\n") + 1; lines < 3 {
		t.Errorf("Run report has %d lines; want a line for each written or skipped item:\n%s", lines, out.String())
	}
	out.Reset()
	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, Out: &out}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	for _, line := range []string{"config: " + p.ConfigFile() + " unchanged", "scratch root: " + p.ScratchRoot() + " kept"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("second Run report = %q; want %q", out.String(), line)
		}
	}

	invalidPaths, invalidGetenv := setupPaths(t)
	if err := setup.Run(context.Background(), setup.Options{Paths: invalidPaths, Getenv: invalidGetenv, Profile: "no-such-profile"}); err == nil {
		t.Fatal("Run(unknown profile) error = nil; want validation failure")
	}
	if _, err := os.Stat(invalidPaths.ConfigFile()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Run(unknown profile) wrote %q: stat error = %v; want not exist", invalidPaths.ConfigFile(), err)
	}
}

func TestRunAppliesClaudeCodeProfileAndWritesSkill(t *testing.T) {
	p, getenv := setupPaths(t)
	skillDir := filepath.Join(t.TempDir(), "skills")
	if err := setup.Run(context.Background(), setup.Options{
		Paths:    p,
		Getenv:   getenv,
		Profile:  "claude-code",
		SkillDir: skillDir,
		NoHerdr:  true,
		Out:      new(bytes.Buffer),
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatalf("load profile config: %v", err)
	}
	wantWorker := []string{"claude", "--model", "{model}", "--permission-mode", "auto", "--session-id", "{session}", "--", "{message}"}
	if !reflect.DeepEqual(got.Agent.Worker, wantWorker) {
		t.Errorf("worker argv = %#v; want %#v", got.Agent.Worker, wantWorker)
	}
	wantCoordinator := []string{"claude", "--permission-mode", "auto", "--session-id", "{session}", "--append-system-prompt", "{prompt}"}
	if !reflect.DeepEqual(got.Agent.Coordinator, wantCoordinator) {
		t.Errorf("coordinator argv = %#v; want %#v", got.Agent.Coordinator, wantCoordinator)
	}
	if got.Agent.SessionEnv != "CLAUDE_CODE_SESSION_ID" {
		t.Errorf("session env = %q; want CLAUDE_CODE_SESSION_ID", got.Agent.SessionEnv)
	}
	skillFile := filepath.Join(skillDir, "herdr-desk", "SKILL.md")
	writtenSkill, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("read written skill: %v", err)
	}
	if string(writtenSkill) != herdrdesk.Skill() {
		t.Error("setup skill file differs from herdrdesk.Skill()")
	}
	var secondOut bytes.Buffer
	if err := setup.Run(context.Background(), setup.Options{
		Paths:    p,
		Getenv:   getenv,
		Profile:  "claude-code",
		SkillDir: skillDir,
		NoHerdr:  true,
		Out:      &secondOut,
	}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if strings.Contains(secondOut.String(), "agent.router") {
		t.Errorf("second Run report = %q; want no agent.router line: the router is gone", secondOut.String())
	}
	for _, line := range []string{"agent.worker: kept", "agent.coordinator: kept", "agent.session_env: kept", "skill: " + skillFile + " unchanged"} {
		if !strings.Contains(secondOut.String(), line) {
			t.Errorf("second Run report = %q; want %q", secondOut.String(), line)
		}
	}
}

func TestRunFindsHerdrConfigThroughHomeFallback(t *testing.T) {
	p, _ := setupPaths(t)
	home := t.TempDir()
	herdrConfig := filepath.Join(home, ".config", "herdr", "config.toml")
	if err := os.MkdirAll(filepath.Dir(herdrConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(herdrConfig, []byte("onboarding = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := true
	if err := setup.Run(context.Background(), setup.Options{
		Paths: p,
		Getenv: func(name string) string {
			if name == "HOME" {
				return home
			}
			return ""
		},
		Runner:  &runner,
		NoHerdr: false,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Runner.Enabled {
		t.Error("Run() did not apply Runner=true")
	}
	if !reflect.DeepEqual(got.Notify.Command, []string{"herdr", "notification", "show", "{title}", "--body", "{body}"}) {
		t.Errorf("notification argv = %#v; want herdr notification command", got.Notify.Command)
	}
	changed, err := os.ReadFile(herdrConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(changed), "# >>> herdr-desk keys") {
		t.Errorf("home fallback config did not receive herdr-desk keys:\n%s", changed)
	}
}

func TestRunLeavesNotificationUnsetWhenHerdrIsUnavailable(t *testing.T) {
	p, getenv := setupPaths(t)
	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, Out: new(bytes.Buffer)}); err != nil {
		t.Fatalf("Run(without herdr config) error = %v", err)
	}
	withoutHerdr, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutHerdr.Notify.Command) != 0 {
		t.Errorf("Run without a herdr config set notification argv to %#v", withoutHerdr.Notify.Command)
	}
	if backups, err := filepath.Glob(filepath.Join(filepath.Dir(p.ConfigDir), "herdr", "config.toml.herdr-desk-bak-*")); err != nil || len(backups) != 0 {
		t.Errorf("Run without a herdr config made backups %#v, %v", backups, err)
	}

	herdrConfig := filepath.Join(filepath.Dir(p.ConfigDir), "herdr", "config.toml")
	if err := os.MkdirAll(filepath.Dir(herdrConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(herdrConfig, []byte("onboarding = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, Out: new(bytes.Buffer)}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"herdr", "notification", "show", "{title}", "--body", "{body}"}
	if !reflect.DeepEqual(got.Notify.Command, want) {
		t.Errorf("notification argv = %#v; want %#v", got.Notify.Command, want)
	}
	backups, err := filepath.Glob(herdrConfig + ".herdr-desk-bak-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("herdr config backups = %#v, %v; want one", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "onboarding = false\n" {
		t.Errorf("backup = %q; want original herdr config", backup)
	}
	changedHerdr, err := os.ReadFile(herdrConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(changedHerdr), "# >>> herdr-desk keys") {
		t.Errorf("Run did not add the herdr-desk key block to herdr config:\n%s", changedHerdr)
	}

	got.Notify.Command = []string{"keep-notify"}
	if err := got.Save(p.ConfigFile()); err != nil {
		t.Fatal(err)
	}
	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, Out: new(bytes.Buffer)}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	again, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Notify.Command, []string{"keep-notify"}) {
		t.Errorf("Run replaced existing notification argv with %#v", again.Notify.Command)
	}
	if err := os.WriteFile(herdrConfig, []byte(`[[keys.command]]
key = "prefix+t"
type = "plugin_action"
command = "other.open"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	var conflictOut bytes.Buffer
	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, Out: &conflictOut}); err != nil {
		t.Fatalf("Run(with conflicting herdr key) error = %v", err)
	}
	if !strings.Contains(conflictOut.String(), "herdr: prefix+t skipped, another binding holds it") {
		t.Errorf("conflicting-key report = %q; want prefix+t skipped", conflictOut.String())
	}
}

func TestRunUsesHERDRConfigPathForANewHerdrConfig(t *testing.T) {
	p, getenv := setupPaths(t)
	testutil.FakeHerdr(t)
	herdrConfig := filepath.Join(t.TempDir(), "custom", "herdr.toml")

	if err := setup.Run(context.Background(), setup.Options{
		Paths: p,
		Getenv: func(name string) string {
			if name == "HERDR_CONFIG_PATH" {
				return herdrConfig
			}
			return getenv(name)
		},
		Out: new(bytes.Buffer),
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := os.Stat(herdrConfig); err != nil {
		t.Fatalf("Run() did not create HERDR_CONFIG_PATH %q: %v", herdrConfig, err)
	}
	defaultConfig := filepath.Join(getenv("XDG_CONFIG_HOME"), "herdr", "config.toml")
	if _, err := os.Stat(defaultConfig); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Run() wrote default herdr config %q: %v", defaultConfig, err)
	}
}

func TestRunCreatesMissingHerdrConfigWithKeysAndSidebar(t *testing.T) {
	p, getenv := setupPaths(t)
	testutil.FakeHerdr(t)
	herdrConfig := filepath.Join(getenv("XDG_CONFIG_HOME"), "herdr", "config.toml")

	var out bytes.Buffer
	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, Out: &out}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	keys, _, _ := setup.WriteHerdrKeys("", false)
	wantConfig, _ := sidebar.WriteHerdrSidebar(keys)
	gotConfig, err := os.ReadFile(herdrConfig)
	if err != nil {
		t.Fatalf("read created herdr config: %v", err)
	}
	if string(gotConfig) != wantConfig {
		t.Errorf("created herdr config = %q; want %q", gotConfig, wantConfig)
	}
	info, err := os.Stat(herdrConfig)
	if err != nil {
		t.Fatalf("stat created herdr config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("created herdr config mode = %04o; want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(herdrConfig))
	if err != nil {
		t.Fatalf("stat created herdr config folder: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o755 {
		t.Errorf("created herdr config folder mode = %04o; want 0755", got)
	}
	for _, line := range []string{
		"herdr: " + herdrConfig + " created (herdr had no config file; it holds only herdr-desk's keys and sidebar row)",
		"herdr: prefix+t bound",
		"herdr: prefix+a bound",
		"herdr: sidebar row $desk written",
	} {
		if got := strings.Count(out.String(), line); got != 1 {
			t.Errorf("Run() report count for %q = %d; want 1\n%s", line, got, out.String())
		}
	}
	if backups, err := filepath.Glob(herdrConfig + ".herdr-desk-bak-*"); err != nil || len(backups) != 0 {
		t.Errorf("created herdr config backups = %#v, %v; want none", backups, err)
	}
	got, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatalf("load desk config: %v", err)
	}
	wantNotify := []string{"herdr", "notification", "show", "{title}", "--body", "{body}"}
	if !reflect.DeepEqual(got.Notify.Command, wantNotify) {
		t.Errorf("notification argv = %#v; want %#v", got.Notify.Command, wantNotify)
	}
}

func TestRunReportsNoHerdrAndLeavesAMissingConfigAbsent(t *testing.T) {
	p, getenv := setupPaths(t)
	herdrConfig := filepath.Join(getenv("XDG_CONFIG_HOME"), "herdr", "config.toml")
	var out bytes.Buffer

	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, Out: &out}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := os.Stat(herdrConfig); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Run() created a config without herdr: stat error = %v; want not exist", err)
	}
	line := "herdr: no herdr found and no config file; keys and sidebar row not written"
	if got := strings.Count(out.String(), line); got != 1 {
		t.Errorf("Run() report count for %q = %d; want 1\n%s", line, got, out.String())
	}
	got, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatalf("load desk config: %v", err)
	}
	if len(got.Notify.Command) != 0 {
		t.Errorf("notification argv = %#v; want unset without herdr", got.Notify.Command)
	}
}

func TestRunNoHerdrDoesNotCreateAMissingConfigEvenWhenHerdrIsAvailable(t *testing.T) {
	p, getenv := setupPaths(t)
	testutil.FakeHerdr(t)
	herdrConfig := filepath.Join(getenv("XDG_CONFIG_HOME"), "herdr", "config.toml")
	var out bytes.Buffer

	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, NoHerdr: true, Out: &out}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := os.Stat(herdrConfig); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Run(NoHerdr) created a config: stat error = %v; want not exist", err)
	}
	line := "herdr: left alone (--no-herdr)"
	if got := strings.Count(out.String(), line); got != 1 {
		t.Errorf("Run(NoHerdr) report count for %q = %d; want 1\n%s", line, got, out.String())
	}
}

func TestRunSecondMissingHerdrConfigSetupLeavesItUnchangedWithoutBackup(t *testing.T) {
	p, getenv := setupPaths(t)
	testutil.FakeHerdr(t)
	herdrConfig := filepath.Join(getenv("XDG_CONFIG_HOME"), "herdr", "config.toml")
	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, Out: new(bytes.Buffer)}); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	before, err := os.ReadFile(herdrConfig)
	if err != nil {
		t.Fatalf("read first created herdr config: %v", err)
	}

	var out bytes.Buffer
	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, Out: &out}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	after, err := os.ReadFile(herdrConfig)
	if err != nil {
		t.Fatalf("read second herdr config: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("second Run() changed created herdr config\nbefore:\n%s\nafter:\n%s", before, after)
	}
	line := "herdr: " + herdrConfig + " unchanged"
	if got := strings.Count(out.String(), line); got != 1 {
		t.Errorf("second Run() report count for %q = %d; want 1\n%s", line, got, out.String())
	}
	if backups, err := filepath.Glob(herdrConfig + ".herdr-desk-bak-*"); err != nil || len(backups) != 0 {
		t.Errorf("second Run() made backups %#v, %v; want none", backups, err)
	}
}

func TestSkillReturnsTheInstalledClaudeCodeContract(t *testing.T) {
	skill := herdrdesk.Skill()
	if !strings.HasPrefix(skill, "---\nname: herdr-desk\n") {
		t.Fatalf("Skill() front matter = %q; want name: herdr-desk", skill)
	}
	for _, rule := range []string{
		"review", "blocked", "never", "ready", "done", "--thread agent", "--json",
		"Never retry blind", "Exit codes", "note --ref", "decide --tag k:v", "session <id> --md",
	} {
		if !strings.Contains(skill, rule) {
			t.Errorf("Skill() does not contain required rule %q", rule)
		}
	}
	profileFile := filepath.Join("..", "..", "profiles", "claude-code", "skills", "herdr-desk", "SKILL.md")
	profileSkill, err := os.ReadFile(profileFile)
	if err != nil {
		t.Fatalf("read %s: %v", profileFile, err)
	}
	if skill != string(profileSkill) {
		t.Error("Skill() differs from profiles/claude-code/skills/herdr-desk/SKILL.md")
	}
}

func setupPaths(t *testing.T) (config.Paths, func(string) string) {
	t.Helper()
	base := t.TempDir()
	xdgConfig := filepath.Join(base, "config")
	p := config.Paths{
		ConfigDir: filepath.Join(xdgConfig, "herdr-desk"),
		StateDir:  filepath.Join(base, "state", "herdr-desk"),
		DataDir:   filepath.Join(base, "data", "herdr-desk"),
		CacheDir:  filepath.Join(base, "cache", "herdr-desk"),
	}
	return p, func(name string) string {
		if name == "XDG_CONFIG_HOME" {
			return xdgConfig
		}
		return ""
	}
}
