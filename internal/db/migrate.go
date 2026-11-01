package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migration is one numbered, forward-only schema step.
type migration struct {
	Version int
	Name    string
	SQL     string
}

var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// loadMigrations reads migrations/NNNN_name.sql from fsys. Versions must run
// 1..n with no gaps.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration file %q does not match NNNN_name.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(fsys, "migrations/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		out = append(out, migration{Version: v, Name: m[2], SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migrations are not contiguous: expected version %d, found %d", i+1, m.Version)
		}
	}
	return out, nil
}

// applyMigration runs one migration and sets user_version (and application_id
// on version 1) in the same transaction.
func applyMigration(ctx context.Context, w *sql.DB, m migration) error {
	err := func() error {
		tx, err := w.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback() //nolint:errcheck // no-op after commit
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
			return err
		}
		if m.Version == 1 {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id = %d", ApplicationID)); err != nil {
				return err
			}
		}
		return tx.Commit()
	}()
	if err != nil {
		return fmt.Errorf("apply migration %04d_%s: %w", m.Version, m.Name, err)
	}
	return nil
}
