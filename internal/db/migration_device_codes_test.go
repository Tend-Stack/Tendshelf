package db

import "testing"

// TestMigrationDeviceCodesFromPrevious: a v30 database keeps its sessions, with no device bound, and
// gains the device_codes table.
func TestMigrationDeviceCodesFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 30, dir)
	seed7(t, prev)
	before := countRows(t, prev.Reader(), "sessions")
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if got := countRows(t, st.Reader(), "sessions"); got != before || got == 0 {
		t.Errorf("session rows = %d, want %d", got, before)
	}
	var bound int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM sessions WHERE device_id IS NOT NULL`).Scan(&bound); err != nil || bound != 0 {
		t.Errorf("bound sessions = %d (err %v), want 0", bound, err)
	}
	if got := countRows(t, st.Reader(), "device_codes"); got != 0 {
		t.Errorf("device_codes rows = %d, want 0", got)
	}
	if n := indexCount(t, st, "device_codes_session"); n != 1 {
		t.Errorf("device_codes_session indexes = %d, want 1", n)
	}
}

// TestMigrationDeviceCodesFromEmpty: a new database has the table, the index and the column.
func TestMigrationDeviceCodesFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if n := indexCount(t, st, "device_codes_session"); n != 1 {
		t.Errorf("device_codes_session indexes = %d, want 1", n)
	}
	if _, err := st.Reader().Exec(`SELECT device_id FROM sessions LIMIT 1`); err != nil {
		t.Errorf("sessions.device_id: %v", err)
	}
}

// TestMigrationDeviceCodesChecks: a code hash or challenge of the wrong length is refused by the table.
func TestMigrationDeviceCodesChecks(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if _, err := st.Writer().Exec(`INSERT INTO device_codes (code_hash, session_hash, challenge, created_at, expires_at)
		VALUES (x'00', x'00', x'00', 1, 2)`); err == nil {
		t.Error("a short code hash was accepted")
	}
}
