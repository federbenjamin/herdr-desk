package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/model"
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
		if r, ok := model.AsRefusal(err); ok && (r.Code == model.CodeHomeUnreachable || r.Code == model.CodeBadToken) {
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
	cmd.AddCommand(start)
	return cmd
}
