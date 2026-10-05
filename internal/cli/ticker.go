package cli

import (
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/ticker"
)

const stopTimeout = 10 * time.Second

func (a *app) tickerCmd() *cobra.Command {
	run := func(_ *cobra.Command, _ []string) error {
		c, err := a.config()
		if err != nil {
			return err
		}
		if c.IsClient() {
			a.say("herdr-desk ticker: this machine is a client of %s; the ticker runs on the home", c.Client.Home)
			return nil
		}
		err = ticker.Run(a.ctx, ticker.Options{Paths: a.paths})
		if errors.Is(err, ticker.ErrRunning) {
			if info, ok := ticker.Running(a.paths); ok {
				a.say("herdr-desk ticker: already running (pid %d)", info.PID)
			} else {
				a.say("herdr-desk ticker: already running")
			}
			return nil
		}
		return err
	}
	cmd := &cobra.Command{
		Use:   "ticker [run] | status | stop",
		Short: "Run the timed jobs once a minute (herdr starts it), or ask or stop the running one",
		Args:  cobra.NoArgs,
		RunE:  a.do(run),
	}
	runCmd := &cobra.Command{Use: "run", Short: "Run the ticker in the foreground", Args: cobra.NoArgs, RunE: a.do(run)}
	status := &cobra.Command{
		Use:   "status",
		Short: "Print the home's status as JSON, its ticker included; exit 1 when the home does not answer",
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
				if r, ok := model.AsRefusal(err); ok && r.Code == model.CodeHomeUnreachable {
					msg = r.Msg
				}
				return &exitError{code: exitRefused, msg: msg}
			}
			return a.printJSON(st)
		}),
	}
	stop := &cobra.Command{
		Use:   "stop",
		Short: "Stop the ticker; no ticker is not an error",
		Args:  cobra.NoArgs,
		RunE: a.do(func(_ *cobra.Command, _ []string) error {
			return ticker.Stop(a.paths, stopTimeout)
		}),
	}
	cmd.AddCommand(runCmd, status, stop)
	return cmd
}
