package db

import (
	"context"
	"testing"
	"time"
)

// TestMigration0015UploadSplit: a v14 database keeps its rows and gains the column.
func TestMigration0015UploadSplit(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:14], supported: 14, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seed7(t, prev)
	prev.Close()
	st := mustOpen(t, dir)
	defer st.Close()
	if countRows(t, st.Reader(), "books") != 1 {
		t.Error("books lost its row")
	}
	if _, err := st.Reader().Exec(`SELECT split_from FROM uploads`); err != nil {
		t.Errorf("column missing: %v", err)
	}
}

// TestMigration0015FromEmpty: a new database has the column.
func TestMigration0015FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if _, err := st.Reader().Exec(`SELECT split_from FROM uploads`); err != nil {
		t.Errorf("column missing: %v", err)
	}
}
