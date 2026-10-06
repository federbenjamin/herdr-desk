package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// hookInput is the part of Claude Code's SessionStart JSON the hook reads.
type hookInput struct {
	SessionID string `json:"session_id"`
	Source    string `json:"source"`
}

const maxHookInput = 1 << 20

func (a *app) hookCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{Use: "hook", Short: "Agent harness hooks", Args: cobra.NoArgs}
	start := &cobra.Command{
		Use:   "start --format claude-code",
		Short: "The session-start hook: write this session's journal view and print its path",
		Args:  cobra.NoArgs,
	}
	start.RunE = a.do(func(_ *cobra.Command, _ []string) error {
		if a.env.Getenv("DESK_HOOKS") == "off" {
			return nil
		}
		if format != "claude-code" {
			return usage("--format takes claude-code")
		}
		var in hookInput
		if err := json.NewDecoder(io.LimitReader(a.env.Stdin, maxHookInput)).Decode(&in); err != nil {
			return usage("stdin is not the hook's JSON: %v", err)
		}
		if !model.ValidSessionID(in.SessionID) {
			return badSessionID()
		}
		actor := store.Actor{Session: in.SessionID, Run: a.runID()}
		c, err := a.client()
		if err != nil {
			return err
		}
		if in.Source == "compact" {
			if _, _, err := c.Append(a.ctx, api.AppendRequest{Actor: actor, Kind: model.KindCompacted}); err != nil {
				return err
			}
		}
		data, err := c.SessionView(a.ctx, in.SessionID)
		if r, ok := model.AsRefusal(err); ok && r.Code == model.CodeHomeUnreachable {
			a.say("herdr-desk: this session's journal is not loaded: %s", r.Msg)
			return nil
		}
		if err != nil {
			return err
		}
		path, err := a.writeSessionView(in.SessionID, data)
		if err != nil {
			return err
		}
		a.say("herdr-desk journal for this session: %s", path)
		a.say("Read the journal again with `herdr-desk session %s --md`.", in.SessionID)
		return nil
	})
	start.Flags().StringVar(&format, "format", "", "the hook input's format: claude-code")
	event := &cobra.Command{
		Use:   "herdr-event",
		Short: "herdr's pane event hook: check the pane's run against herdr now",
		Args:  cobra.NoArgs,
		RunE: a.do(func(_ *cobra.Command, _ []string) error {
			a.herdrEvent()
			return nil
		}),
	}
	cmd.AddCommand(start, event)
	return cmd
}

// herdrEvent tracks the pane herdr's event names. herdr runs it for every pane on the machine, so a pane no live
// run owns costs a read-only open and one lookup. Nothing reaches herdr: it always exits 0. A call that does not
// track its pane for a fault says why in the log; a client, DESK_HOOKS=off, no store, and a pane no live run owns are
// not faults and log nothing.
func (a *app) herdrEvent() {
	if a.env.Getenv("DESK_HOOKS") == "off" {
		return
	}
	c, err := config.Load(a.paths.ConfigFile())
	if err != nil {
		a.hookLog(fmt.Errorf("the config does not load, so no pane is tracked: %w", err))
		return
	}
	if c.IsClient() {
		return
	}
	var ev struct {
		Data struct {
			PaneID string `json:"pane_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(a.env.Getenv("HERDR_PLUGIN_EVENT_JSON")), &ev); err != nil || ev.Data.PaneID == "" {
		a.hookLog(fmt.Errorf("the event %q has no data.pane_id in HERDR_PLUGIN_EVENT_JSON (%v), so no pane is tracked",
			a.env.Getenv("HERDR_PLUGIN_EVENT"), err))
		return
	}
	pane := ev.Data.PaneID
	owned, err := paneOwned(a.ctx, a.paths, pane)
	if err != nil {
		a.hookLog(fmt.Errorf("pane %s: look for its run: %w", pane, err))
		return
	}
	if !owned {
		return
	}
	r, err := runner.Open(a.paths, c)
	if err != nil {
		a.hookLog(fmt.Errorf("pane %s: %w", pane, err))
		return
	}
	err = r.Track(a.ctx, pane)
	if cerr := r.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		a.hookLog(fmt.Errorf("pane %s: %w", pane, err))
	}
}

// paneOwned reports whether a live run owns the pane, through a read-only open. No store owns nothing; a store of
// another schema version is an error: this binary is not the one that migrated it.
func paneOwned(ctx context.Context, p config.Paths, pane string) (bool, error) {
	st, err := store.OpenReadOnly(p.DB(), store.Options{})
	if errors.Is(err, store.ErrNoStore) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer st.Close()
	_, ok, err := st.LiveRunOnPane(ctx, pane)
	return ok, err
}

// hookLog writes err to <state>/herdr-desk.log, where the runner the hook opens logs too. A log that cannot be opened
// drops the line: a hook prints nothing to herdr.
func (a *app) hookLog(err error) {
	f, ferr := a.paths.OpenLog()
	if ferr != nil {
		return
	}
	defer f.Close()
	log.New(f, "", log.LstdFlags).Printf("herdr-desk hook herdr-event: %v", err)
}
