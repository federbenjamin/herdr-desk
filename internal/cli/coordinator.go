package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
)

func (a *app) coordinatorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "coordinator",
		Short: "Open the desk's coordinator in its own herdr workspace, or focus it when its pane is open",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(_ *cobra.Command, _ []string) error {
		c, err := a.config()
		if err != nil {
			return err
		}
		if c.IsClient() {
			a.say("herdr-desk coordinator: this machine is a client of %s; run it on the home", c.Client.Home)
			return nil
		}
		actor, err := a.actor()
		if err != nil {
			return err
		}
		r, err := runner.Open(a.paths, c)
		if err != nil {
			return err
		}
		co, opened, err := r.Coordinator(a.ctx, actor)
		if cerr := r.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		verb := "focused"
		if opened {
			verb = "opened"
		}
		a.say("coordinator %s: workspace %s, pane %s", verb, co.Workspace, co.Pane)
		return nil
	})
	run := &cobra.Command{
		Use:   "run",
		Short: "Become the coordinator session (typed into its pane by herdr-desk coordinator)",
		Args:  cobra.NoArgs,
	}
	run.RunE = a.do(func(_ *cobra.Command, _ []string) error {
		session := a.env.Getenv("DESK_SESSION")
		if session == "" {
			return usage("DESK_SESSION is not set; herdr-desk coordinator sets it")
		}
		if !model.ValidSessionID(session) {
			return badSessionID()
		}
		c, err := a.config()
		if err != nil {
			return err
		}
		argv := config.Expand(c.Agent.Coordinator, map[string]string{"session": session, "prompt": herdrdesk.Coordinator()})
		bin, err := agentBinary("coordinator", argv)
		if err != nil {
			return &exitError{code: exitIO, msg: err.Error()}
		}
		return a.execAgent(bin, argv)
	})
	cmd.AddCommand(run)
	return cmd
}

func (a *app) skillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "skill coordinator",
		Short: "Print the coordinator skill, the coordinator's system prompt",
		Args:  cobra.ExactArgs(1),
		RunE: a.do(func(_ *cobra.Command, args []string) error {
			if args[0] != "coordinator" {
				return usage("unknown skill %q (known: coordinator)", args[0])
			}
			_, err := fmt.Fprint(a.env.Stdout, herdrdesk.Coordinator())
			return err
		}),
	}
}
