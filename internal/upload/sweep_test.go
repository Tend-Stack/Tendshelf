package upload

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepExpiresStaleReceivingUploads(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	e := newEnv(t, func(c *Config) { c.Now = func() time.Time { return now } })
	old, _ := oneFile(t, e, 64)
	fresh, _ := oneFile(t, e, 64)
	queuedV, _ := oneFile(t, e, 64)
	if _, err := e.st.Writer().Exec(`UPDATE uploads SET updated_at = ? WHERE id = ?`,
		now.Add(-25*time.Hour).UnixMilli(), old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Writer().Exec(`UPDATE uploads SET state = 'queued' WHERE id = ?`, queuedV.ID); err != nil {
		t.Fatal(err)
	}
	// A staging folder with no row, one old and one new.
	orphanOld := filepath.Join(e.svc.incomingDir(), "upl_orphanold")
	orphanNew := filepath.Join(e.svc.incomingDir(), "upl_orphannew")
	for _, d := range []string{orphanOld, orphanNew} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	past := now.Add(-30 * time.Hour)
	if err := os.Chtimes(orphanOld, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphanNew, now, now); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if u := e.upload(old.ID); u.State != StateExpired {
		t.Errorf("stale upload is %s", u.State)
	}
	if _, err := os.Stat(e.svc.stagingDir(old.ID)); !os.IsNotExist(err) {
		t.Errorf("stale staging kept: %v", err)
	}
	if u := e.upload(fresh.ID); u.State != StateReceiving {
		t.Errorf("fresh upload is %s", u.State)
	}
	if _, err := os.Stat(e.svc.stagingDir(fresh.ID)); err != nil {
		t.Errorf("fresh staging gone: %v", err)
	}
	if u := e.upload(queuedV.ID); u.State != StateQueued {
		t.Errorf("a queued upload was expired: %s", u.State)
	}
	if _, err := os.Stat(e.svc.stagingDir(queuedV.ID)); err != nil {
		t.Errorf("a queued upload lost its staging: %v", err)
	}
	if _, err := os.Stat(orphanOld); !os.IsNotExist(err) {
		t.Errorf("old orphan kept: %v", err)
	}
	if _, err := os.Stat(orphanNew); err != nil {
		t.Errorf("new orphan removed: %v", err)
	}
	// An expired upload cannot receive more.
	_, err := e.svc.WriteChunk(bg, me, old.ID, old.Files[0].ID, 0, "", nil)
	wantErr(t, err, 410, "expired")
}

func TestSweepPrunesOldEndedRowsButNotTheLedger(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	e := newEnv(t, func(c *Config) { c.Now = func() time.Time { return now } })
	v := e.send("My Book", greatBook(t))
	e.mustPlace(v.ID)
	recent, _ := oneFile(t, e, 64)
	if _, err := e.st.Writer().Exec(`UPDATE uploads SET updated_at = ? WHERE id = ?`, now.Add(-31*24*time.Hour).UnixMilli(), v.ID); err != nil {
		t.Fatal(err)
	}
	ledger := countRows(t, e, "upload_paths")
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.loadUpload(bg, v.ID); err == nil {
		t.Error("old placed upload kept")
	}
	if n := countRows(t, e, "upload_files"); n != 1 {
		t.Errorf("upload_files = %d (cascade)", n)
	}
	if n := countRows(t, e, "upload_paths"); n != ledger || ledger == 0 {
		t.Errorf("ledger rows %d -> %d: the ledger must outlive the upload", ledger, n)
	}
	if _, err := e.svc.loadUpload(bg, recent.ID); err != nil {
		t.Errorf("recent upload gone: %v", err)
	}
}
