package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

const (
	backupDirName = "backups"
	backupKeep    = 3
	backupStamp   = "20060102T150405.000Z"
)

var backupName = regexp.MustCompile(`^tendshelf-v(\d+)-(\d{8}T\d{6}\.\d{3}Z)\.db$`)

// backupBefore writes a verified VACUUM INTO copy of the database to
// <dataDir>/backups/tendshelf-v<old>-<UTC>.db and keeps the newest three. The
// copy counts only after its header and quick_check pass; on failure it is
// removed and nothing is rotated.
func backupBefore(ctx context.Context, w *sql.DB, dataDir string, old int, now time.Time) (string, error) {
	dir := filepath.Join(dataDir, backupDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, fmt.Sprintf("tendshelf-v%d-%s.db", old, now.UTC().Format(backupStamp)))
	if _, err := w.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		os.Remove(dest)
		return "", fmt.Errorf("write backup: %w", err)
	}
	if err := verifyBackup(ctx, dest, old); err != nil {
		os.Remove(dest)
		return "", fmt.Errorf("verify backup: %w", err)
	}
	if err := rotateBackups(dir, backupKeep); err != nil {
		return dest, err
	}
	return dest, nil
}

func verifyBackup(ctx context.Context, path string, old int) error {
	d, err := sql.Open("sqlite", roDSN(path))
	if err != nil {
		return err
	}
	defer d.Close()
	var id int32
	var uv int
	if err := d.QueryRowContext(ctx, "PRAGMA application_id").Scan(&id); err != nil {
		return err
	}
	if err := d.QueryRowContext(ctx, "PRAGMA user_version").Scan(&uv); err != nil {
		return err
	}
	if id != ApplicationID || uv != old {
		return fmt.Errorf("backup header is (%#x, %d), want (%#x, %d)", id, uv, ApplicationID, old)
	}
	var res string
	if err := d.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("backup quick_check: %s", res)
	}
	return nil
}

// rotateBackups removes all but the newest keep backups, sorted by the
// timestamp in the name. Files that do not match the backup name are never
// touched.
func rotateBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type bk struct {
		name string
		at   time.Time
	}
	var all []bk
	for _, e := range entries {
		m := backupName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		at, err := time.Parse(backupStamp, m[2])
		if err != nil {
			continue
		}
		all = append(all, bk{e.Name(), at})
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].at.Equal(all[j].at) {
			return all[i].at.After(all[j].at)
		}
		return all[i].name > all[j].name
	})
	for i := keep; i < len(all); i++ {
		if err := os.Remove(filepath.Join(dir, all[i].name)); err != nil {
			return err
		}
	}
	return nil
}
