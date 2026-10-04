package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/model"
)

func (a *app) runsCmd() *cobra.Command {
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "runs [--all] [--json]",
		Short: "List the runner's live runs (--all: every run)",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(_ *cobra.Command, _ []string) error {
		c, err := a.client()
		if err != nil {
			return err
		}
		list := a.liveRuns
		if all {
			list = func(c *api.Client) ([]model.Run, error) { return c.ListRuns(a.ctx) }
		}
		runs, err := list(c)
		if err != nil {
			return err
		}
		if asJSON {
			if runs == nil {
				runs = []model.Run{}
			}
			return a.printJSON(runs)
		}
		if len(runs) == 0 {
			a.say("no live runs")
			return nil
		}
		now := time.Now()
		for _, r := range runs {
			end := now
			if !r.EndedTS.IsZero() {
				end = r.EndedTS
			}
			a.say("run %d  T%d  %s  %s  %s  %s  %s", r.ID, r.Task, r.State,
				dash(r.Root), dash(r.Isolation), dash(r.Model), elapsed(end.Sub(r.StartedTS)))
		}
		return nil
	})
	f := cmd.Flags()
	f.BoolVar(&all, "all", false, "list every run, not only the live ones")
	f.BoolVar(&asJSON, "json", false, "print the runs as a JSON array")

	kill := &cobra.Command{
		Use:   "kill <task>",
		Short: "Stop the task's run, close its pane, and block the task",
		Args:  cobra.ExactArgs(1),
	}
	kill.RunE = a.do(func(_ *cobra.Command, args []string) error {
		n, err := parseTask(args[0])
		if err != nil {
			return err
		}
		actor, err := a.actor()
		if err != nil {
			return err
		}
		c, err := a.client()
		if err != nil {
			return err
		}
		t, err := c.KillRun(a.ctx, actor, n)
		if err != nil {
			return err
		}
		a.say("T%d %s", t.Number, t.Status)
		return nil
	})
	cmd.AddCommand(kill)
	return cmd
}

func (a *app) runnerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runner [status]",
		Short: "Show the runner's state",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(_ *cobra.Command, _ []string) error {
		c, err := a.client()
		if err != nil {
			return err
		}
		st, err := c.Status(a.ctx)
		if err != nil {
			return err
		}
		return a.sayRunner(st.RunnerState, st.RunnerCap)
	})
	pause := func(use, short string, paused bool) *cobra.Command {
		return &cobra.Command{
			Use: use, Short: short, Args: cobra.NoArgs,
			RunE: a.do(func(_ *cobra.Command, _ []string) error {
				actor, err := a.actor()
				if err != nil {
					return err
				}
				c, err := a.client()
				if err != nil {
					return err
				}
				st, err := c.PauseRunner(a.ctx, actor, paused)
				if err != nil {
					return err
				}
				return a.sayRunner(st.RunnerState, st.RunnerCap)
			}),
		}
	}
	cmd.AddCommand(
		&cobra.Command{Use: "status", Short: "Show the runner's state", Args: cobra.NoArgs, RunE: cmd.RunE},
		pause("pause", "Start no new runs; live runs go on", true),
		pause("resume", "Start runs again", false),
	)
	return cmd
}

// sayRunner prints `runner <state>`, and the live runs against the cap when the state is on or paused.
func (a *app) sayRunner(state string, cap int) error {
	if state != "on" && state != "paused" {
		a.say("runner %s", state)
		return nil
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	live, err := a.liveRuns(c)
	if err != nil {
		return err
	}
	a.say("runner %s · %d/%d live", state, len(live), cap)
	return nil
}

// liveRuns returns the home's runs in a live state, by id: the one filter desk runs, desk runner, and desk worker
// read runs through.
func (a *app) liveRuns(c *api.Client) ([]model.Run, error) {
	runs, err := c.ListRuns(a.ctx)
	if err != nil {
		return nil, err
	}
	live := []model.Run{}
	for _, r := range runs {
		if model.RunLive(r.State) {
			live = append(live, r)
		}
	}
	return live, nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// elapsed is a duration as 42s, 7m, or 3h.
func elapsed(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
