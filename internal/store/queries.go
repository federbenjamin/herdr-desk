package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/federbenjamin/desk/internal/model"
)

// querier is what reads need from a *sql.DB or a *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var liveStatuses = []model.Status{
	model.StatusOpen, model.StatusReady, model.StatusStarted, model.StatusBlocked, model.StatusReview,
}

// Filter picks tasks.
type Filter struct {
	Statuses []model.Status `json:"statuses,omitempty"` // empty → the five live statuses
	Project  *string        `json:"project,omitempty"`  // nil → every project; "" → tasks with no project
	Archived bool           `json:"archived,omitempty"` // true → archived tasks only
	All      bool           `json:"all,omitempty"`      // every task, any status, archived or not
}

// Match reports whether t passes the filter. ListTasks and the API client both decide with it.
// With Archived and no Statuses, an archived task of any status matches.
func (f Filter) Match(t model.Task) bool {
	if f.All {
		return true
	}
	if t.Archived != f.Archived {
		return false
	}
	if f.Project != nil && t.Project != *f.Project {
		return false
	}
	switch {
	case len(f.Statuses) > 0:
		return slices.Contains(f.Statuses, t.Status)
	case f.Archived:
		return true
	default:
		return slices.Contains(liveStatuses, t.Status)
	}
}

// Live reports whether the filter only narrows the live board: not All, not Archived, no status but the
// five live ones.
func (f Filter) Live() bool {
	if f.All || f.Archived {
		return false
	}
	for _, s := range f.Statuses {
		if !slices.Contains(liveStatuses, s) {
			return false
		}
	}
	return true
}

// TaskDetail is a task and its history.
type TaskDetail struct {
	Task    model.Task    `json:"task"`
	History []model.Event `json:"history"` // every event of this task, oldest first
}

const taskCols = `number, title, notes, status, project, thread, archived, root, isolation, model, created_ts, updated_ts`

func scanTask(row interface{ Scan(...any) error }) (model.Task, error) {
	var t model.Task
	var status, created, updated string
	err := row.Scan(&t.Number, &t.Title, &t.Notes, &status, &t.Project, &t.Thread, &t.Archived, &t.Root,
		&t.Isolation, &t.Model, &created, &updated)
	if err != nil {
		return t, err
	}
	t.Status = model.Status(status)
	if t.CreatedTS, err = parseTS(created); err != nil {
		return t, err
	}
	t.UpdatedTS, err = parseTS(updated)
	t.Steps = []model.Step{}
	return t, err
}

// readTask returns one task with its steps; a missing task is unknown-task.
func readTask(ctx context.Context, q querier, number int) (model.Task, error) {
	t, err := scanTask(q.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE number = ?`, number))
	if errors.Is(err, sql.ErrNoRows) {
		return t, refuse(model.CodeUnknownTask, "no task T%d", number)
	}
	if err != nil {
		return t, err
	}
	steps, err := readSteps(ctx, q, `WHERE task = ?`, number)
	if err != nil {
		return t, err
	}
	t.Steps = append(t.Steps, steps[number]...)
	return t, nil
}

// readSteps returns the steps the where clause picks, by task, in order.
func readSteps(ctx context.Context, q querier, where string, args ...any) (map[int][]model.Step, error) {
	rows, err := q.QueryContext(ctx, `SELECT task, short_id, text, done FROM steps `+where+` ORDER BY task, pos`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int][]model.Step{}
	for rows.Next() {
		var task int
		var st model.Step
		if err := rows.Scan(&task, &st.ShortID, &st.Text, &st.Done); err != nil {
			return nil, err
		}
		out[task] = append(out[task], st)
	}
	return out, rows.Err()
}

// ListTasks returns the tasks f matches, ordered by number.
func (s *Store) ListTasks(ctx context.Context, f Filter) ([]model.Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskCols+` FROM tasks ORDER BY number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		if f.Match(t) {
			out = append(out, t)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	steps, err := readSteps(ctx, s.db, ``)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Steps = append(out[i].Steps, steps[out[i].Number]...)
	}
	return out, nil
}

// GetTask returns a task and every event of it, oldest first.
func (s *Store) GetTask(ctx context.Context, number int) (TaskDetail, error) {
	t, err := readTask(ctx, s.db, number)
	if err != nil {
		return TaskDetail{}, err
	}
	hist, err := s.events(ctx, `WHERE task = ?`, number)
	if err != nil {
		return TaskDetail{}, err
	}
	return TaskDetail{Task: t, History: hist}, nil
}

const eventCols = `id, ts, session, who, kind, task, data, tags, run, v`

// events returns the events the where clause picks, by id.
func (s *Store) events(ctx context.Context, where string, args ...any) ([]model.Event, error) {
	out := []model.Event{}
	err := s.eachEvent(ctx, where, args, func(ev model.Event) error {
		out = append(out, ev)
		return nil
	})
	return out, err
}

func (s *Store) eachEvent(ctx context.Context, where string, args []any, fn func(model.Event) error) error {
	rows, err := s.db.QueryContext(ctx, `SELECT `+eventCols+` FROM events `+where+` ORDER BY id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ev model.Event
		var ts, who, kind, data string
		var task, run sql.NullInt64
		var tags sql.NullString
		if err := rows.Scan(&ev.ID, &ts, &ev.Session, &who, &kind, &task, &data, &tags, &run, &ev.V); err != nil {
			return err
		}
		if ev.TS, err = parseTS(ts); err != nil {
			return err
		}
		if ev.Tags, err = decodeTags(tags); err != nil {
			return err
		}
		ev.Who, ev.Kind, ev.Data = model.Who(who), model.Kind(kind), json.RawMessage(data)
		ev.Task, ev.Run = int(task.Int64), run.Int64
		if err := fn(ev); err != nil {
			return err
		}
	}
	return rows.Err()
}

// SessionEvents returns the events of the session and of each session it continues (cycle-guarded), every
// merged event, and each task the chain created.
func (s *Store) SessionEvents(ctx context.Context, session string) (model.SessionData, error) {
	chain := []string{session}
	for cur := session; ; {
		var next sql.NullString
		err := s.db.QueryRowContext(ctx, `SELECT continues FROM sessions WHERE id = ?`, cur).Scan(&next)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && (!next.Valid || slices.Contains(chain, next.String))) {
			break
		}
		if err != nil {
			return model.SessionData{}, err
		}
		cur = next.String
		chain = append(chain, cur)
	}
	in := `(?` + strings.Repeat(`,?`, len(chain)-1) + `)`
	args := make([]any, len(chain))
	for i, c := range chain {
		args[i] = c
	}
	evs, err := s.events(ctx, `WHERE session IN `+in+` OR kind = 'merged'`, args...)
	if err != nil {
		return model.SessionData{}, err
	}
	out := model.SessionData{Session: session, Chain: chain, Events: evs, Tasks: []model.SessionTask{}}
	created, err := s.events(ctx, `WHERE kind = 'task' AND session IN `+in, args...)
	if err != nil {
		return model.SessionData{}, err
	}
	for _, ev := range created {
		t, err := readTask(ctx, s.db, ev.Task)
		if err != nil {
			return model.SessionData{}, err
		}
		st := model.SessionTask{Task: t, Created: ev.ID, Tags: ev.Tags}
		if t.Status == model.StatusDone {
			err := s.db.QueryRowContext(ctx,
				`SELECT COALESCE(MAX(id), 0) FROM events WHERE task = ? AND kind IN ('task', 'set') AND json_extract(data, '$.status') = 'done'`,
				t.Number).Scan(&st.DoneAt)
			if err != nil {
				return model.SessionData{}, err
			}
		}
		out.Tasks = append(out.Tasks, st)
	}
	slices.SortFunc(out.Tasks, func(a, b model.SessionTask) int { return a.Number - b.Number })
	return out, nil
}

// ListRuns returns every run, by id.
func (s *Store) ListRuns(ctx context.Context) ([]model.Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task, state, root, isolation, model, reason, session, workspace, pane,
		started_ts, ended_ts, exit FROM runs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Run{}
	for rows.Next() {
		var r model.Run
		var started string
		var ended sql.NullString
		var exit sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Task, &r.State, &r.Root, &r.Isolation, &r.Model, &r.Reason, &r.Session,
			&r.Workspace, &r.Pane, &started, &ended, &exit); err != nil {
			return nil, err
		}
		if r.StartedTS, err = parseTS(started); err != nil {
			return nil, err
		}
		if ended.Valid {
			if r.EndedTS, err = parseTS(ended.String); err != nil {
				return nil, err
			}
		}
		r.Exit = int(exit.Int64)
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountByStatus counts the live tasks (not archived, not done) by status; each live status has an entry.
func (s *Store) CountByStatus(ctx context.Context) (map[model.Status]int, error) {
	out := map[model.Status]int{}
	for _, st := range liveStatuses {
		out[st] = 0
	}
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM tasks WHERE archived = 0 GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		if _, live := out[model.Status(st)]; live {
			out[model.Status(st)] = n
		}
	}
	return out, rows.Err()
}

// ExportEvents writes every event as one JSON line, by id, and returns the count.
func (s *Store) ExportEvents(ctx context.Context, w io.Writer) (int, error) {
	enc := json.NewEncoder(w)
	n := 0
	err := s.eachEvent(ctx, ``, nil, func(ev model.Event) error {
		n++
		return enc.Encode(ev)
	})
	return n, err
}
