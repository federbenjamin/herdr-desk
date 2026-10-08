package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func (a *app) runsCmd() *cobra.Command {
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "runs [--all] [--json]",
		Short: "Check the live runs against herdr once, then list them (--all: every run)",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(_ *cobra.Command, _ []string) error {
		c, err := a.client()
		if err != nil {
			return err
		}
		runs, unchecked, err := c.ReconcileRuns(a.ctx)
		if err != nil {
			return err
		}
		if unchecked != "" {
			fmt.Fprintf(a.env.Stderr, "herdr-desk runs: %s\n", unchecked)
		}
		if !all {
			runs = onlyLive(runs)
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
			a.say("%s  %s", runLine(r), elapsed(end.Sub(r.StartedTS)))
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

// runLine is a run as herdr-desk runs and herdr-desk run start print it: run <id>  T<n>  <state>  <root>  <isolation>  <model>.
func runLine(r model.Run) string {
	return fmt.Sprintf("run %d  T%d  %s  %s  %s  %s", r.ID, r.Task, r.State, dash(r.Root), dash(r.Isolation), dash(r.Model))
}

func (a *app) runCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start a run of a task",
		Args:  cobra.NoArgs,
	}
	var route store.RunRoute
	var asJSON bool
	start := &cobra.Command{
		Use:   "start <task> [--root <r>] [--isolation <i>] [--model <m>] [--first-message <template>] [--json]",
		Short: "Start a run of the task now, or queue it as waiting; a task with a live run prints that run",
		Args:  cobra.ExactArgs(1),
	}
	start.RunE = a.do(func(_ *cobra.Command, args []string) error {
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
		return a.startRun(c, actor, n, route, func(r model.Run) error {
			if asJSON {
				return a.printJSON(r)
			}
			a.say("%s", runLine(r))
			return nil
		})
	})
	routeFlags(start, &route)
	start.Flags().BoolVar(&asJSON, "json", false, "print the run as JSON")
	cmd.AddCommand(start)
	return cmd
}

// routeFlags registers on cmd the flags that pick a run's route, filling route, and returns their names. run start
// and add --start take them.
func routeFlags(cmd *cobra.Command, route *store.RunRoute) []string {
	f := cmd.Flags()
	f.StringVar(&route.Root, "root", "", "the root to run in (default: the task's, else its project's root, else the scratch root)")
	f.StringVar(&route.Isolation, "isolation", "", "self, worktree, or in-place (default: the task's, else the root's)")
	f.StringVar(&route.Model, "model", "", "one of [agent] models (default: the task's, else the first)")
	f.StringVar(&route.FirstMessage, "first-message", "", "the worker's first message, holding {task_file} (default: the task's, else the root's)")
	return []string{"root", "isolation", "model", "first-message"}
}

// startRun starts task n's run on route and prints the run with report. A run whose spawn failed in this call is
// printed, then returned as run-failed: a caller that reads only the exit code must not take it for a start.
func (a *app) startRun(c *api.Client, actor store.Actor, n int, route store.RunRoute, report func(model.Run) error) error {
	r, err := c.StartRun(a.ctx, actor, n, route)
	if err != nil {
		return err
	}
	if err := report(r); err != nil {
		return err
	}
	if r.State == model.RunFailed {
		return &model.Refusal{Code: model.CodeRunFailed, Msg: fmt.Sprintf("run %d failed: %s", r.ID, r.Reason)}
	}
	return nil
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
		return a.sayRunner(st)
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
				return a.sayRunner(st)
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

// sayRunner prints `runner <state>`, and the live runs against the cap when the state is on or paused, then that no
// ticker runs when none does.
func (a *app) sayRunner(st api.Status) error {
	if st.RunnerState != api.RunnerStateOn && st.RunnerState != api.RunnerStatePaused {
		a.say("runner %s", st.RunnerState)
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
	line := fmt.Sprintf("runner %s · %d/%d live", st.RunnerState, len(live), st.RunnerCap)
	if st.NoTicker() {
		line += " · " + api.NoTickerText
	}
	a.say("%s", line)
	return nil
}

// liveRuns returns the home's runs in a live state, by id, with no reconcile: herdr-desk runner and herdr-desk worker
// read runs through it.
func (a *app) liveRuns(c *api.Client) ([]model.Run, error) {
	runs, err := c.ListRuns(a.ctx)
	if err != nil {
		return nil, err
	}
	return onlyLive(runs), nil
}

// onlyLive returns the runs in a live state, in order.
func onlyLive(runs []model.Run) []model.Run {
	live := []model.Run{}
	for _, r := range runs {
		if model.RunLive(r.State) {
			live = append(live, r)
		}
	}
	return live
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
