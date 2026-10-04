// Package store is desk's SQLite store. The daemon is its one user; every write goes through one private
// append that scans for secrets, inserts the event, and updates the state tables in one transaction.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/secretscan"

	_ "modernc.org/sqlite"
)

// Options configures a Store.
type Options struct {
	Scanner      secretscan.Scanner // nil → secretscan.Builtin()
	Now          func() time.Time   // nil → time.Now
	AgentsMayArm bool
	OnMerged     model.Status // "" → model.StatusReview
}

// Store is an open desk database.
type Store struct {
	db           *sql.DB
	scan         secretscan.Scanner
	now          func() time.Time
	agentsMayArm bool
	onMerged     model.Status
	// mu serializes writes: a deferred SQLite transaction that reads then writes fails with SQLITE_BUSY
	// when another connection wrote first, and busy_timeout does not retry that.
	mu sync.Mutex
}

// Open opens the store at path. It creates the parent dir 0700 and the file 0600, uses WAL, and applies
// migrations.
func Open(path string, o Options) (*Store, error) {
	onMerged, ok := model.OnMergedStatus(string(o.OnMerged))
	if !ok {
		return nil, fmt.Errorf("on_merged must be review or done, not %q", o.OnMerged)
	}
	o.OnMerged = onMerged
	if o.Scanner == nil {
		o.Scanner = secretscan.Builtin()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err := private(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	if err := migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &Store{db: db, scan: o.Scanner, now: o.Now, agentsMayArm: o.AgentsMayArm, onMerged: o.OnMerged}, nil
}

// private sets the store's folder to 0700 and its files to 0600, whoever created them first and with what mode.
func private(path string) error {
	modes := map[string]os.FileMode{filepath.Dir(path): 0o700, path: 0o600, path + "-wal": 0o600, path + "-shm": 0o600}
	for f, mode := range modes {
		if err := os.Chmod(f, mode); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Actor is who makes a write. A non-empty Session means an agent.
type Actor struct {
	Session string     `json:"session,omitempty"`
	Run     int64      `json:"run,omitempty"`
	TS      *time.Time `json:"ts,omitempty"` // set only by an outbox replay; nil → Options.Now()
}

// Who is agent when the actor names a session, else user.
func (a Actor) Who() model.Who {
	if a.Session != "" {
		return model.WhoAgent
	}
	return model.WhoUser
}

func refuse(code, format string, args ...any) error {
	return &model.Refusal{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// write is one event to append.
type write struct {
	kind model.Kind
	tags []string
	scan []string // the write's text fields, scanned once as one text
	// prepare runs in the transaction before the insert and returns the event's task and payload. A nil
	// payload writes nothing.
	prepare func(tx *sql.Tx) (task int, data any, err error)
	// apply updates the state tables once the event has its id; nil for a journal event.
	apply func(tx *sql.Tx, ev model.Event) error
}

// append is the one write path. It reports whether an event was written.
func (s *Store) append(ctx context.Context, a Actor, w write) (model.Event, bool, error) {
	if err := s.scanText(ctx, w.scan); err != nil {
		return model.Event{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, false, err
	}
	defer tx.Rollback()
	task, data, err := w.prepare(tx)
	if err != nil {
		return model.Event{}, false, err
	}
	if data == nil {
		return model.Event{}, false, tx.Commit()
	}
	ts := s.now()
	if a.TS != nil {
		ts = *a.TS
	}
	ev := model.Event{
		TS:      ts.UTC(),
		Session: a.Session,
		Who:     a.Who(),
		Kind:    w.kind,
		Task:    task,
		Data:    model.MustData(data),
		Tags:    w.tags,
		Run:     a.Run,
		V:       1,
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO events(ts, session, who, kind, task, data, tags, run, v) VALUES(?,?,?,?,?,?,?,?,?)`,
		formatTS(ev.TS), ev.Session, string(ev.Who), string(ev.Kind), nullInt(int64(ev.Task)), string(ev.Data),
		encodeTags(ev.Tags), nullInt(ev.Run), ev.V)
	if err != nil {
		return model.Event{}, false, err
	}
	if ev.ID, err = res.LastInsertId(); err != nil {
		return model.Event{}, false, err
	}
	if w.apply != nil {
		if err := w.apply(tx, ev); err != nil {
			return model.Event{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.Event{}, false, err
	}
	return ev, true, nil
}

// scanText joins the fields with newlines and runs the scanner once. A refusal names the pattern, never
// the text.
func (s *Store) scanText(ctx context.Context, fields []string) error {
	var parts []string
	for _, f := range fields {
		if f != "" {
			parts = append(parts, f)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	pattern, err := s.scan(ctx, strings.Join(parts, "\n"))
	if err != nil {
		return refuse(model.CodeScanFailed, "the secret scan could not run: %v", err)
	}
	if pattern != "" {
		return refuse(model.CodeSecretDetected, "the text matches the secret pattern %s", pattern)
	}
	return nil
}

// Times are stored as RFC 3339 UTC text with nanoseconds.
func formatTS(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTS(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func encodeTags(tags []string) any {
	if len(tags) == 0 {
		return nil
	}
	return string(model.MustData(tags))
}

func decodeTags(s sql.NullString) ([]string, error) {
	if !s.Valid {
		return nil, nil
	}
	var tags []string
	return tags, json.Unmarshal([]byte(s.String), &tags)
}
