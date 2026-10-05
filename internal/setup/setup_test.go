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
	if !got.Runner.Enabled || got.Runner.Cap != 7 || !reflect.DeepEqual(got.SecretScan.Command, []string{"scan-existing"}) {
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
	for _, line := range []string{"agent.worker: kept", "agent.session_env: kept", "skill: " + skillFile + " unchanged"} {
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

func TestRunAddsHerdrNotificationOnlyWhenHerdrConfigExists(t *testing.T) {
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
