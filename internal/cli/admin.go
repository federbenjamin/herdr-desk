package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/setup"
	"github.com/federbenjamin/herdr-desk/internal/version"
)

func (a *app) clientCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "client", Short: "Join this machine to a home", Args: cobra.NoArgs}
	cmd.AddCommand(&cobra.Command{
		Use:   "add <ssh target>",
		Short: "Make this machine a client of the home at <ssh target>, once it answers through [client] command",
		Args:  cobra.ExactArgs(1),
		RunE: a.do(func(_ *cobra.Command, args []string) error {
			if err := setup.ClientAdd(a.ctx, a.paths, args[0]); err != nil {
				return err
			}
			a.say("client of %s", args[0])
			return nil
		}),
	})
	return cmd
}

func (a *app) rootsCmd() *cobra.Command {
	var asJSON, agentsMayStart bool
	var about, isolation, firstMessage string
	list := func(_ *cobra.Command, _ []string) error {
		c, err := a.config()
		if err != nil {
			return err
		}
		return a.printRoots(c.Roots, asJSON)
	}
	cmd := &cobra.Command{
		Use:   "roots [list] | add <path> | remove <path>",
		Short: "List or edit the roots tasks run in",
		Args:  cobra.NoArgs,
		RunE:  a.do(list),
	}
	cmd.PersistentFlags().BoolVar(&asJSON, "json", false, "print the roots as a JSON array")
	edit := func(change func(cmd *cobra.Command, c *config.Config, path string) error) func(*cobra.Command, []string) error {
		return a.do(func(cmd *cobra.Command, args []string) error {
			c, err := a.config()
			if err != nil {
				return err
			}
			path := args[0]
			if !filepath.IsAbs(path) {
				path = filepath.Join(a.env.Cwd, path)
			}
			if err := change(cmd, &c, path); err != nil {
				if _, ok := model.AsRefusal(err); ok {
					return err
				}
				return usage("%v", err)
			}
			if err := c.Save(a.paths.ConfigFile()); err != nil {
				return err
			}
			return a.printRoots(c.Roots, asJSON)
		})
	}
	add := &cobra.Command{
		Use:   "add <path>",
		Short: "Add a root, or change the one with that path (only the fields whose flags you pass)",
		Args:  cobra.ExactArgs(1),
		RunE: edit(func(cmd *cobra.Command, c *config.Config, path string) error {
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				return fmt.Errorf("root %s is not an existing directory", path)
			}
			r := config.Root{Path: path}
			if i := slices.IndexFunc(c.Roots, func(have config.Root) bool { return filepath.Clean(have.Path) == filepath.Clean(path) }); i >= 0 {
				r = c.Roots[i]
			}
			if cmd.Flags().Changed("about") {
				r.About = about
			}
			if cmd.Flags().Changed("isolation") {
				r.Isolation = isolation
			}
			if cmd.Flags().Changed("first-message") {
				r.FirstMessage = firstMessage
			}
			if cmd.Flags().Changed("agents-may-start") {
				if agentsMayStart {
					session, err := a.callerSession()
					if err != nil {
						return err
					}
					if session != "" {
						return &model.Refusal{Code: model.CodeNotAllowed,
							Msg: "an agent may not let agents start runs in a root; a person does"}
					}
				}
				r.AgentsMayStart = agentsMayStart
			}
			return c.AddRoot(r)
		}),
	}
	add.Flags().StringVar(&about, "about", "", "what the root holds, for the coordinator")
	add.Flags().StringVar(&isolation, "isolation", "", "self, worktree, or in-place (unset: worktree for a git top, else in-place)")
	add.Flags().StringVar(&firstMessage, "first-message", "", "the root's default first message, holding {task_file}; '' clears it")
	add.Flags().BoolVar(&agentsMayStart, "agents-may-start", false,
		"let agent sessions other than the coordinator start runs here; =false takes it back (a person's choice: an agent is refused)")
	remove := &cobra.Command{
		Use:   "remove <path>",
		Short: "Remove a root",
		Args:  cobra.ExactArgs(1),
		RunE:  edit(func(_ *cobra.Command, c *config.Config, path string) error { return c.RemoveRoot(path) }),
	}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List the roots", Args: cobra.NoArgs, RunE: a.do(list)}, add, remove)
	return cmd
}

func (a *app) printRoots(roots []config.Root, asJSON bool) error {
	if asJSON {
		if roots == nil {
			roots = []config.Root{}
		}
		return a.printJSON(roots)
	}
	for _, r := range roots {
		line := r.Path
		if r.Isolation != "" {
			line += "  " + r.Isolation
		}
		if r.About != "" {
			line += "  " + r.About
		}
		a.say("%s", line)
	}
	return nil
}

func (a *app) setupCmd() *cobra.Command {
	var profile, runner, skillDir string
	var force, noHerdr bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Write the config, the scratch root, the profile, the skill, and the herdr keys",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(cmd *cobra.Command, _ []string) error {
		o := setup.Options{
			Paths: a.paths, Getenv: a.env.Getenv, Profile: profile,
			SkillDir: skillDir, Force: force, NoHerdr: noHerdr, Out: a.env.Stdout,
		}
		if cmd.Flags().Changed("runner") {
			var on bool
			switch runner {
			case "on":
				on = true
			case "off":
			default:
				return usage("--runner takes on or off")
			}
			o.Runner = &on
		}
		if o.SkillDir != "" && !filepath.IsAbs(o.SkillDir) {
			o.SkillDir = filepath.Join(a.env.Cwd, o.SkillDir)
		}
		return setup.Run(a.ctx, o)
	})
	f := cmd.Flags()
	f.StringVar(&profile, "profile", "", "an agent profile: claude-code")
	f.StringVar(&runner, "runner", "", "on or off")
	f.StringVar(&skillDir, "skill-dir", "", "write the agent skill to <dir>/herdr-desk/SKILL.md")
	f.BoolVar(&force, "force", false, "replace another program's prefix+t and prefix+a bindings in herdr")
	f.BoolVar(&noHerdr, "no-herdr", false, "leave herdr's config alone")
	return cmd
}

func (a *app) backupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Export every event to the backup remote now",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(_ *cobra.Command, _ []string) error {
		c, err := a.client()
		if err != nil {
			return err
		}
		r, err := c.Backup(a.ctx)
		if err != nil {
			return err
		}
		committed := "unchanged"
		if r.Committed {
			committed = "committed"
		}
		line := fmt.Sprintf("backup: %d events, %s", r.Events, committed)
		if r.Pushed {
			line += ", pushed"
		}
		a.say("%s", line)
		return nil
	})
	return cmd
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print herdr-desk's version",
		Args:  cobra.NoArgs,
		RunE: a.do(func(_ *cobra.Command, _ []string) error {
			a.say("herdr-desk %s", version.Version)
			return nil
		}),
	}
}
