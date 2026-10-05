package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations[i] takes the schema from user_version i to i+1. A shipped entry is never edited: a change is a
// new entry.
var migrations = []string{
	`CREATE TABLE events(
		id INTEGER PRIMARY KEY,
		ts TEXT NOT NULL,
		session TEXT NOT NULL DEFAULT '',
		who TEXT NOT NULL,
		kind TEXT NOT NULL,
		task INTEGER NULL,
		data JSON NOT NULL,
		tags JSON NULL,
		run INTEGER NULL,
		v INTEGER NOT NULL
	);
	CREATE INDEX events_task ON events(task, id);
	CREATE INDEX events_session ON events(session, id);
	CREATE INDEX events_kind ON events(kind, id);
	CREATE TABLE tasks(
		number INTEGER PRIMARY KEY,
		title TEXT NOT NULL,
		notes TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		project TEXT NOT NULL DEFAULT '',
		thread TEXT NOT NULL DEFAULT '',
		archived INTEGER NOT NULL DEFAULT 0,
		root TEXT NOT NULL DEFAULT '',
		isolation TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		created_ts TEXT NOT NULL,
		updated_ts TEXT NOT NULL
	);
	CREATE TABLE steps(
		task INTEGER NOT NULL,
		short_id TEXT NOT NULL,
		text TEXT NOT NULL,
		done INTEGER NOT NULL DEFAULT 0,
		pos INTEGER NOT NULL,
		PRIMARY KEY(task, short_id)
	);
	CREATE TABLE runs(
		id INTEGER PRIMARY KEY,
		task INTEGER NOT NULL,
		state TEXT NOT NULL,
		root TEXT NOT NULL DEFAULT '',
		isolation TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		session TEXT NOT NULL DEFAULT '',
		workspace TEXT NOT NULL DEFAULT '',
		pane TEXT NOT NULL DEFAULT '',
		started_ts TEXT NOT NULL,
		ended_ts TEXT NULL,
		exit INTEGER NULL
	);
	CREATE TABLE sessions(
		id TEXT PRIMARY KEY,
		continues TEXT NULL
	);`,
}

func schemaVersion(ctx context.Context, q querier) (int, error) {
	var v int
	err := q.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v)
	return v, err
}

// migrate applies the migrations the file has not had, in one transaction. A file that is current costs one read
// and takes no write lock; a file that is behind is read again inside the transaction, since another process may
// have migrated it meanwhile.
func migrate(ctx context.Context, db *sql.DB) error {
	v, err := schemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if v == len(migrations) {
		return nil
	}
	if err := checkVersion(v); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if v, err = schemaVersion(ctx, tx); err != nil {
		return err
	}
	if v == len(migrations) {
		return nil
	}
	if err := checkVersion(v); err != nil {
		return err
	}
	for _, m := range migrations[v:] {
		if _, err := tx.ExecContext(ctx, m); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}

func checkVersion(v int) error {
	if v > len(migrations) {
		return fmt.Errorf("the store is schema version %d; this desk knows up to %d", v, len(migrations))
	}
	return nil
}
