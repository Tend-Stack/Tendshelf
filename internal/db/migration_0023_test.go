package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

func indexExists(t *testing.T, st *Store, name string) bool {
	t.Helper()
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

const jobInsert = `INSERT INTO jobs(id, kind, state, lease_until, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`

// TestMigration0023FromPrevious: a v22 database keeps its jobs and gains the two indexes.
func TestMigration0023FromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 22, dir)
	seed7(t, prev)
	if _, err := prev.Writer().Exec(jobInsert, "j1", "scan", "done", nil, 1, 1); err != nil {
		t.Fatal(err)
	}
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if !indexExists(t, st, "jobs_kind") || !indexExists(t, st, "jobs_finished") {
		t.Fatal("jobs indexes missing")
	}
	if countRows(t, st.Reader(), "jobs") != 2 || countRows(t, st.Reader(), "progress") != 1 {
		t.Error("existing rows were lost")
	}
}

// TestMigration0023FromEmpty: a fresh database has the indexes, and the lookup by kind uses one.
func TestMigration0023FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if !indexExists(t, st, "jobs_kind") || !indexExists(t, st, "jobs_finished") {
		t.Fatal("jobs indexes missing")
	}
	var plan string
	rows, err := st.Reader().Query(`EXPLAIN QUERY PLAN SELECT id FROM jobs WHERE kind = 'scan' ORDER BY created_at DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a, b, c int
		var d string
		if err := rows.Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		plan += d
	}
	if want := "jobs_kind"; !strings.Contains(plan, want) {
		t.Errorf("plan %q does not use %s", plan, want)
	}
}

func TestSweepRetention(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	old := now.Add(-JobsKeep - time.Hour).UnixMilli()
	fresh := now.Add(-time.Hour).UnixMilli()
	w := st.Writer()
	for _, j := range []struct {
		id, kind, state string
		lease           any
		at              int64
	}{
		{"old_done", "scan", "done", nil, old},
		{"old_failed", "scan", "failed", nil, old},
		{"new_done", "scan", "done", nil, fresh},
		{"old_queued", "scan", "queued", nil, old},
		{"old_running", "scan", "running", now.UnixMilli(), old},
		{"old_align_held", "align.run", "held", nil, old},
		{"new_align_held", "align.run", "held", nil, fresh},
	} {
		if _, err := w.Exec(jobInsert, j.id, j.kind, j.state, j.lease, j.at, j.at); err != nil {
			t.Fatal(err)
		}
	}
	oldAudit := now.Add(-AuditKeep - time.Hour).UnixMilli()
	for id, at := range map[string]int64{"a_old": oldAudit, "a_new": now.Add(-24 * time.Hour).UnixMilli()} {
		if _, err := w.Exec(`INSERT INTO audit_log(id, at, action, target) VALUES (?, ?, 'x', '')`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Sweep(context.Background(), st, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Jobs != 3 || got.Audit != 1 {
		t.Errorf("swept %+v, want 3 jobs and 1 audit row", got)
	}
	var left int
	_ = st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE id IN ('new_done','old_queued','old_running','new_align_held')`).Scan(&left)
	if left != 4 || countRows(t, st.Reader(), "audit_log") != 1 {
		t.Errorf("kept %d of the 4 jobs that must stay; audit rows %d", left, countRows(t, st.Reader(), "audit_log"))
	}
}
