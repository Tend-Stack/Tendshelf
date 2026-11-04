package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// CopyKeep is how many scheduled copies stay in the backups folder.
const CopyKeep = 7

// copyName matches scheduled copies. They sit beside the pre-migration backups but rotate on their own,
// and neither rotation ever touches the other's files.
var copyName = regexp.MustCompile(`^tendshelf-copy-v(\d+)-(\d{8}T\d{6}\.\d{3}Z)\.db$`)

// Copy is one scheduled or manual copy of the database.
type Copy struct {
	Name  string
	At    time.Time
	Bytes int64
}

// WriteCopy writes a verified VACUUM INTO copy to <dataDir>/backups/tendshelf-copy-v<n>-<UTC>.db and keeps
// the newest CopyKeep copies. It reads through its own read-only connection, so it never holds the
// store's writer; a copy that fails to verify is removed.
func WriteCopy(ctx context.Context, st *Store, dataDir string, now time.Time) (Copy, error) {
	dir := filepath.Join(dataDir, backupDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Copy{}, err
	}
	name := fmt.Sprintf("tendshelf-copy-v%d-%s.db", st.SchemaVersion(), now.UTC().Format(backupStamp))
	dest := filepath.Join(dir, name)
	fail := func(err error) (Copy, error) {
		os.Remove(dest)
		return Copy{}, err
	}
	src, err := sql.Open("sqlite", roDSN(st.Path())+"&_pragma=busy_timeout(5000)")
	if err != nil {
		return Copy{}, err
	}
	_, err = src.ExecContext(ctx, "VACUUM INTO ?", dest)
	src.Close()
	if err != nil {
		return fail(fmt.Errorf("write copy: %w", err))
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		return fail(err)
	}
	if err := verifyBackup(ctx, dest, st.SchemaVersion()); err != nil {
		return fail(fmt.Errorf("verify copy: %w", err))
	}
	c := Copy{Name: name, At: now.UTC()}
	if fi, err := os.Stat(dest); err == nil {
		c.Bytes = fi.Size()
	}
	if err := rotateMatching(dir, copyName, CopyKeep); err != nil {
		return c, err
	}
	return c, nil
}

// ListCopies lists the copies in the backups folder, newest first. A missing folder is an empty list.
func ListCopies(dataDir string) ([]Copy, error) {
	dir := filepath.Join(dataDir, backupDirName)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Copy
	for _, e := range entries {
		m := copyName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		at, err := time.Parse(backupStamp, m[2])
		if err != nil {
			continue
		}
		c := Copy{Name: e.Name(), At: at}
		if fi, err := e.Info(); err == nil {
			c.Bytes = fi.Size()
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}
