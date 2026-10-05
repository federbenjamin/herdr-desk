package cli

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk/internal/board"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// deskContext is what herdr-desk context prints: the desk as the coordinator reads it at the start of a turn.
type deskContext struct {
	StartRuns     string           `json:"start_runs"`
	RunnerState   string           `json:"runner_state"`
	Cap           int              `json:"cap"`
	Today         int              `json:"today"` // runs started since local midnight
	MaxRunsPerDay int              `json:"max_runs_per_day"`
	MaxRunMinutes int              `json:"max_run_minutes"`
	Roots         []contextRoot    `json:"roots"` // the scratch root last
	Models        []string         `json:"models"`
	Board         []contextSection `json:"board"`
	Runs          []model.Run      `json:"runs"` // the live runs
	Changes       store.Changes    `json:"changes"`
}

type contextRoot struct {
	Path      string `json:"path"`
	About     string `json:"about"`
	Isolation string `json:"isolation"`
}

type contextSection struct {
	Title string       `json:"title"`
	Tasks []model.Task `json:"tasks"`
}

func (a *app) contextCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "context [--json]",
		Short: "Print the desk for the coordinator: modes, caps, roots, models, the board, live runs, and what changed",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(_ *cobra.Command, _ []string) error {
		cfg, err := a.config()
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
		st, err := c.Status(a.ctx)
		if err != nil {
			return err
		}
		runs, err := c.ReconcileRuns(a.ctx)
		if err != nil {
			return err
		}
		tl, err := c.ListTasks(a.ctx, store.Filter{})
		if err != nil {
			return err
		}
		// Last, so the cursor moves only once everything else was read.
		changes, err := c.Changes(a.ctx, actor)
		if err != nil {
			return err
		}
		d := deskContext{
			StartRuns:     cfg.Coordinator.StartRuns,
			RunnerState:   st.RunnerState,
			Cap:           cfg.Runner.Cap,
			Today:         startedToday(runs, time.Now()),
			MaxRunsPerDay: cfg.Runner.MaxRunsPerDay,
			MaxRunMinutes: cfg.Runner.MaxRunMinutes,
			Models:        slices.Clone(cfg.Agent.Models),
			Runs:          onlyLive(runs),
			Changes:       changes,
		}
		if d.Models == nil {
			d.Models = []string{}
		}
		if d.Changes.Events == nil {
			d.Changes.Events = []model.Event{}
		}
		for _, r := range runner.Roots(cfg, a.paths) {
			d.Roots = append(d.Roots, contextRoot{Path: r.Path, About: r.About, Isolation: r.Isolation})
		}
		for _, s := range board.Sections() {
			sec := contextSection{Title: s.Title, Tasks: []model.Task{}}
			for _, t := range tl.Tasks {
				if slices.Contains(s.Statuses, t.Status) {
					sec.Tasks = append(sec.Tasks, t)
				}
			}
			d.Board = append(d.Board, sec)
		}
		if asJSON {
			return a.printJSON(d)
		}
		a.printContext(d)
		return nil
	})
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the context as one JSON object")
	return cmd
}

// startedToday counts the runs started since now's local midnight, as runner.max_runs_per_day counts them.
func startedToday(runs []model.Run, now time.Time) int {
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	n := 0
	for _, r := range runs {
		if !r.StartedTS.Before(midnight) {
			n++
		}
	}
	return n
}

func (a *app) printContext(d deskContext) {
	a.say("start_runs %s", d.StartRuns)
	a.say("runner %s · cap %d · today %d of max_runs_per_day %d · max_run_minutes %d",
		d.RunnerState, d.Cap, d.Today, d.MaxRunsPerDay, d.MaxRunMinutes)
	a.say("\nroots:")
	for _, r := range d.Roots {
		line := "  " + r.Path + "  " + dash(r.Isolation)
		if r.About != "" {
			line += "  " + r.About
		}
		a.say("%s", line)
	}
	if len(d.Models) == 0 {
		a.say("\nmodels: -")
	} else {
		a.say("\nmodels: %s", strings.Join(d.Models, ", "))
	}
	for _, s := range d.Board {
		a.say("\n%s", s.Title)
		if len(s.Tasks) == 0 {
			a.say("  -")
		}
		for _, t := range s.Tasks {
			a.say("  %s", taskLine(t))
		}
	}
	a.say("\nlive runs:")
	if len(d.Runs) == 0 {
		a.say("  -")
	}
	for _, r := range d.Runs {
		a.say("  %s", runLine(r))
	}
	ch := d.Changes
	a.say("\nchanges since e%d:", ch.From)
	if ch.LeftOut > 0 {
		a.say("  (%d older changes left out; read a task with herdr-desk show)", ch.LeftOut)
	}
	if len(ch.Events) == 0 {
		a.say("  -")
	}
	for _, e := range ch.Events {
		task := "-"
		if e.Task != 0 {
			task = "T" + strconv.Itoa(e.Task)
		}
		a.say("  e%d  %s  %s  %s  %s  %s", e.ID, e.TS.UTC().Format(timeFormat), task, e.Who, e.Kind, e.Data)
	}
}
