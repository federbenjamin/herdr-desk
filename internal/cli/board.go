package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

// boardSections are the static board's sections, each with its statuses in the order they are shown.
var boardSections = []struct {
	title    string
	statuses []model.Status
}{
	{"NEEDS YOU", []model.Status{model.StatusBlocked, model.StatusReview}},
	{"IN MOTION", []model.Status{model.StatusStarted}},
	{"ON DECK", []model.Status{model.StatusReady, model.StatusOpen}},
}

// boardFlags makes bare `desk` print the static board.
func (a *app) boardFlags(root *cobra.Command) {
	var asJSON bool
	root.Flags().BoolVar(&asJSON, "json", false, "print the board's TaskList as JSON")
	root.RunE = a.do(func(cmd *cobra.Command, _ []string) error {
		c, err := a.client()
		if err != nil {
			return err
		}
		tl, err := c.ListTasks(a.ctx, store.Filter{})
		if err != nil {
			return err
		}
		head := ""
		if tl.Offline {
			a.warnOffline(cmd, tl)
			head = "desk · offline (snapshot " + snapshotAge(tl.SnapshotTS) + ")"
		} else {
			st, err := c.Status(a.ctx)
			if err != nil {
				return err
			}
			head = "desk · home · runner " + onOff(st.RunnerOn)
		}
		if asJSON {
			return a.printJSON(tl)
		}
		a.say("%s", head)
		for _, s := range boardSections {
			a.say("\n%s", s.title)
			for _, st := range s.statuses {
				for _, t := range tl.Tasks {
					if t.Status == st {
						a.say("  %s", taskLine(t))
					}
				}
			}
		}
		return nil
	})
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// snapshotAge is how old the snapshot is, in its largest whole unit: 40s old, 5m old, 3h old, 2d old.
func snapshotAge(ts *time.Time) string {
	if ts == nil {
		return "of unknown age"
	}
	d := time.Since(*ts)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds old", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm old", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh old", int(d.Hours()))
	}
	return fmt.Sprintf("%dd old", int(d.Hours()/24))
}
