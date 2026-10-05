package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

// Coordinator returns the recorded coordinator; ok is false when none is recorded.
func (s *Store) Coordinator(ctx context.Context) (c model.Coordinator, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT session, workspace, pane, cursor FROM coordinator WHERE id = 1`).
		Scan(&c.Session, &c.Workspace, &c.Pane, &c.Cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Coordinator{}, false, nil
	}
	if err != nil {
		return model.Coordinator{}, false, err
	}
	return c, true, nil
}

// SetCoordinator records the coordinator's session, workspace, and pane, replacing the one recorded before and
// keeping its cursor. Only a person may: an actor with a session gets not-allowed.
func (s *Store) SetCoordinator(ctx context.Context, a Actor, c model.Coordinator) error {
	if a.Who() == model.WhoAgent {
		return refuse(model.CodeNotAllowed, "an agent may not record the coordinator; a person does")
	}
	if !model.ValidSessionID(c.Session) {
		return refuse(model.CodeBadInput, "the coordinator's session %q is not a valid session id", c.Session)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO coordinator(id, session, workspace, pane) VALUES(1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET session = excluded.session, workspace = excluded.workspace, pane = excluded.pane`,
		c.Session, c.Workspace, c.Pane)
	return err
}

// Changes is what changed since the coordinator's cursor: the events with ids in (From, To], oldest first, and how
// many older ones of them the limit left out.
type Changes struct {
	From    int64         `json:"from"`
	To      int64         `json:"to"`
	Events  []model.Event `json:"events"`
	LeftOut int           `json:"left_out"`
}

// Changes returns the events after the coordinator's cursor: the newest limit of them (every one when limit is 0
// or less), oldest first. It moves the cursor to To only when a's session is the recorded coordinator's, so a
// person's or another agent's read leaves it.
func (s *Store) Changes(ctx context.Context, a Actor, limit int) (Changes, error) {
	c, ok, err := s.Coordinator(ctx)
	if err != nil {
		return Changes{}, err
	}
	out := Changes{From: c.Cursor, To: c.Cursor}
	var newest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM events`).Scan(&newest); err != nil {
		return Changes{}, err
	}
	if newest.Int64 > out.From {
		out.To = newest.Int64
	}
	if limit <= 0 {
		limit = -1
	}
	out.Events, err = s.events(ctx, `WHERE id IN (SELECT id FROM events WHERE id > ? AND id <= ? ORDER BY id DESC LIMIT ?)`,
		out.From, out.To, limit)
	if err != nil {
		return Changes{}, err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE id > ? AND id <= ?`, out.From, out.To).Scan(&total); err != nil {
		return Changes{}, err
	}
	out.LeftOut = total - len(out.Events)
	if ok && a.Session != "" && a.Session == c.Session && out.To > c.Cursor {
		if _, err := s.db.ExecContext(ctx, `UPDATE coordinator SET cursor = ? WHERE id = 1 AND cursor < ?`, out.To, out.To); err != nil {
			return Changes{}, err
		}
	}
	return out, nil
}
