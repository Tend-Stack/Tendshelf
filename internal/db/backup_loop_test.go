package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A crash-loop backs up the same schema on every start. It must not rotate away the backups of
// earlier versions, the only way back.
func TestBackupBeforeCrashLoopKeepsEarlierVersions(t *testing.T) {
	dir := t.TempDir()
	st := mustOpen(t, dir)
	defer st.Close()
	bdir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"tendshelf-v20-20260101T000000.000Z.db", "tendshelf-v21-20260102T000000.000Z.db"} {
		if err := os.WriteFile(filepath.Join(bdir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		if _, err := backupBefore(context.Background(), st.Writer(), dir, SchemaVersion, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(bdir)
	if len(entries) != 5 {
		t.Fatalf("%d backups, want 5 (v20, v21 and the newest three of the current version)", len(entries))
	}
	for _, n := range []string{"tendshelf-v20-20260101T000000.000Z.db", "tendshelf-v21-20260102T000000.000Z.db"} {
		if _, err := os.Stat(filepath.Join(bdir, n)); err != nil {
			t.Errorf("%s was rotated away", n)
		}
	}
}
