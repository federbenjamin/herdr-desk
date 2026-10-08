package cli

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/board"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// deskContext is what herdr-desk context prints: the desk as the coordinator reads it at the start of a turn.
type deskContext struct {
	StartRuns     string           `json:"start_runs"`
	RunnerState   string           `json:"runner_state"`
	NoTicker      bool             `json:"no_ticker"` // api.Status.NoTicker: the runner is on or paused and no ticker runs
	Cap           int              `json:"cap"`
	Today         int              `json:"today"` // runs started since local midnight
	MaxRunsPerDay int              `json:"max_runs_per_day"`
	Roots         []api.Root       `json:"roots"` // the scratch root last
	Models        []string         `json:"models"`
	Board         []contextSection `json:"board"`
	Runs          []model.Run      `json:"runs"`                // the live runs
	Unchecked     string           `json:"unchecked,omitempty"` // why the live runs were not checked against herdr
	Changes       store.Changes    `json:"changes"`
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
		runs, unchecked, err := c.ReconcileRuns(a.ctx)
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
		// Every figure is the home's, from its own config and store: a client's config says nothing of the desk.
		d := deskContext{
			StartRuns:     st.StartRuns,
			RunnerState:   st.RunnerState,
			NoTicker:      st.NoTicker(),
			Cap:           st.RunnerCap,
			Today:         st.Today,
			MaxRunsPerDay: st.MaxRunsPerDay,
			Roots:         st.Roots,
			Models:        st.Models,
			Runs:          onlyLive(runs),
			Unchecked:     unchecked,
			Changes:       changes,
		}
		if d.Roots == nil {
			d.Roots = []api.Root{}
		}
		if d.Models == nil {
			d.Models = []string{}
		}
		if d.Changes.Events == nil {
			d.Changes.Events = []model.Event{}
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

func (a *app) printContext(d deskContext) {
	a.say("start_runs %s", d.StartRuns)
	a.say("runner %s · cap %d · today %d of max_runs_per_day %d", d.RunnerState, d.Cap, d.Today, d.MaxRunsPerDay)
	if d.NoTicker {
		a.say("%s", api.NoTickerText)
	}
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
	if d.Unchecked != "" {
		a.say("  (%s)", d.Unchecked)
	}
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
		a.say("  e%d  %s  %s  %s  %s  %s", e.ID, e.TS.UTC().Format(timeFormat), task, e.Who, e.Kind, changeData(e.Data))
	}
}

// maxChangeData is the longest event payload a changes line prints whole; a longer one is cut, so a notes save
// does not put the whole notes in front of the coordinator every turn.
const maxChangeData = 200

func changeData(data json.RawMessage) string {
	s := string(data)
	if utf8.RuneCountInString(s) <= maxChangeData {
		return s
	}
	cut := []rune(s)[:maxChangeData]
	return string(cut) + "… (" + strconv.Itoa(utf8.RuneCountInString(s)-maxChangeData) + " more; herdr-desk show has it all)"
}
