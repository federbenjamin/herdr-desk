package setup_test

import (
	"bytes"
	"context"
	"encoding/hex"
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
		Listen: "127.0.0.1:7411",
		Out:    &out,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatalf("load configured home: %v", err)
	}
	if got.Home.Listen != "127.0.0.1:7411" {
		t.Errorf("home listen = %q; want setup listen", got.Home.Listen)
	}
	if !got.Runner.Enabled || got.Runner.Cap != 7 || !reflect.DeepEqual(got.SecretScan.Command, []string{"scan-existing"}) {
		t.Errorf("Run lost existing config values: %#v", got)
	}

	token, err := config.ReadToken(p)
	if err != nil {
		t.Fatalf("read setup token: %v", err)
	}
	if len(token) != 64 {
		t.Errorf("token length = %d; want 64 hexadecimal characters", len(token))
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Errorf("token is not hexadecimal: %v", err)
	}
	info, err := os.Stat(p.TokenFile())
	if err != nil {
		t.Fatalf("stat setup token: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("token mode = %04o; want 0600", got)
	}
	if _, err := os.Stat(filepath.Join(p.ScratchRoot(), ".git")); err != nil {
		t.Errorf("scratch root is not a git repository: %v", err)
	}
	if lines := strings.Count(strings.TrimSpace(out.String()), "\n") + 1; lines < 3 {
		t.Errorf("Run report has %d lines; want a line for each written or skipped item:\n%s", lines, out.String())
	}
	out.Reset()
	if err := setup.Run(context.Background(), setup.Options{Paths: p, Getenv: getenv, Listen: "127.0.0.1:7411", Out: &out}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	for _, line := range []string{"token: " + p.TokenFile() + " kept", "scratch root: " + p.ScratchRoot() + " kept"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("second Run report = %q; want %q", out.String(), line)
		}
	}

	invalidPaths, invalidGetenv := setupPaths(t)
	if err := setup.Run(context.Background(), setup.Options{Paths: invalidPaths, Getenv: invalidGetenv, Listen: "0.0.0.0:7411"}); err == nil {
		t.Fatal("Run(wildcard listen) error = nil; want validation failure")
	}
	for _, file := range []string{invalidPaths.ConfigFile(), invalidPaths.TokenFile()} {
		if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Run(wildcard listen) wrote %q: stat error = %v; want not exist", file, err)
		}
	}
}

func TestRunAppliesClaudeCodeProfileWritesSkillAndPreservesExistingToken(t *testing.T) {
	p, getenv := setupPaths(t)
	if err := config.WriteToken(p, "already-issued-token"); err != nil {
		t.Fatalf("write existing token: %v", err)
	}
	skillDir := filepath.Join(t.TempDir(), "skills")
	if err := setup.Run(context.Background(), setup.Options{
		Paths:    p,
		Getenv:   getenv,
		Profile:  "claude-code",
		Listen:   "127.0.0.1:7411",
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
	wantRouter := []string{"claude", "-p", "--safe-mode", "--tools", "", "--system-prompt-file", "{system}", "--json-schema", "{schema}", "--max-budget-usd", "0.10", "--no-session-persistence", "--output-format", "json"}
	wantWorker := []string{"claude", "--model", "{model}", "--permission-mode", "auto", "--session-id", "{session}", "--", "{message}"}
	if !reflect.DeepEqual(got.Agent.Router, wantRouter) {
		t.Errorf("router argv = %#v; want %#v", got.Agent.Router, wantRouter)
	}
	if !reflect.DeepEqual(got.Agent.Worker, wantWorker) {
		t.Errorf("worker argv = %#v; want %#v", got.Agent.Worker, wantWorker)
	}
	if got.Agent.SessionEnv != "CLAUDE_CODE_SESSION_ID" {
		t.Errorf("session env = %q; want CLAUDE_CODE_SESSION_ID", got.Agent.SessionEnv)
	}
	if token, err := config.ReadToken(p); err != nil || token != "already-issued-token" {
		t.Errorf("Run changed existing token to %q, %v", token, err)
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
		Listen:   "127.0.0.1:7411",
		SkillDir: skillDir,
		NoHerdr:  true,
		Out:      &secondOut,
	}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	for _, line := range []string{"agent.router: kept", "agent.worker: kept", "agent.session_env: kept", "skill: " + skillFile + " unchanged"} {
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
	if backups, err := filepath.Glob(filepath.Join(filepath.Dir(p.ConfigDir), "herdr", "config.toml.desk-bak-*")); err != nil || len(backups) != 0 {
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
	backups, err := filepath.Glob(herdrConfig + ".desk-bak-*")
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

func TestClientAddWritesOnlyAfterTheHomeAcceptsItsToken(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.NewMachine(t)

	if err := setup.ClientAdd(context.Background(), client.Paths, home.Addr, home.Token); err != nil {
		t.Fatalf("ClientAdd(valid home) error = %v", err)
	}
	got, err := config.Load(client.Paths.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if got.Client.Home != home.Addr {
		t.Errorf("client home = %q; want %q", got.Client.Home, home.Addr)
	}
	if token, err := config.ReadToken(client.Paths); err != nil || token != home.Token {
		t.Errorf("stored token = %q, %v; want home token", token, err)
	}

	badClient := testutil.NewMachine(t)
	if err := setup.ClientAdd(context.Background(), badClient.Paths, home.Addr, "wrong-token"); err == nil {
		t.Fatal("ClientAdd(wrong token) error = nil; want refusal")
	}
	for _, file := range []string{badClient.Paths.ConfigFile(), badClient.Paths.TokenFile()} {
		if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ClientAdd(wrong token) wrote %q: stat error = %v; want not exist", file, err)
		}
	}
}

func TestClientAddChecksTheHomeWithoutWritingATemporaryToken(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{Listen: true})
	client := testutil.NewMachine(t)
	tmp := t.TempDir()
	if err := os.Chmod(tmp, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(tmp, 0o700) })
	t.Setenv("TMPDIR", tmp)

	if err := setup.ClientAdd(context.Background(), client.Paths, home.Addr, home.Token); err != nil {
		t.Fatalf("ClientAdd with a temp folder it cannot write = %v, want the check made with the token in memory", err)
	}
	root := filepath.Dir(filepath.Dir(client.Paths.ConfigDir))
	var files []string
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{client.Paths.ConfigFile(), client.Paths.TokenFile()}; !reflect.DeepEqual(files, want) {
		t.Errorf("ClientAdd wrote %q, want only %q", files, want)
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
