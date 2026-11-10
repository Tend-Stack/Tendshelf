// Package db owns Tendshelf's SQLite database: opening, schema checks,
// migrations, pre-migration backups and ID generation.
//
// Open follows the rules in AGENTS.md ("Database and migrations"): the header
// is checked before any write (a refused file stays byte-identical), the
// database is opened in WAL mode through one single-connection writer pool and
// a separate query-only reader pool, an existing database is backed up with
// VACUUM INTO before it is migrated, and each embedded migration commits
// together with its user_version. Only this package imports the SQLite driver.
package db

import (
	"errors"
	"fmt"
)

// ApplicationID is written to PRAGMA application_id ("TSHF").
const ApplicationID int32 = 0x54534846

// SchemaVersion is the newest schema this binary understands. It equals the
// number of the last embedded migration.
const SchemaVersion = 41

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
