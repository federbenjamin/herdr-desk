package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
)

// AddTaskInput is a task event's payload plus the event's tags.
type AddTaskInput struct {
	model.TaskData
	Tags []string `json:"tags,omitempty"`
}

// checkArm refuses an agent that asks for ready or done. AgentsMayArm lifts ready only.
func (s *Store) checkArm(a Actor, st model.Status) error {
	if a.Who() != model.WhoAgent {
		return nil
	}
	if st == model.StatusDone || (st == model.StatusReady && !s.agentsMayArm) {
		return refuse(model.CodeNotAllowed, "an agent may not set a task %s; a person does", st)
	}
	return nil
}

func parseStatus(s model.Status) (model.Status, error) {
	st, ok := model.ParseStatus(string(s))
	if !ok {
		return "", fmt.Errorf("unknown status %q", s)
	}
	return st, nil
}

// AddTask creates a task with the next number.
func (s *Store) AddTask(ctx context.Context, a Actor, in AddTaskInput) (model.Task, error) {
	if strings.TrimSpace(in.Title) == "" {
		return model.Task{}, refuse(model.CodeEmptyTitle, "a task needs a title")
	}
	if in.Status == "" {
		in.Status = model.StatusOpen
	}
	st, err := parseStatus(in.Status)
	if err != nil {
		return model.Task{}, err
	}
	in.Status = st
	if err := s.checkArm(a, st); err != nil {
		return model.Task{}, err
	}
	var out model.Task
	_, _, err = s.append(ctx, a, write{
		kind: model.KindTask,
		tags: in.Tags,
		scan: append([]string{in.Title, in.Notes, in.Project, in.Thread}, in.Tags...),
		prepare: func(tx *sql.Tx) (int, any, error) {
			project, err := resolveProject(ctx, tx, in.Project)
			if err != nil {
				return 0, nil, err
			}
			in.Project = project
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(number), 0) + 1 FROM tasks`).Scan(&n); err != nil {
				return 0, nil, err
			}
			return n, in.TaskData, nil
		},
		apply: func(tx *sql.Tx, ev model.Event) error {
			ts := formatTS(ev.TS)
			_, err := tx.ExecContext(ctx,
				`INSERT INTO tasks(number, title, notes, status, project, thread, created_ts, updated_ts) VALUES(?,?,?,?,?,?,?,?)`,
				ev.Task, in.Title, in.Notes, string(in.Status), in.Project, in.Thread, ts, ts)
			if err != nil {
				return err
			}
			out, err = readTask(ctx, tx, ev.Task)
			return err
		},
	})
	return out, err
}

// resolveProject keeps an absolute path as given and turns a bare name into the one known project with
// that base name.
func resolveProject(ctx context.Context, q querier, project string) (string, error) {
	if project == "" || filepath.IsAbs(project) {
		return project, nil
	}
	if strings.ContainsRune(project, filepath.Separator) {
		return "", refuse(model.CodeUnknownProject, "project %q is neither an absolute path nor a bare name", project)
	}
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT project FROM tasks WHERE project != ''`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return "", err
		}
		if filepath.Base(p) == project {
			found = append(found, p)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", refuse(model.CodeUnknownProject, "no known project is named %q", project)
	default:
		return "", refuse(model.CodeUnknownProject, "%d known projects are named %q; give the path", len(found), project)
	}
}

// SetTask patches a task. A patch that changes no field and carries no ref writes no event and returns the
// task. An agent may not ask for ready or done; review with Merged writes the OnMerged status.
func (s *Store) SetTask(ctx context.Context, a Actor, number int, p model.Patch) (model.Task, error) {
	if p.Title != nil && strings.TrimSpace(*p.Title) == "" {
		return model.Task{}, refuse(model.CodeEmptyTitle, "a task needs a title")
	}
	if p.Status != nil {
		st, err := parseStatus(*p.Status)
		if err != nil {
			return model.Task{}, err
		}
		if err := s.checkArm(a, st); err != nil {
			return model.Task{}, err
		}
		if st == model.StatusReview && p.Merged {
			st = s.onMerged
		}
		p.Status = &st
	}
	if p.Isolation != nil && !config.ValidIsolation(*p.Isolation) {
		return model.Task{}, fmt.Errorf("isolation must be self, worktree, or in-place, not %q", *p.Isolation)
	}
	var out model.Task
	_, _, err := s.append(ctx, a, write{
		kind: model.KindSet,
		scan: []string{deref(p.Title), deref(p.Notes), deref(p.Thread), deref(p.Root), deref(p.Isolation), deref(p.Model), p.Ref},
		prepare: func(tx *sql.Tx) (int, any, error) {
			cur, err := readTask(ctx, tx, number)
			if err != nil {
				return 0, nil, err
			}
			out = cur
			diff := model.Patch{Ref: p.Ref}
			changed := false
			if p.Status != nil && *p.Status != cur.Status {
				diff.Status, out.Status, changed = p.Status, *p.Status, true
			}
			for _, f := range []struct {
				want *string
				cur  *string
				set  **string
			}{
				{p.Title, &out.Title, &diff.Title},
				{p.Notes, &out.Notes, &diff.Notes},
				{p.Thread, &out.Thread, &diff.Thread},
				{p.Root, &out.Root, &diff.Root},
				{p.Isolation, &out.Isolation, &diff.Isolation},
				{p.Model, &out.Model, &diff.Model},
			} {
				if f.want != nil && *f.want != *f.cur {
					*f.set, *f.cur, changed = f.want, *f.want, true
				}
			}
			if p.Archived != nil && *p.Archived != cur.Archived {
				diff.Archived, out.Archived, changed = p.Archived, *p.Archived, true
			}
			if !changed && p.Ref == "" {
				return 0, nil, nil
			}
			diff.Merged = p.Merged
			return number, diff, nil
		},
		apply: func(tx *sql.Tx, ev model.Event) error {
			out.UpdatedTS = ev.TS
			_, err := tx.ExecContext(ctx,
				`UPDATE tasks SET title=?, notes=?, status=?, thread=?, root=?, isolation=?, model=?, archived=?, updated_ts=? WHERE number=?`,
				out.Title, out.Notes, string(out.Status), out.Thread, out.Root, out.Isolation, out.Model, out.Archived,
				formatTS(ev.TS), number)
			return err
		},
	})
	return out, err
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Step changes a task's steps. Step ids are s1, s2, … per task and are never reused.
func (s *Store) Step(ctx context.Context, a Actor, number int, op model.StepOp) (model.Task, error) {
	switch op.Op {
	case "add":
		op.ShortID = ""
	case "toggle", "remove":
		op.Text = ""
	case "rename":
	default:
		return model.Task{}, fmt.Errorf("unknown step op %q; want add, toggle, rename, or remove", op.Op)
	}
	if (op.Op == "add" || op.Op == "rename") && strings.TrimSpace(op.Text) == "" {
		return model.Task{}, refuse(model.CodeEmptyText, "a step needs text")
	}
	var out model.Task
	_, _, err := s.append(ctx, a, write{
		kind: model.KindStep,
		scan: []string{op.Text},
		prepare: func(tx *sql.Tx) (int, any, error) {
			if _, err := readTask(ctx, tx, number); err != nil {
				return 0, nil, err
			}
			if op.Op == "add" {
				var n int
				err := tx.QueryRowContext(ctx,
					`SELECT COUNT(*) FROM events WHERE task = ? AND kind = 'step' AND json_extract(data, '$.op') = 'add'`,
					number).Scan(&n)
				if err != nil {
					return 0, nil, err
				}
				op.ShortID = "s" + strconv.Itoa(n+1)
				return number, op, nil
			}
			var one int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM steps WHERE task = ? AND short_id = ?`, number, op.ShortID).Scan(&one)
			if err == sql.ErrNoRows {
				return 0, nil, refuse(model.CodeUnknownStep, "T%d has no step %q", number, op.ShortID)
			}
			if err != nil {
				return 0, nil, err
			}
			return number, op, nil
		},
		apply: func(tx *sql.Tx, ev model.Event) error {
			var q string
			var args []any
			switch op.Op {
			case "add":
				q = `INSERT INTO steps(task, short_id, text, done, pos)
					VALUES(?, ?, ?, 0, (SELECT COALESCE(MAX(pos), 0) + 1 FROM steps WHERE task = ?))`
				args = []any{number, op.ShortID, op.Text, number}
			case "toggle":
				q, args = `UPDATE steps SET done = 1 - done WHERE task = ? AND short_id = ?`, []any{number, op.ShortID}
			case "rename":
				q, args = `UPDATE steps SET text = ? WHERE task = ? AND short_id = ?`, []any{op.Text, number, op.ShortID}
			case "remove":
				q, args = `DELETE FROM steps WHERE task = ? AND short_id = ?`, []any{number, op.ShortID}
			}
			if _, err := tx.ExecContext(ctx, q, args...); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET updated_ts = ? WHERE number = ?`, formatTS(ev.TS), number); err != nil {
				return err
			}
			var err error
			out, err = readTask(ctx, tx, number)
			return err
		},
	})
	return out, err
}
