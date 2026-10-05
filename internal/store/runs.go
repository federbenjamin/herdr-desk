package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

// ErrNotArmed is StartRun's answer when the task is no longer armed.
var ErrNotArmed = errors.New("the task is not armed")

// runCols leaves out exit: the column has no writer, since herdr owns the pane and herdr-desk never sees a worker exit.
const runCols = `id, task, state, root, isolation, model, reason, session, workspace, pane, started_ts, ended_ts`

const liveRunStates = `('` + model.RunRouting + `', '` + model.RunWaiting + `', '` + model.RunRunning + `')`

func scanRun(row interface{ Scan(...any) error }) (model.Run, error) {
	var r model.Run
	var started string
	var ended sql.NullString
	err := row.Scan(&r.ID, &r.Task, &r.State, &r.Root, &r.Isolation, &r.Model, &r.Reason, &r.Session,
		&r.Workspace, &r.Pane, &started, &ended)
	if err != nil {
		return r, err
	}
	if r.StartedTS, err = parseTS(started); err != nil {
		return r, err
	}
	if ended.Valid {
		if r.EndedTS, err = parseTS(ended.String); err != nil {
			return r, err
		}
	}
	return r, nil
}

// readRuns returns the runs the where clause picks, by id.
func (s *Store) readRuns(ctx context.Context, where string, args ...any) ([]model.Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runCols+` FROM runs `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRuns returns every run, by id.
func (s *Store) ListRuns(ctx context.Context) ([]model.Run, error) {
	return s.readRuns(ctx, ``)
}

// LiveRuns returns the runs in state routing, waiting, or running, by id.
func (s *Store) LiveRuns(ctx context.Context) ([]model.Run, error) {
	return s.readRuns(ctx, `WHERE state IN `+liveRunStates)
}

// CurrentRun returns the task's newest run; ok is false when it has none.
func (s *Store) CurrentRun(ctx context.Context, task int) (run model.Run, ok bool, err error) {
	run, err = scanRun(s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE task = ? ORDER BY id DESC LIMIT 1`, task))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Run{}, false, nil
	}
	if err != nil {
		return model.Run{}, false, err
	}
	return run, true, nil
}

// RunsSince counts the runs started at or after t.
func (s *Store) RunsSince(ctx context.Context, t time.Time) (int, error) {
	// started_ts is RFC 3339 with trimmed nanoseconds, which does not sort as text within one second: the
	// query keeps every run from t's second on, and the exact test is on the parsed time.
	rows, err := s.db.QueryContext(ctx, `SELECT started_ts FROM runs WHERE started_ts >= ?`,
		t.UTC().Format("2006-01-02T15:04:05"))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var started string
		if err := rows.Scan(&started); err != nil {
			return 0, err
		}
		ts, err := parseTS(started)
		if err != nil {
			return 0, err
		}
		if !ts.Before(t) {
			n++
		}
	}
	return n, rows.Err()
}

// RunWrote reports whether any event carries this run's id and a session: whether its worker wrote anything.
func (s *Store) RunWrote(ctx context.Context, run int64) (bool, error) {
	var wrote bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE run = ? AND session != '')`, run).Scan(&wrote)
	return wrote, err
}

// RunUpdate is a change to a run row. A zero field is left as it is.
type RunUpdate struct {
	State     string
	Root      string
	Isolation string
	Model     string
	Reason    string
	Session   string
	Workspace string
	Pane      string
}

// UpdateRun applies u to the run while its state is from, and reports whether it did. A State of ended,
// failed, or killed also sets ended_ts. A State of running also sets started_ts, so a run's time is counted from
// its spawn, not from the routing or waiting before it.
func (s *Store) UpdateRun(ctx context.Context, id int64, from string, u RunUpdate) (bool, error) {
	var started, ended any
	if slices.Contains([]string{model.RunEnded, model.RunFailed, model.RunKilled}, u.State) {
		ended = formatTS(s.now())
	}
	if u.State == model.RunRunning {
		started = formatTS(s.now())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `UPDATE runs SET
		state = COALESCE(NULLIF(?, ''), state),
		root = COALESCE(NULLIF(?, ''), root),
		isolation = COALESCE(NULLIF(?, ''), isolation),
		model = COALESCE(NULLIF(?, ''), model),
		reason = COALESCE(NULLIF(?, ''), reason),
		session = COALESCE(NULLIF(?, ''), session),
		workspace = COALESCE(NULLIF(?, ''), workspace),
		pane = COALESCE(NULLIF(?, ''), pane),
		started_ts = COALESCE(?, started_ts),
		ended_ts = COALESCE(?, ended_ts)
		WHERE id = ? AND state = ?`,
		u.State, u.Root, u.Isolation, u.Model, u.Reason, u.Session, u.Workspace, u.Pane, started, ended, id, from)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// armedTasks returns the armed tasks' numbers in the order they were armed; extra narrows the query. One query
// decides arming for Armed and StartRun.
func (s *Store) armedTasks(ctx context.Context, q querier, extra string, args ...any) ([]int, error) {
	query := `SELECT t.number FROM tasks t
		JOIN events e ON e.id = (SELECT MAX(id) FROM events WHERE task = t.number AND kind IN ('task', 'set')
			AND json_extract(data, '$.status') = 'ready')
		WHERE t.status = 'ready' AND t.thread = 'agent' AND t.archived = 0`
	if !s.agentsMayArm {
		query += ` AND e.who = 'user'`
	}
	rows, err := q.QueryContext(ctx, query+extra+` ORDER BY e.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Armed returns the tasks the runner may start, in the order they were armed: status ready, thread "agent",
// not archived, and, unless AgentsMayArm, the newest event that set the task ready was written by a user.
func (s *Store) Armed(ctx context.Context) ([]model.Task, error) {
	numbers, err := s.armedTasks(ctx, s.db, ``)
	if err != nil {
		return nil, err
	}
	tasks, err := s.readTasks(ctx, `WHERE number IN (SELECT value FROM json_each(?))`,
		[]any{string(model.MustData(numbers))}, func(model.Task) bool { return true })
	if err != nil {
		return nil, err
	}
	out := make([]model.Task, 0, len(tasks))
	for _, n := range numbers {
		if i := slices.IndexFunc(tasks, func(t model.Task) bool { return t.Number == n }); i >= 0 {
			out = append(out, tasks[i])
		}
	}
	return out, nil
}

// StartRun creates the task's run row in state routing and sets the task started, in one transaction. The set
// event carries the run's id and no session. It returns ErrNotArmed when the task is not armed any more.
func (s *Store) StartRun(ctx context.Context, task int) (model.Run, error) {
	var run model.Run
	started := model.StatusStarted
	_, _, err := s.append(ctx, Actor{}, write{
		kind: model.KindSet,
		prepare: func(tx *sql.Tx) (int, any, error) {
			armed, err := s.armedTasks(ctx, tx, ` AND t.number = ?`, task)
			if err != nil {
				return 0, nil, err
			}
			if len(armed) == 0 {
				return 0, nil, ErrNotArmed
			}
			return task, model.Patch{Status: &started}, nil
		},
		apply: func(tx *sql.Tx, ev model.Event) error {
			ts := formatTS(ev.TS)
			res, err := tx.ExecContext(ctx, `INSERT INTO runs(task, state, started_ts) VALUES(?, ?, ?)`, task, model.RunRouting, ts)
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			// The event is inserted before the run exists, so it learns the run's id here, in the same transaction.
			if _, err := tx.ExecContext(ctx, `UPDATE events SET run = ? WHERE id = ?`, id, ev.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status = ?, updated_ts = ? WHERE number = ?`,
				string(started), ts, task); err != nil {
				return err
			}
			run, err = scanRun(tx.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE id = ?`, id))
			return err
		},
	})
	return run, err
}

// newestRun returns the id of the task's newest run, 0 when it has none.
func newestRun(ctx context.Context, q querier, task int) (int64, error) {
	var id sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT MAX(id) FROM runs WHERE task = ?`, task).Scan(&id)
	return id.Int64, err
}

// endLiveRun sets the task's live run, when it has one, to ended.
func endLiveRun(ctx context.Context, tx *sql.Tx, task int, ts time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE runs SET state = ?, ended_ts = ? WHERE task = ? AND state IN `+liveRunStates,
		model.RunEnded, formatTS(ts), task)
	return err
}
