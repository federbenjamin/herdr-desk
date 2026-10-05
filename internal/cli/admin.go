package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/daemon"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/setup"
	"github.com/federbenjamin/herdr-desk/internal/version"
)

const stopTimeout = 10 * time.Second

func (a *app) daemonCmd() *cobra.Command {
	run := func(_ *cobra.Command, _ []string) error {
		c, err := a.config()
		if err != nil {
			return err
		}
		if c.IsClient() {
			a.sayClient(c)
			return nil
		}
		err = daemon.Run(a.ctx, a.paths, c)
		if errors.Is(err, daemon.ErrAlreadyRunning) {
			info, err := a.runningInfo()
			if err != nil {
				return fmt.Errorf("another daemon holds %s, but no info file names the daemon on its socket: %w", a.paths.LockFile(), err)
			}
			a.say("herdr-desk daemon: already running (pid %d)", info.PID)
			return nil
		}
		return err
	}
	cmd := &cobra.Command{
		Use:   "daemon [run] | stop | restart | status",
		Short: "Run, stop, restart, or ask the daemon",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(run)
	runCmd := &cobra.Command{Use: "run", Short: "Run the daemon in the foreground", Args: cobra.NoArgs, RunE: a.do(run)}
	stop := &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon; no daemon is not an error",
		Args:  cobra.NoArgs,
		RunE: a.do(func(_ *cobra.Command, _ []string) error {
			return daemon.Stop(a.paths, stopTimeout)
		}),
	}
	restart := &cobra.Command{
		Use:   "restart",
		Short: "Stop the daemon, then start it detached",
		Args:  cobra.NoArgs,
	}
	restart.RunE = a.do(func(_ *cobra.Command, _ []string) error {
		c, err := a.config()
		if err != nil {
			return err
		}
		if c.IsClient() {
			a.sayClient(c)
			return nil
		}
		if err := daemon.Stop(a.paths, stopTimeout); err != nil {
			return err
		}
		if a.env.Spawn == nil {
			return errors.New("this desk cannot start a daemon; run `herdr-desk daemon run`")
		}
		return a.env.Spawn(a.paths)
	})
	status := &cobra.Command{
		Use:   "status",
		Short: "Print the daemon's status as JSON; exit 1 when it does not answer",
		Args:  cobra.NoArgs,
		RunE: a.do(func(_ *cobra.Command, _ []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			st, err := c.Status(a.ctx)
			if err != nil {
				// Exit 1, as the status command's contract says. The refusal code stays off the line:
				// home-unreachable means exit 3 everywhere else.
				msg := err.Error()
				if r, ok := model.AsRefusal(err); ok && (r.Code == model.CodeHomeUnreachable || r.Code == model.CodeBadToken) {
					msg = r.Msg
				}
				return &exitError{code: exitRefused, msg: msg}
			}
			return a.printJSON(st)
		}),
	}
	cmd.AddCommand(runCmd, stop, restart, status)
	return cmd
}

// infoWait is how long `herdr-desk daemon run` waits for the running daemon's info file.
var infoWait = 5 * time.Second

// runningInfo reads the info file of the daemon that holds the lock. A daemon that took the lock a moment ago
// writes the file once it serves, and one that died without cleaning up leaves its own file behind, so a file
// counts only when its start time is the one the daemon on the socket reports. Until then it is read again,
// up to infoWait.
func (a *app) runningInfo() (daemon.Info, error) {
	c, err := a.client()
	if err != nil {
		return daemon.Info{}, err
	}
	deadline := time.Now().Add(infoWait)
	for {
		info, err := daemon.ReadInfo(a.paths)
		if err == nil {
			st, serr := c.Status(a.ctx)
			switch {
			case serr != nil:
				err = serr
			case !st.StartedTS.Equal(info.StartedTS):
				err = fmt.Errorf("%s names a daemon started %s; the one on the socket started %s",
					a.paths.DaemonInfo(), info.StartedTS.Format(time.RFC3339Nano), st.StartedTS.Format(time.RFC3339Nano))
			default:
				return info, nil
			}
		}
		if time.Now().After(deadline) {
			return daemon.Info{}, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (a *app) sayClient(c config.Config) {
	a.say("herdr-desk daemon: this machine is a client of %s; nothing to run", c.Client.Home)
}

func (a *app) tokenCmd() *cobra.Command {
	show := func(_ *cobra.Command, _ []string) error {
		t, err := config.ReadToken(a.paths)
		if err != nil {
			return fmt.Errorf("no token (`herdr-desk setup --listen <host:port>` mints one on a home): %w", err)
		}
		a.say("%s", t)
		return nil
	}
	cmd := &cobra.Command{
		Use:   "token [show] | rotate",
		Short: "Print or rotate the bearer token",
		Args:  cobra.NoArgs,
		RunE:  a.do(show),
	}
	rotate := &cobra.Command{
		Use:   "rotate",
		Short: "Mint a new token; the old one stops working at once",
		Args:  cobra.NoArgs,
		RunE: a.do(func(_ *cobra.Command, _ []string) error {
			c, err := a.config()
			if err != nil {
				return err
			}
			if c.IsClient() {
				return usage("this machine is a client of %s; rotate the token on the home, then run `herdr-desk client add` here", c.Client.Home)
			}
			t, err := config.RotateToken(a.paths)
			if err != nil {
				return err
			}
			a.say("%s", t)
			return nil
		}),
	}
	cmd.AddCommand(&cobra.Command{Use: "show", Short: "Print the token", Args: cobra.NoArgs, RunE: a.do(show)}, rotate)
	return cmd
}

func (a *app) clientCmd() *cobra.Command {
	var tokenFile string
	cmd := &cobra.Command{Use: "client", Short: "Join this machine to a home", Args: cobra.NoArgs}
	add := &cobra.Command{
		Use:   "add <host:port>",
		Short: "Make this machine a client of the home at host:port; the token comes from --token-file or stdin",
		Args:  cobra.ExactArgs(1),
	}
	add.RunE = a.do(func(_ *cobra.Command, args []string) error {
		home := args[0]
		var src io.Reader = a.env.Stdin
		if tokenFile != "" {
			f, err := os.Open(tokenFile)
			if err != nil {
				return usage("%v", err)
			}
			defer f.Close()
			src = f
		} else if a.env.StdinTTY {
			fmt.Fprint(a.env.Stderr, "token (from `herdr-desk token` on the home): ")
		}
		line, err := bufio.NewReader(src).ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		token := strings.TrimSpace(line)
		if token == "" {
			return usage("no token: pass --token-file or the token on stdin")
		}
		if err := setup.ClientAdd(a.ctx, a.paths, home, token); err != nil {
			return err
		}
		a.wroteConfig = true
		a.say("client of %s", home)
		return nil
	})
	add.Flags().StringVar(&tokenFile, "token-file", "", "a file holding the home's token")
	cmd.AddCommand(add)
	return cmd
}

func (a *app) rootsCmd() *cobra.Command {
	var asJSON bool
	var about, isolation string
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
				return usage("%v", err)
			}
			if err := c.Save(a.paths.ConfigFile()); err != nil {
				return err
			}
			a.wroteConfig = true
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
			return c.AddRoot(r)
		}),
	}
	add.Flags().StringVar(&about, "about", "", "what the root holds, for the router")
	add.Flags().StringVar(&isolation, "isolation", "", "self, worktree, or in-place (unset: the router chooses)")
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
	var profile, listen, runner, skillDir string
	var force, noHerdr bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Write the config, the token, the scratch root, the profile, the skill, and the herdr keys",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(cmd *cobra.Command, _ []string) error {
		o := setup.Options{
			Paths: a.paths, Getenv: a.env.Getenv, Profile: profile, Listen: listen,
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
		a.wroteConfig = true
		return setup.Run(a.ctx, o)
	})
	f := cmd.Flags()
	f.StringVar(&profile, "profile", "", "an agent profile: claude-code")
	f.StringVar(&listen, "listen", "", "serve clients on this host:port (a tailnet address)")
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
