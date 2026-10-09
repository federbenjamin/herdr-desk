// Package store is herdr-desk's SQLite store. Every command on the home opens it, so several processes may write
// at once: every write transaction begins IMMEDIATE, and a second writer waits on the busy timeout. Every event
// goes through one private append that scans for secrets, inserts the event, and updates the state tables in one
// transaction. Run rows and the coordinator row are runner state, outside the event log: UpdateRun and
// SetCoordinator write them directly.
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
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/secretscan"

	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Options configures a Store.
type Options struct {
	Scanner  secretscan.Scanner // nil → secretscan.Builtin()
	Now      func() time.Time   // nil → time.Now
	OnMerged model.Status       // "" → model.StatusReview
}

// Store is an open desk database.
type Store struct {
	db       *sql.DB
	scan     secretscan.Scanner
	now      func() time.Time
	onMerged model.Status
}

var (
	// ErrNoStore is OpenReadOnly finding no store file.
	ErrNoStore = errors.New("no store")
	// ErrSchema is OpenReadOnly finding a schema version other than this binary's.
	ErrSchema = errors.New("the store's schema version is not this binary's")
)

// withDefaults checks o and fills its unset fields.
func (o Options) withDefaults() (Options, error) {
	onMerged, ok := model.OnMergedStatus(string(o.OnMerged))
	if !ok {
		return o, fmt.Errorf("on_merged must be review or done, not %q", o.OnMerged)
	}
	o.OnMerged = onMerged
	if o.Scanner == nil {
		o.Scanner = secretscan.Builtin()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o, nil
}

func newStore(db *sql.DB, o Options) *Store {
	return &Store{db: db, scan: o.Scanner, now: o.Now, onMerged: o.OnMerged}
}

// Open opens the store at path. It creates the parent dir 0700 and the file 0600, uses WAL, and applies
// migrations, writing nothing when the schema is current. Every write transaction begins IMMEDIATE.
func Open(path string, o Options) (*Store, error) {
	o, err := o.withDefaults()
	if err != nil {
		return nil, err
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
	// _txlock=immediate: a deferred transaction that reads then writes fails with SQLITE_BUSY when another
	// connection wrote first, and the busy timeout does not retry that; an IMMEDIATE one waits at its BEGIN.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	if err := connect(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return newStore(db, o), nil
}

// busyTimeout is the DSN's busy_timeout.
const busyTimeout = 5 * time.Second

// connect makes the first connection, retrying while it fails with SQLITE_BUSY, up to busyTimeout. The switch of a
// new file to WAL runs as each connection opens and does not wait on the busy timeout, so processes opening a new
// store at once can see busy here.
func connect(db *sql.DB) error {
	deadline := time.Now().Add(busyTimeout)
	for {
		err := db.Ping()
		if err == nil || !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// isBusy reports SQLITE_BUSY or one of its extended codes.
func isBusy(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && coded.Code()&0xff == sqlite3.SQLITE_BUSY
}

// OpenReadOnly opens an existing store for reading. It never creates the file or its folder, never changes a
// mode, and never migrates: a missing file is ErrNoStore and a schema version other than this binary's is
// ErrSchema. Every write through it fails.
func OpenReadOnly(path string, o Options) (*Store, error) {
	o, err := o.withDefaults()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("open %s: %w", path, ErrNoStore)
	} else if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	v, err := schemaVersion(context.Background(), db)
	if err == nil && v != len(migrations) {
		err = fmt.Errorf("%w: the file is version %d, this binary's is %d", ErrSchema, v, len(migrations))
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return newStore(db, o), nil
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

// Actor is who makes a write. A non-empty Session means an agent; a Run with no Session means the runner.
type Actor struct {
	Session string     `json:"session,omitempty"`
	Run     int64      `json:"run,omitempty"`
	TS      *time.Time `json:"ts,omitempty"` // set only by an outbox replay; nil → Options.Now()
}

// Who is agent when the actor names a session, runner when it names only a run, else user.
func (a Actor) Who() model.Who {
	switch {
	case a.Session != "":
		return model.WhoAgent
	case a.Run != 0:
		return model.WhoRunner
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
	// payload writes no event, and the write's apply is skipped.
	prepare func(tx *sql.Tx) (task int, data any, err error)
	// apply updates the state tables once the event has its id; nil for a journal event.
	apply func(tx *sql.Tx, ev model.Event) error
}

// append is the one write path: it scans every write's text, then runs each write's prepare, insert, and apply in
// order in one transaction, and returns the events written. Any error rolls back every write.
func (s *Store) append(ctx context.Context, a Actor, ws ...write) ([]model.Event, error) {
	var text []string
	for _, w := range ws {
		text = append(text, w.scan...)
	}
	if err := s.scanText(ctx, text); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var out []model.Event
	for _, w := range ws {
		task, data, err := w.prepare(tx)
		if err != nil {
			return nil, err
		}
		if data == nil {
			continue
		}
		ev := model.Event{
			TS:      s.stamp(a),
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
			return nil, err
		}
		if ev.ID, err = res.LastInsertId(); err != nil {
			return nil, err
		}
		if w.apply != nil {
			if err := w.apply(tx, ev); err != nil {
				return nil, err
			}
		}
		out = append(out, ev)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// stamp is the time of a's writes: its own TS (an outbox replay), else now, in UTC.
func (s *Store) stamp(a Actor) time.Time {
	if a.TS != nil {
		return a.TS.UTC()
	}
	return s.now().UTC()
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
