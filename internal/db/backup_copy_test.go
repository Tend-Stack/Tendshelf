package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupsWriteCopy(t *testing.T) {
	dir := t.TempDir()
	st := mustOpen(t, dir)
	defer st.Close()
	seedAll(t, st)
	now := time.Date(2026, 11, 3, 8, 0, 0, 5_000_000, time.UTC)
	c, err := WriteCopy(context.Background(), st, dir, now)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("tendshelf-copy-v%d-20261103T080000.005Z.db", SchemaVersion)
	if c.Name != want || c.Bytes == 0 || !c.At.Equal(now) {
		t.Fatalf("copy = %+v, want name %s", c, want)
	}
	path := filepath.Join(dir, "backups", want)
	if h := rawHeader(t, path); h != (Header{ApplicationID, SchemaVersion}) {
		t.Fatalf("header = %+v", h)
	}
	if err := verifyBackup(context.Background(), path, SchemaVersion); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", fi.Mode(), err)
	}
	list, err := ListCopies(dir)
	if err != nil || len(list) != 1 || list[0].Name != want {
		t.Fatalf("ListCopies = %+v, %v", list, err)
	}
	if none, err := ListCopies(t.TempDir()); err != nil || len(none) != 0 {
		t.Fatalf("missing folder = %+v, %v", none, err)
	}
}

func TestBackupsRotation(t *testing.T) {
	dir := t.TempDir()
	st := mustOpen(t, dir)
	defer st.Close()
	bdir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		t.Fatal(err)
	}
	var pre []string
	for i := 1; i <= 3; i++ {
		n := fmt.Sprintf("tendshelf-v%d-2025010%dT000000.000Z.db", SchemaVersion, i)
		pre = append(pre, n)
		if err := os.WriteFile(filepath.Join(bdir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		if _, err := WriteCopy(context.Background(), st, dir, base.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := ListCopies(dir)
	if err != nil || len(list) != CopyKeep {
		t.Fatalf("copies = %d, %v", len(list), err)
	}
	if list[0].At.Day() != 8 || list[len(list)-1].At.Day() != 2 {
		t.Fatalf("kept %v .. %v", list[0].At, list[len(list)-1].At)
	}
	for _, n := range pre {
		if b, err := os.ReadFile(filepath.Join(bdir, n)); err != nil || string(b) != "x" {
			t.Fatalf("pre-migration file %s changed: %v", n, err)
		}
	}
	// A pre-migration backup leaves the copies alone.
	if _, err := backupBefore(context.Background(), st.Writer(), dir, SchemaVersion, base.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	list, _ = ListCopies(dir)
	if len(list) != CopyKeep {
		t.Fatalf("copies after backupBefore = %d", len(list))
	}
}

func TestBackupsWhileWriting(t *testing.T) {
	dir := t.TempDir()
	st := mustOpen(t, dir)
	defer st.Close()
	seedAll(t, st)
	ctx := context.Background()
	tx, err := st.Writer().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value_json) VALUES ('during.copy', '1')`); err != nil {
		t.Fatal(err)
	}
	c, err := WriteCopy(ctx, st, dir, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("copy during an open writer transaction: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("writer commit after copy: %v", err)
	}
	d, err := sql.Open("sqlite", roDSN(filepath.Join(dir, "backups", c.Name)))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM settings WHERE key = 'during.copy'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("uncommitted row in copy: %d, %v", n, err)
	}
	var users int
	if err := d.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil || users == 0 {
		t.Fatalf("copy lost data: %d, %v", users, err)
	}
}

func TestBackupsFailureRemovesFile(t *testing.T) {
	dir := t.TempDir()
	st := mustOpen(t, dir)
	defer st.Close()
	bdir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bdir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(bdir, 0o700)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	if _, err := WriteCopy(context.Background(), st, dir, time.Now()); err == nil {
		t.Fatal("copy into an unwritable folder succeeded")
	}
	ents, _ := os.ReadDir(bdir)
	if len(ents) != 0 {
		t.Fatalf("left behind: %v", ents)
	}
}
