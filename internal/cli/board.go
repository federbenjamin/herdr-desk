package cli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/board"
	"github.com/federbenjamin/desk/internal/herdr"
	"github.com/federbenjamin/desk/internal/store"
)

// boardFlags makes bare `desk` run the interactive board on a terminal and print the static board anywhere else.
func (a *app) boardFlags(root *cobra.Command) {
	var asJSON bool
	root.Flags().BoolVar(&asJSON, "json", false, "print the board's TaskList as JSON")
	root.RunE = a.do(func(cmd *cobra.Command, _ []string) error {
		c, err := a.client()
		if err != nil {
			return err
		}
		if a.interactive(asJSON) {
			o, err := a.boardOptions(c)
			if err != nil {
				return err
			}
			return board.Run(a.ctx, o)
		}
		tl, err := c.ListTasks(a.ctx, store.Filter{})
		if err != nil {
			return err
		}
		if tl.Offline {
			a.warnOffline(cmd, tl)
		}
		if asJSON {
			return a.printJSON(tl)
		}
		head := "desk · offline (snapshot " + snapshotAge(tl.SnapshotTS) + ")"
		if !tl.Offline {
			st, err := c.Status(a.ctx)
			if err != nil {
				return err
			}
			head = "desk · home · runner " + onOff(st.RunnerOn)
		}
		a.say("%s", head)
		for _, s := range board.Sections() {
			a.say("\n%s", s.Title)
			for _, st := range s.Statuses {
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

// snapshotAge is how old the snapshot is, as board.Age prints it with " old" after it.
func snapshotAge(ts *time.Time) string {
	if ts == nil {
		return "of unknown age"
	}
	return board.Age(time.Since(*ts)) + " old"
}

// interactive is true when the board or the capture popup may take the terminal: stdin and stdout are both
// terminals and the caller did not ask for JSON.
func (a *app) interactive(asJSON bool) bool {
	return a.env.StdinTTY && a.env.StdoutTTY && !asJSON
}

// boardOptions is what board.Run and board.Capture need on this machine.
func (a *app) boardOptions(c *api.Client) (board.Options, error) {
	cfg, err := a.config()
	if err != nil {
		return board.Options{}, err
	}
	// No herdr is not an error here: the board says so when a key needs it.
	bin, _ := herdr.Find()
	return board.Options{
		Home:   c,
		IsHome: !cfg.IsClient(),
		Herdr:  bin,
		In:     a.env.Stdin,
		Out:    a.env.Stdout,
	}, nil
}
