package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

// ErrRunLive is StartRun's answer when the task already has a starting, waiting, or running run; StartRun returns
// that run with it.
var ErrRunLive = errors.New("the task already has a live run")

// runCols leaves out exit: the column has no writer, since herdr owns the pane and herdr-desk never sees a worker exit.
const runCols = `id, task, state, root, isolation, model, first_message, reason, session, workspace, pane, started_ts, ended_ts, left_open`

// liveRunStates is model.LiveRunStates as an SQL list.
var liveRunStates = sqlList(model.LiveRunStates())

// sqlList is states as an SQL list, ('a', 'b'). The states are model's constants, never input.
func sqlList(states []string) string {
	return "('" + strings.Join(states, "', '") + "')"
}

// execer is what a write of run rows needs from a *sql.DB or a *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func scanRun(row interface{ Scan(...any) error }) (model.Run, error) {
	var r model.Run
	var started string
	var ended sql.NullString
	err := row.Scan(&r.ID, &r.Task, &r.State, &r.Root, &r.Isolation, &r.Model, &r.FirstMessage, &r.Reason, &r.Session,
		&r.Workspace, &r.Pane, &started, &ended, &r.LeftOpen)
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
func readRuns(ctx context.Context, q querier, where string, args ...any) ([]model.Run, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+runCols+` FROM runs `+where+` ORDER BY id`, args...)
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
	return readRuns(ctx, s.db, ``)
}

// LiveRuns returns the runs in state starting, waiting, running, or idle, by id.
func (s *Store) LiveRuns(ctx context.Context) ([]model.Run, error) {
	return readRuns(ctx, s.db, `WHERE state IN `+liveRunStates)
}

// LiveRunOnPane returns the newest live run whose pane is pane; ok is false when there is none.
func (s *Store) LiveRunOnPane(ctx context.Context, pane string) (run model.Run, ok bool, err error) {
	return s.newestLiveRun(ctx, "pane", pane)
}

// LiveRunOfSession returns the newest live run whose session is session; ok is false when there is none or session
// is "".
func (s *Store) LiveRunOfSession(ctx context.Context, session string) (run model.Run, ok bool, err error) {
	return s.newestLiveRun(ctx, "session", session)
}

// newestLiveRun returns the newest live run whose column col is value; an empty value matches none. col is one of
// the callers' constants, never input.
func (s *Store) newestLiveRun(ctx context.Context, col, value string) (model.Run, bool, error) {
	if value == "" {
		return model.Run{}, false, nil
	}
	runs, err := readRuns(ctx, s.db, `WHERE `+col+` = ? AND state IN `+liveRunStates, value)
	if err != nil || len(runs) == 0 {
		return model.Run{}, false, err
	}
	return runs[len(runs)-1], true, nil
}

// LeftOpenRuns returns the runs whose pane is owed a close, by id: a kill or a spawn could not close it, StartRun
// ended the run while idle, or its task was set done.
func (s *Store) LeftOpenRuns(ctx context.Context) ([]model.Run, error) {
	return readRuns(ctx, s.db, `WHERE left_open = 1`)
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
	return runsSince(ctx, s.db, t)
}

// runsSince is the one count of runs started at or after t: RunsSince's, and StartRun's day cap's inside its
// transaction.
func runsSince(ctx context.Context, q querier, t time.Time) (int, error) {
	// started_ts is RFC 3339 with trimmed nanoseconds, which does not sort as text within one second: the
	// query keeps every run from t's second on, and the exact test is on the parsed time.
	rows, err := q.QueryContext(ctx, `SELECT started_ts FROM runs WHERE started_ts >= ?`,
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

// RunWrote reports whether any event carries this run's id and the run's own session: whether its worker wrote
// anything. The start's own event carries the run's id and the session of whoever started it, never the run's.
func (s *Store) RunWrote(ctx context.Context, run int64) (bool, error) {
	var wrote bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events e JOIN runs r ON r.id = e.run
		WHERE e.run = ? AND e.session != '' AND e.session = r.session)`, run).Scan(&wrote)
	return wrote, err
}

// RunUpdate is a change to a run row. A zero field is left as it is. Reason is written by HandBack.
type RunUpdate struct {
	State     string
	Reason    string
	Session   string
	Workspace string
	Pane      string
	LeftOpen  *bool
}

// UpdateRun applies u to the run while its state is from, and reports whether it did. A State of ended,
// failed, or killed also sets ended_ts. A State of running from starting or waiting also sets started_ts, so a
// run's time is counted from its spawn; from idle it keeps it.
func (s *Store) UpdateRun(ctx context.Context, id int64, from string, u RunUpdate) (bool, error) {
	return updateRun(ctx, s.db, id, from, u, s.now())
}

func updateRun(ctx context.Context, q execer, id int64, from string, u RunUpdate, now time.Time) (bool, error) {
	var started, ended, leftOpen any
	if model.RunFinal(u.State) {
		ended = formatTS(now)
	}
	if u.State == model.RunRunning && (from == model.RunStarting || from == model.RunWaiting) {
		started = formatTS(now)
	}
	if u.LeftOpen != nil {
		leftOpen = *u.LeftOpen
	}
	res, err := q.ExecContext(ctx, `UPDATE runs SET
		state = COALESCE(NULLIF(?, ''), state),
		reason = COALESCE(NULLIF(?, ''), reason),
		session = COALESCE(NULLIF(?, ''), session),
		workspace = COALESCE(NULLIF(?, ''), workspace),
		pane = COALESCE(NULLIF(?, ''), pane),
		started_ts = COALESCE(?, started_ts),
		ended_ts = COALESCE(?, ended_ts),
		left_open = COALESCE(?, left_open)
		WHERE id = ? AND state = ?`,
		u.State, u.Reason, u.Session, u.Workspace, u.Pane, started, ended, leftOpen, id, from)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RunRoute is where and how a task runs: runs.start's params, Resolve's input and output, and what StartRun
// records. An empty field means not given.
type RunRoute struct {
	Root         string `json:"root,omitempty"`
	Isolation    string `json:"isolation,omitempty"`
	Model        string `json:"model,omitempty"`
	FirstMessage string `json:"first_message,omitempty"`
}

// RunCaps are the limits StartRun checks inside its transaction.
type RunCaps struct {
	Slots  int       // runner.cap: runs starting or running at once; a run past it waits
	PerDay int       // runner.max_runs_per_day: runs started at or after Since; a start past it is refused
	Since  time.Time // the start of the day PerDay counts, local midnight
}

// StartRun creates the task's run on the route and sets the task started with the route's root, isolation, and model,
// in one transaction of two writes: the run insert, then the task's set event, which carries the run's id and the
// session of a, who started it (none for a person). The route's FirstMessage is recorded on the run only, so a root's template never becomes the task's
// and the tiers keep their order on every later start. The run is starting
// when slotOpen allows it under caps.Slots, else waiting. The task must exist, be neither archived nor done, and have
// no starting, waiting, or running run (ErrRunLive, with that run). Then, while caps.PerDay runs started at or after
// caps.Since, the start is refused cap-reached and writes nothing. An idle run of the task is ended in the same
// transaction and, when it has a pane, marked left_open: its pane is owed a close (LeftOpenRuns).
func (s *Store) StartRun(ctx context.Context, a Actor, task int, route RunRoute, caps RunCaps) (model.Run, error) {
	starter := Actor{Session: a.Session}
	var run, live model.Run
	insert := write{
		scan: []string{route.FirstMessage},
		prepare: func(tx *sql.Tx) (int, any, error) {
			cur, err := readTask(ctx, tx, task)
			if err != nil {
				return 0, nil, err
			}
			switch {
			case cur.Archived:
				return 0, nil, refuse(model.CodeNotAllowed, "T%d is archived; unarchive it to run it", task)
			case cur.Status == model.StatusDone:
				return 0, nil, refuse(model.CodeNotAllowed, "T%d is done; set it open or ready to run it again", task)
			}
			lives, err := readRuns(ctx, tx, `WHERE task = ? AND state IN `+liveRunStates, task)
			if err != nil {
				return 0, nil, err
			}
			for _, r := range lives {
				if r.State != model.RunIdle {
					live = r
					return 0, nil, ErrRunLive
				}
			}
			today, err := runsSince(ctx, tx, caps.Since)
			if err != nil {
				return 0, nil, err
			}
			if today >= caps.PerDay {
				return 0, nil, refuse(model.CodeCapReached, "%d runs started today, runner.max_runs_per_day is %d", today, caps.PerDay)
			}
			ts := s.stamp(Actor{})
			if err := endLiveRuns(ctx, tx, ts, `task = ? AND state = ?`, task, model.RunIdle); err != nil {
				return 0, nil, err
			}
			// Every live run left in lives is idle, and its worker is still at its prompt in its pane: the close it is
			// owed is recorded with its end, so whoever closes it (the caller, or the ticker) learns it from this
			// transaction, never from a read made before it.
			for _, idle := range lives {
				if idle.Pane == "" {
					continue
				}
				if _, err := tx.ExecContext(ctx, `UPDATE runs SET left_open = 1 WHERE id = ?`, idle.ID); err != nil {
					return 0, nil, err
				}
			}
			state := model.RunWaiting
			if open, err := slotOpen(ctx, tx, caps.Slots, route); err != nil {
				return 0, nil, err
			} else if open {
				state = model.RunStarting
			}
			res, err := tx.ExecContext(ctx,
				`INSERT INTO runs(task, state, root, isolation, model, first_message, started_ts) VALUES(?, ?, ?, ?, ?, ?, ?)`,
				task, state, route.Root, route.Isolation, route.Model, route.FirstMessage, formatTS(ts))
			if err != nil {
				return 0, nil, err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return 0, nil, err
			}
			run, err = scanRun(tx.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE id = ?`, id))
			return 0, nil, err
		},
	}
	started := model.StatusStarted
	var after model.Task
	set := s.setWrite(ctx, starter, task, model.Patch{Status: &started, Root: &route.Root, Isolation: &route.Isolation, Model: &route.Model},
		"", false, &after)
	apply := set.apply
	set.apply = func(tx *sql.Tx, ev model.Event) error {
		// The set event is inserted with the starter's run, none, so it learns the new run's id here.
		if _, err := tx.ExecContext(ctx, `UPDATE events SET run = ? WHERE id = ?`, run.ID, ev.ID); err != nil {
			return err
		}
		return apply(tx, ev)
	}
	_, err := s.append(ctx, starter, insert, set)
	if errors.Is(err, ErrRunLive) {
		return live, err
	}
	if err != nil {
		return model.Run{}, err
	}
	return run, nil
}

// ClaimWaiting moves the oldest waiting run that slotOpen allows under cap to starting, with its started_ts set to
// now, in one transaction. ok is false when no waiting run can start.
func (s *Store) ClaimWaiting(ctx context.Context, cap int) (run model.Run, ok bool, err error) {
	_, err = s.append(ctx, Actor{}, write{
		prepare: func(tx *sql.Tx) (int, any, error) {
			waiting, err := readRuns(ctx, tx, `WHERE state = ?`, model.RunWaiting)
			if err != nil {
				return 0, nil, err
			}
			for _, w := range waiting {
				open, err := slotOpen(ctx, tx, cap, RunRoute{Root: w.Root, Isolation: w.Isolation})
				if err != nil {
					return 0, nil, err
				}
				if !open {
					continue
				}
				now := s.now()
				if _, err := tx.ExecContext(ctx, `UPDATE runs SET state = ?, started_ts = ? WHERE id = ? AND state = ?`,
					model.RunStarting, formatTS(now), w.ID, model.RunWaiting); err != nil {
					return 0, nil, err
				}
				run, ok = w, true
				run.State, run.StartedTS = model.RunStarting, now.UTC()
				break
			}
			return 0, nil, nil
		},
	})
	if err != nil {
		return model.Run{}, false, err
	}
	return run, ok, nil
}

// slotOpen is the one slot rule, answered inside the caller's transaction: fewer than cap runs are starting or
// running, and, for an in-place route, no in-place run on its root is starting, running, or idle. Waiting and idle
// runs take no slot.
func slotOpen(ctx context.Context, q querier, cap int, route RunRoute) (bool, error) {
	var used int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE state IN `+sqlList(model.SlotRunStates())).Scan(&used)
	if err != nil || used >= cap {
		return false, err
	}
	if route.Isolation != "in-place" {
		return true, nil
	}
	var busy bool
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE root = ? AND isolation = 'in-place' AND state IN `+
		sqlList(model.RootRunStates())+`)`, route.Root).Scan(&busy)
	return !busy, err
}

// HandBack is one hand-back of a run: the claim moves the run from From to To, then the note and the task's status
// are written as the run's actor.
type HandBack struct {
	From, To string       // the claim; To "" leaves the run in From
	Status   model.Status // the task's new status; "" leaves it
	IfStatus model.Status // "" → always; else Status is written only while the task's status is IfStatus, and then neither is the note of a hand-back that leaves its run live; a status-only hand-back (From equals To) writes nothing else then either
	Tags     []string     // the note's tags
	Note     string       // "" writes no note
	Reason   string       // written on the run row with the claim; "" leaves it
}

// ErrNoteWithheld wraps the secret scan's refusal of a hand-back's own text, which HandBack replaced with a fixed
// note. The hand-back was written: the claim, the fixed note, and the status.
var ErrNoteWithheld = errors.New("the hand-back's note was withheld")

// HandBack claims the run and writes hb's note, reason, and status in one transaction, as the run's actor, and
// returns the task and whether the claim held. A claim that finds the run out of From writes nothing, and so does a
// status-only hand-back (From equals To, IfStatus set) that finds the task out of IfStatus. Any other hand-back that
// finds the task out of IfStatus writes the claim, and its note only when the run ends: the note of a live run's
// hand-back is about the status it did not write. A run that is
// not its task's newest is refused stale-run, as checkRunRules refuses its worker. The status write never ends a run:
// To alone decides the run's state.
//
// The note and the reason are the runner's own text, which may quote a child's output, so they are scanned for
// secrets once. A scan that refuses them (secret-detected, or scan-failed from a scanner that cannot run) does not
// stop the hand-back: both are replaced with a fixed text that is not scanned, the rest is written, and the refusal
// comes back wrapped in ErrNoteWithheld with the task and the claim.
func (s *Store) HandBack(ctx context.Context, run model.Run, hb HandBack) (model.Task, bool, error) {
	var withheld error
	if hb.Note != "" || hb.Reason != "" {
		withheld = s.scanText(ctx, append([]string{hb.Note, hb.Reason}, hb.Tags...))
	}
	if withheld != nil {
		fixed := "the runner's note was withheld: the secret scan refused it"
		if r, ok := model.AsRefusal(withheld); ok {
			fixed += " (" + r.Code + ")"
		}
		if hb.Note != "" {
			hb.Note = fixed
		}
		if hb.Reason != "" {
			hb.Reason = fixed
		}
	}
	a := Actor{Run: run.ID}
	var out model.Task
	claimed := false
	ws := []write{{
		prepare: func(tx *sql.Tx) (int, any, error) {
			if err := checkNewestRun(ctx, tx, run.ID, run.Task); err != nil {
				return 0, nil, err
			}
			if hb.IfStatus != "" && hb.From == hb.To {
				// A status-only hand-back: with the task out of IfStatus it has nothing to write, its note included,
				// so two processes that saw the same pane write one note between them.
				cur, err := readTask(ctx, tx, run.Task)
				if err != nil || cur.Status != hb.IfStatus {
					return 0, nil, err
				}
			}
			ok, err := updateRun(ctx, tx, run.ID, hb.From, RunUpdate{State: hb.To, Reason: hb.Reason}, s.now())
			if err != nil || !ok {
				return 0, nil, err
			}
			claimed = true
			out, err = readTask(ctx, tx, run.Task)
			return 0, nil, err
		},
	}}
	if hb.Note != "" {
		note := journalWrite(ctx, model.KindNote, run.Task, hb.Tags, nil, model.NoteData{Text: hb.Note}, nil, nil)
		note.scan = nil // scanned above
		if hb.IfStatus != "" && !model.RunFinal(hb.To) {
			prepare := note.prepare
			note.prepare = func(tx *sql.Tx) (int, any, error) {
				cur, err := readTask(ctx, tx, run.Task)
				if err != nil || cur.Status != hb.IfStatus {
					return 0, nil, err
				}
				return prepare(tx)
			}
		}
		ws = append(ws, note)
	}
	if hb.Status != "" {
		p, err := s.checkPatch(model.Patch{Status: &hb.Status})
		if err != nil {
			return model.Task{}, false, err
		}
		ws = append(ws, s.setWrite(ctx, a, run.Task, p, hb.IfStatus, false, &out))
	}
	for i := 1; i < len(ws); i++ {
		prepare := ws[i].prepare
		ws[i].prepare = func(tx *sql.Tx) (int, any, error) {
			if !claimed {
				return 0, nil, nil
			}
			return prepare(tx)
		}
	}
	if _, err := s.append(ctx, a, ws...); err != nil {
		return model.Task{}, false, err
	}
	if !claimed {
		return model.Task{}, false, nil
	}
	if withheld != nil {
		return out, true, fmt.Errorf("%w: %w", ErrNoteWithheld, withheld)
	}
	return out, true, nil
}

// newestRun returns the id of the task's newest run, 0 when it has none.
func newestRun(ctx context.Context, q querier, task int) (int64, error) {
	var id sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT MAX(id) FROM runs WHERE task = ?`, task).Scan(&id)
	return id.Int64, err
}

// endLiveRuns sets the live runs the where clause picks to ended at ts.
func endLiveRuns(ctx context.Context, q execer, ts time.Time, where string, args ...any) error {
	_, err := q.ExecContext(ctx, `UPDATE runs SET state = ?, ended_ts = ? WHERE `+where+` AND state IN `+liveRunStates,
		append([]any{model.RunEnded, formatTS(ts)}, args...)...)
	return err
}
