package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

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
		a.say("Record facts with `herdr-desk note \"<text>\"` (add `--branch <b>` for branch work) and choices with `herdr-desk decide \"<text>\"`.")
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
// run owns costs a read-only open and one lookup. Nothing reaches herdr: errors go to the log, and it always exits 0.
func (a *app) herdrEvent() {
	c, err := config.Load(a.paths.ConfigFile())
	if err != nil || c.IsClient() || a.env.Getenv("DESK_HOOKS") == "off" {
		return
	}
	var ev struct {
		Data struct {
			PaneID string `json:"pane_id"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(a.env.Getenv("HERDR_PLUGIN_EVENT_JSON")), &ev) != nil || ev.Data.PaneID == "" {
		return
	}
	pane := ev.Data.PaneID
	owned, err := paneOwned(a.ctx, a.paths, pane)
	if err != nil {
		a.hookLog(func() error { return fmt.Errorf("pane %s: look for its run: %w", pane, err) })
		return
	}
	if !owned {
		return
	}
	a.hookLog(func() error {
		r, err := runner.Open(a.paths, c)
		if err != nil {
			return fmt.Errorf("pane %s: %w", pane, err)
		}
		err = r.Track(a.ctx, pane)
		if cerr := r.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("pane %s: %w", pane, err)
		}
		return nil
	})
}

// paneOwned reports whether a live run owns the pane, through a read-only open. No store, or a store of another
// schema version, owns nothing.
func paneOwned(ctx context.Context, p config.Paths, pane string) (bool, error) {
	st, err := store.OpenReadOnly(p.DB(), store.Options{})
	if errors.Is(err, store.ErrNoStore) || errors.Is(err, store.ErrSchema) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer st.Close()
	_, ok, err := st.LiveRunOnPane(ctx, pane)
	return ok, err
}

// hookLog runs fn with the standard logger, which the runner logs through, pointed at <state>/herdr-desk.log, and
// logs fn's error there. A log that cannot be opened drops the lines: a hook prints nothing to herdr.
func (a *app) hookLog(fn func() error) {
	var out io.Writer = io.Discard
	if err := os.MkdirAll(a.paths.StateDir, 0o700); err == nil {
		if f, err := os.OpenFile(a.paths.Log(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600); err == nil {
			defer f.Close()
			out = f
		}
	}
	prev := log.Writer()
	log.SetOutput(out)
	defer log.SetOutput(prev)
	if err := fn(); err != nil {
		log.Printf("herdr-desk hook herdr-event: %v", err)
	}
}
