// Package db owns Tendshelf's SQLite database: opening, schema checks,
// migrations, pre-migration backups and ID generation.
//
// P0 placeholder: no SQLite driver is linked yet. The schema-check rules are
// implemented here as pure functions so the refusal behaviour is fixed and
// tested before the driver arrives.
//
// TODO(P0, needs operator approval of the download): add modernc.org/sqlite
// (pure Go; see docs/architecture.md, "Dependencies awaiting approval") and
// implement Open:
//  1. If the file exists, open it read-only (mode=ro, never immutable, so WAL
//     is read normally), read PRAGMA application_id and user_version, and call
//     CheckHeader. On error, close and return without writing.
//  2. Open writable with journal_mode=WAL, foreign_keys=ON, busy_timeout=5000,
//     synchronous=NORMAL.
//  3. If migrations are pending on an existing database, VACUUM INTO
//     <data>/backups/tendshelf-v<old>-<UTC>.db, verify it opens, keep three.
//  4. Apply each embedded migrations/NNNN_name.sql in its own transaction that
//     also sets user_version (and application_id on the first one).
package db

import (
	"errors"
	"fmt"
)

// ApplicationID is written to PRAGMA application_id ("TSHF").
const ApplicationID int32 = 0x54534846

// SchemaVersion is the newest schema this binary understands. It equals the
// number of the last embedded migration (0 until migration 0001 lands).
const SchemaVersion = 0

var (
	// ErrSchemaTooNew means the database was written by a newer Tendshelf.
	ErrSchemaTooNew = errors.New("database was created by a newer version of Tendshelf")
	// ErrNotTendshelf means the file is a SQLite database of another program.
	ErrNotTendshelf = errors.New("database does not belong to Tendshelf")
)

// Header is what Open reads from an existing database before any write.
type Header struct {
	ApplicationID int32
	UserVersion   int
}

// SchemaError reports a refused database with the versions involved.
type SchemaError struct {
	Found     int
	Supported int
	Err       error
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("%v (database schema %d, this binary supports up to %d)", e.Err, e.Found, e.Supported)
}

func (e *SchemaError) Unwrap() error { return e.Err }

// CheckHeader decides whether a database with header h may be opened
// writable by a binary that supports schemas up to supported.
//
// A brand-new file (application_id 0, user_version 0) is accepted and will be
// initialised. A file with another application's id, or with no id but a
// non-zero user_version, is refused. A newer schema is refused.
func CheckHeader(h Header, supported int) error {
	switch {
	case h.ApplicationID == 0 && h.UserVersion == 0:
		return nil
	case h.ApplicationID != ApplicationID:
		return &SchemaError{Found: h.UserVersion, Supported: supported, Err: ErrNotTendshelf}
	case h.UserVersion > supported:
		return &SchemaError{Found: h.UserVersion, Supported: supported, Err: ErrSchemaTooNew}
	case h.UserVersion < 0:
		return &SchemaError{Found: h.UserVersion, Supported: supported, Err: ErrNotTendshelf}
	}
	return nil
}

// Store is the database handle the services will receive. Placeholder until
// the driver lands; it will grow transaction helpers, not query methods (the
// queries live in the owning packages).
type Store interface {
	SchemaVersion() int
	Close() error
}
