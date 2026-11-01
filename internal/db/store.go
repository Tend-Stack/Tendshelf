package db

import (
	"context"
	"database/sql"
	"errors"
)

// Store is the database handle the services receive. It owns two pools: a
// writer with exactly one connection (every write is serialised and starts
// with BEGIN IMMEDIATE, so a write never hits SQLITE_BUSY on a lock upgrade)
// and a query-only reader pool. It carries no queries; those live in the
// packages that own the tables.
type Store struct {
	w, r    *sql.DB
	path    string
	version int
}

// Writer returns the single-connection write pool.
func (s *Store) Writer() *sql.DB { return s.w }

// Reader returns the query-only pool.
func (s *Store) Reader() *sql.DB { return s.r }

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// SchemaVersion returns the schema version after Open finished migrating.
func (s *Store) SchemaVersion() int { return s.version }

// Close closes the reader, then the writer.
func (s *Store) Close() error {
	return errors.Join(s.r.Close(), s.w.Close())
}

// WriteTx runs fn in an immediate transaction on the writer. It rolls back on
// an error or a panic (and re-panics), and commits otherwise.
func (s *Store) WriteTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
