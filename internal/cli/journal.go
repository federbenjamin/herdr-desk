package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/journal"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

// sessionViewPath is the file the hook writes a session's view to on this machine.
func (a *app) sessionViewPath(session string) string {
	return filepath.Join(a.paths.SessionsDir(), session+".md")
}

// writeSessionView writes a session's view to its file and returns the path. The hook and every write that
// refreshes the view go through it.
func (a *app) writeSessionView(session string, data model.SessionData) (string, error) {
	path := a.sessionViewPath(session)
	return path, config.WriteFileAtomic(path, []byte(journal.Build(data, false).Markdown()))
}

// refreshSessionView brings the session's view file up to date after a write by that session succeeded, so the
// path the hook gave the agent holds the entry it just made. It does nothing when the session is empty or the hook
// wrote no file on this machine. The write has already succeeded, so a failure here is a warning.
func (a *app) refreshSessionView(cmd *cobra.Command, c *api.Client, session string) {
	if session == "" {
		return
	}
	if _, err := os.Stat(a.sessionViewPath(session)); err != nil {
		return
	}
	data, err := c.SessionView(a.ctx, session)
	if err == nil {
		_, err = a.writeSessionView(session, data)
	}
	if err != nil {
		a.warn(cmd, "the write succeeded, but the session view file was not refreshed: %v", err)
	}
}

// appendEvent sends one journal event and prints e<id>, or queued when the home did not answer.
func (a *app) appendEvent(cmd *cobra.Command, r api.AppendRequest) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	ev, queued, err := c.Append(a.ctx, r)
	if err != nil {
		return err
	}
	if queued {
		a.warn(cmd, "%s; queued, and forwarded on the next call that reaches it", a.unanswered)
		a.say("queued")
		return nil
	}
	a.refreshSessionView(cmd, c, r.Actor.Session)
	a.say("e%d", ev.ID)
	return nil
}

func (a *app) noteCmd() *cobra.Command {
	var task, ref, branch, sha string
	var tags []string
	var merged bool
	var pr int
	cmd := &cobra.Command{
		Use:   "note <text>",
		Short: "Append a note to the journal (--merged: a merged branch)",
	}
	cmd.RunE = a.do(func(cmd *cobra.Command, args []string) error {
		changed := cmd.Flags().Changed
		text := strings.Join(args, " ")
		actor, err := a.actor()
		if err != nil {
			return err
		}
		if merged {
			if branch == "" {
				return usage("--merged needs --branch")
			}
			if changed("task") || changed("ref") || changed("tag") {
				return usage("--merged takes --branch, --pr, --sha, and text only")
			}
			return a.appendEvent(cmd, api.AppendRequest{Actor: actor, Kind: model.KindMerged,
				Merged: &model.MergedData{Branch: branch, PR: pr, SHA: sha, Text: text}})
		}
		if changed("pr") || changed("sha") {
			return usage("--pr and --sha go with --merged")
		}
		in := store.NoteInput{NoteData: model.NoteData{Text: text, Ref: ref}, Tags: slices.Clone(tags)}
		if branch != "" {
			in.Tags = append(in.Tags, model.BranchTag(branch))
		}
		if changed("task") {
			if in.Task, err = parseTask(task); err != nil {
				return err
			}
		}
		return a.appendEvent(cmd, api.AppendRequest{Actor: actor, Kind: model.KindNote, Note: &in})
	})
	f := cmd.Flags()
	f.StringVar(&task, "task", "", "the task the note is about")
	f.StringVar(&ref, "ref", "", "a file or PR the note is about")
	f.StringVar(&branch, "branch", "", "the branch the note is about")
	f.StringArrayVar(&tags, "tag", nil, "a tag; repeatable")
	f.BoolVar(&merged, "merged", false, "record that --branch was merged")
	f.IntVar(&pr, "pr", 0, "the merged PR's number")
	f.StringVar(&sha, "sha", "", "the merge commit")
	return cmd
}

func (a *app) decideCmd() *cobra.Command {
	var task, replaces string
	var tags []string
	cmd := &cobra.Command{
		Use:   "decide <text>",
		Short: "Append a decision to the journal",
	}
	cmd.RunE = a.do(func(cmd *cobra.Command, args []string) error {
		actor, err := a.actor()
		if err != nil {
			return err
		}
		in := store.DecisionInput{DecisionData: model.DecisionData{Text: strings.Join(args, " ")}, Tags: slices.Clone(tags)}
		if cmd.Flags().Changed("replaces") {
			if in.Replaces, err = parseEventID(replaces); err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("task") {
			if in.Task, err = parseTask(task); err != nil {
				return err
			}
		}
		return a.appendEvent(cmd, api.AppendRequest{Actor: actor, Kind: model.KindDecision, Decision: &in})
	})
	f := cmd.Flags()
	f.StringArrayVar(&tags, "tag", nil, "a k:v tag; repeatable")
	f.StringVar(&replaces, "replaces", "", "the decision this one replaces (e<id>)")
	f.StringVar(&task, "task", "", "the task the decision is about")
	return cmd
}

func (a *app) sessionCmd() *cobra.Command {
	var all, asJSON bool
	var continues string
	cmd := &cobra.Command{
		Use:   "session [<id>]",
		Short: "Print a session's journal (default: the caller's session)",
		Args:  cobra.MaximumNArgs(1),
	}
	cmd.RunE = a.do(func(cmd *cobra.Command, args []string) error {
		actor, err := a.actor()
		if err != nil {
			return err
		}
		if len(args) == 1 {
			actor.Session = args[0]
		}
		switch {
		case actor.Session == "":
			return usage("no session: give an id, --session, or set DESK_SESSION")
		case !model.ValidSessionID(actor.Session):
			return badSessionID()
		}
		c, err := a.client()
		if err != nil {
			return err
		}
		if continues != "" {
			if !model.ValidSessionID(continues) {
				return badSessionID()
			}
			_, queued, err := c.Append(a.ctx, api.AppendRequest{Actor: actor, Kind: model.KindContinues, From: continues})
			if err != nil {
				return err
			}
			if !queued {
				a.refreshSessionView(cmd, c, actor.Session)
			}
		}
		data, err := c.SessionView(a.ctx, actor.Session)
		if err != nil {
			return err
		}
		v := journal.Build(data, all)
		if asJSON {
			return a.printJSON(v)
		}
		_, err = a.env.Stdout.Write([]byte(v.Markdown()))
		return err
	})
	f := cmd.Flags()
	f.Bool("md", true, "print markdown (the default)")
	f.BoolVar(&all, "all", false, "also print the lines the journal rules hide")
	f.StringVar(&continues, "continues", "", "first record that this session continues <old-id>")
	f.BoolVar(&asJSON, "json", false, "print the View as JSON")
	cmd.MarkFlagsMutuallyExclusive("md", "json")
	return cmd
}
