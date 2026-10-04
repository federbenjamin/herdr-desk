// Package setup does the three one-time jobs of installing desk: first-time setup, the edit of herdr's keys,
// and joining a home.
package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/federbenjamin/desk"
	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/gitcmd"
	"github.com/federbenjamin/desk/internal/model"
)

// Options configures Run.
type Options struct {
	Paths    config.Paths
	Getenv   func(string) string
	Profile  string // "" | "claude-code"
	Listen   string // "" → local only
	Runner   *bool  // nil → leave as is (false on a new config)
	SkillDir string // "" → write no skill file; else <SkillDir>/desk/SKILL.md
	Force    bool   // replace another program's prefix+t / prefix+a bindings
	NoHerdr  bool   // leave herdr's config alone
	Out      io.Writer
}

const gitTimeout = 30 * time.Second

// Run writes the config (keeping an existing one's values), mints the token when Listen is set and none exists,
// creates the scratch root, applies the profile, writes the skill, and writes the herdr keys. It prints one line
// per thing it wrote or skipped. It never prompts.
func Run(ctx context.Context, o Options) error {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	p := o.Paths

	cfg, err := config.Load(p.ConfigFile())
	if err != nil {
		return err
	}
	if o.Listen != "" {
		cfg.Home.Listen = o.Listen
	}
	if o.Runner != nil {
		cfg.Runner.Enabled = *o.Runner
	}
	profileLines, err := applyProfile(&cfg, o.Profile)
	if err != nil {
		return err
	}
	herdrFile := ""
	if !o.NoHerdr {
		herdrFile = herdrConfigPath(getenv)
		if _, err := os.Stat(herdrFile); errors.Is(err, fs.ErrNotExist) {
			herdrFile = ""
		} else if err != nil {
			return fmt.Errorf("herdr's config: %w", err)
		}
	}
	notifyLine := ""
	if herdrFile != "" && len(cfg.Notify.Command) == 0 {
		cfg.Notify.Command = []string{"herdr", "notification", "show", "{title}", "--body", "{body}"}
		notifyLine = "notify: set the command to herdr's notification"
	}
	if err := cfg.Validate(); err != nil {
		return badInput(err)
	}

	before, _ := os.ReadFile(p.ConfigFile())
	if err := cfg.Save(p.ConfigFile()); err != nil {
		return err
	}
	after, _ := os.ReadFile(p.ConfigFile())
	fmt.Fprintf(out, "config: %s %s\n", p.ConfigFile(), changed(!bytes.Equal(before, after)))
	for _, l := range profileLines {
		fmt.Fprintln(out, l)
	}
	if notifyLine != "" {
		fmt.Fprintln(out, notifyLine)
	}

	if o.Listen != "" {
		if err := mintToken(p, out); err != nil {
			return err
		}
	}
	if err := ensureScratch(ctx, p, out); err != nil {
		return err
	}
	if o.SkillDir != "" {
		if err := writeSkill(o.SkillDir, out); err != nil {
			return err
		}
	}
	switch {
	case o.NoHerdr:
		fmt.Fprintln(out, "herdr: left alone (--no-herdr)")
		return nil
	case herdrFile == "":
		fmt.Fprintln(out, "herdr: no config file; keys not written")
		return nil
	}
	return writeHerdr(herdrFile, o.Force, out)
}

// badInput is a value the caller gave that setup refuses; the command line maps bad-input to a usage error.
func badInput(err error) error {
	return &model.Refusal{Code: model.CodeBadInput, Msg: err.Error()}
}

func changed(c bool) string {
	if c {
		return "written"
	}
	return "unchanged"
}

// herdrConfigPath is <XDG_CONFIG_HOME or HOME/.config>/herdr/config.toml.
func herdrConfigPath(getenv func(string) string) string {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(getenv("HOME"), ".config")
	}
	return filepath.Join(base, "herdr", "config.toml")
}

// applyProfile fills the [agent] values of a profile that the config leaves empty and returns a line per value.
// A value the user already set stays.
func applyProfile(cfg *config.Config, profile string) ([]string, error) {
	switch profile {
	case "":
		return nil, nil
	case "claude-code":
	default:
		return nil, badInput(fmt.Errorf("unknown profile %q (known: claude-code)", profile))
	}
	var lines []string
	fill := func(name string, set bool, apply func()) {
		if set {
			lines = append(lines, "agent."+name+": kept")
			return
		}
		apply()
		lines = append(lines, "agent."+name+": set by the claude-code profile")
	}
	fill("router", len(cfg.Agent.Router) > 0, func() {
		cfg.Agent.Router = []string{"claude", "-p", "--safe-mode", "--tools", "", "--system-prompt-file", "{system}",
			"--json-schema", "{schema}", "--max-budget-usd", "0.10", "--no-session-persistence", "--output-format", "json"}
	})
	fill("worker", len(cfg.Agent.Worker) > 0, func() {
		cfg.Agent.Worker = []string{"claude", "--model", "{model}", "--permission-mode", "auto", "--session-id", "{session}", "--", "{message}"}
	})
	fill("session_env", cfg.Agent.SessionEnv != "", func() { cfg.Agent.SessionEnv = "CLAUDE_CODE_SESSION_ID" })
	return lines, nil
}

func mintToken(p config.Paths, out io.Writer) error {
	_, err := config.ReadToken(p)
	if err == nil {
		fmt.Fprintf(out, "token: %s kept\n", p.TokenFile())
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if _, err := config.RotateToken(p); err != nil {
		return err
	}
	fmt.Fprintf(out, "token: %s written\n", p.TokenFile())
	return nil
}

// ensureScratch makes the scratch root a git repo so a task with no project has somewhere to run.
func ensureScratch(ctx context.Context, p config.Paths, out io.Writer) error {
	dir := p.ScratchRoot()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		fmt.Fprintf(out, "scratch root: %s kept\n", dir)
		return nil
	}
	// The data dir holds the store, so it is 0700 whoever creates it first; only the scratch root is looser.
	if err := os.MkdirAll(p.DataDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := gitcmd.Run(ctx, dir, gitTimeout, "init", "--quiet"); err != nil {
		return err
	}
	fmt.Fprintf(out, "scratch root: %s written\n", dir)
	return nil
}

func writeSkill(skillDir string, out io.Writer) error {
	path := filepath.Join(skillDir, "desk", "SKILL.md")
	if old, err := os.ReadFile(path); err == nil && string(old) == desk.Skill() {
		fmt.Fprintf(out, "skill: %s unchanged\n", path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(desk.Skill()), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "skill: %s written\n", path)
	return nil
}

// writeHerdr edits herdr's config at path, copying it to <path>.desk-bak-<time> first when the text changes.
func writeHerdr(path string, force bool, out io.Writer) error {
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text, bound, skipped := WriteHerdrKeys(string(old), force)
	for _, k := range skipped {
		fmt.Fprintf(out, "herdr: %s skipped, another binding holds it (--force replaces it)\n", k)
	}
	if text == string(old) {
		fmt.Fprintf(out, "herdr: %s unchanged\n", path)
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	backup := path + ".desk-bak-" + time.Now().UTC().Format("20060102-150405.000000000")
	if err := os.WriteFile(backup, old, info.Mode().Perm()); err != nil {
		return err
	}
	fmt.Fprintf(out, "herdr: backed up to %s\n", backup)
	if err := os.WriteFile(path, []byte(text), info.Mode().Perm()); err != nil {
		return err
	}
	for _, k := range bound {
		fmt.Fprintf(out, "herdr: %s bound\n", k)
	}
	return nil
}

// ClientAdd checks home with the token (a status call), then writes [client] home and the token file.
func ClientAdd(ctx context.Context, p config.Paths, home, token string) error {
	cfg, err := config.Load(p.ConfigFile())
	if err != nil {
		return err
	}
	cfg.Client.Home = home
	if err := cfg.Validate(); err != nil {
		return badInput(err)
	}
	// The client reads its token from a token file, so the check runs on a scratch machine that holds
	// only this token.
	tmp, err := os.MkdirTemp("", "desk-client-add")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	probe := config.Paths{ConfigDir: tmp, StateDir: tmp, DataDir: tmp, CacheDir: tmp}
	if err := config.WriteToken(probe, token); err != nil {
		return err
	}
	probeCfg := config.Default()
	probeCfg.Client.Home = home
	if _, err := api.NewClient(api.ClientOptions{Paths: probe, Config: probeCfg}).Status(ctx); err != nil {
		return err
	}
	if err := config.WriteToken(p, token); err != nil {
		return err
	}
	return cfg.Save(p.ConfigFile())
}
