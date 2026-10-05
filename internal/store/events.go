package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

// NoteInput is a note event's payload plus the event's task and tags.
type NoteInput struct {
	model.NoteData
	Task int      `json:"task,omitempty"`
	Tags []string `json:"tags,omitempty"`
}

// DecisionInput is a decision event's payload plus the event's task and tags.
type DecisionInput struct {
	model.DecisionData
	Task int      `json:"task,omitempty"`
	Tags []string `json:"tags,omitempty"`
}

// journal appends one journal event, checking that the task it names exists.
func (s *Store) journal(ctx context.Context, a Actor, kind model.Kind, task int, tags, scan []string, data any,
	check func(tx *sql.Tx) error, apply func(tx *sql.Tx, ev model.Event) error) (model.Event, error) {
	evs, err := s.append(ctx, a, journalWrite(ctx, kind, task, tags, scan, data, check, apply))
	if err != nil {
		return model.Event{}, err
	}
	return evs[0], nil
}

// journalWrite is the write of one journal event: journal's, and the note of a HandBack.
func journalWrite(ctx context.Context, kind model.Kind, task int, tags, scan []string, data any,
	check func(tx *sql.Tx) error, apply func(tx *sql.Tx, ev model.Event) error) write {
	return write{
		kind: kind,
		tags: tags,
		scan: append(scan, tags...),
		prepare: func(tx *sql.Tx) (int, any, error) {
			if task != 0 {
				if _, err := readTask(ctx, tx, task); err != nil {
					return 0, nil, err
				}
			}
			if check != nil {
				if err := check(tx); err != nil {
					return 0, nil, err
				}
			}
			return task, data, nil
		},
		apply: apply,
	}
}

// Note appends a note.
func (s *Store) Note(ctx context.Context, a Actor, in NoteInput) (model.Event, error) {
	if strings.TrimSpace(in.Text) == "" {
		return model.Event{}, refuse(model.CodeEmptyText, "a note needs text")
	}
	return s.journal(ctx, a, model.KindNote, in.Task, in.Tags, []string{in.Text, in.Ref}, in.NoteData, nil, nil)
}

// Decide appends a decision. Replaces must name a decision event.
func (s *Store) Decide(ctx context.Context, a Actor, in DecisionInput) (model.Event, error) {
	if strings.TrimSpace(in.Text) == "" {
		return model.Event{}, refuse(model.CodeEmptyText, "a decision needs text")
	}
	check := func(tx *sql.Tx) error {
		if in.Replaces == 0 {
			return nil
		}
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM events WHERE id = ? AND kind = 'decision'`, in.Replaces).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return refuse(model.CodeUnknownEvent, "e%d is not a decision", in.Replaces)
		}
		return err
	}
	return s.journal(ctx, a, model.KindDecision, in.Task, in.Tags, []string{in.Text}, in.DecisionData, check, nil)
}

// Merged appends a merged event for a branch. The branch lives in the event data only; the event has no tags.
func (s *Store) Merged(ctx context.Context, a Actor, in model.MergedData) (model.Event, error) {
	if strings.TrimSpace(in.Branch) == "" {
		return model.Event{}, refuse(model.CodeBadInput, "a merged event names its branch")
	}
	return s.journal(ctx, a, model.KindMerged, 0, nil, []string{in.Branch, in.SHA, in.Text}, in, nil, nil)
}

// Compacted appends a compaction marker for the actor's session.
func (s *Store) Compacted(ctx context.Context, a Actor) (model.Event, error) {
	return s.journal(ctx, a, model.KindCompacted, 0, nil, nil, struct{}{}, nil, nil)
}

// Continues records that the actor's session continues from, in the event log and the sessions table.
func (s *Store) Continues(ctx context.Context, a Actor, from string) (model.Event, error) {
	if !model.ValidSessionID(a.Session) || !model.ValidSessionID(from) || from == a.Session {
		return model.Event{}, refuse(model.CodeBadInput, "continues needs two different valid session ids: %q and %q", a.Session, from)
	}
	apply := func(tx *sql.Tx, _ model.Event) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO sessions(id, continues) VALUES(?, ?) ON CONFLICT(id) DO UPDATE SET continues = excluded.continues`,
			a.Session, from)
		return err
	}
	return s.journal(ctx, a, model.KindContinues, 0, nil, nil, model.ContinuesData{From: from}, nil, apply)
}
