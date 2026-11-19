package upload

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// Removals of what Add books placed are held for 30 days like a hand-added book (decision 0045 addendum).

// heldTree is tree without the holding folder.
func heldTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, p := range tree(t, dir) {
		if strings.HasPrefix(p, HoldingDir+"/") || p == HoldingDir+"/" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// holdingFiles lists the files held for one removal, relative to its holding folder.
func holdingFiles(t *testing.T, root, rid string) []string {
	t.Helper()
	prefix := HoldingDir + "/" + rid + "/"
	var out []string
	for _, p := range tree(t, root) {
		if strings.HasPrefix(p, prefix) && !strings.HasSuffix(p, "/") {
			out = append(out, strings.TrimPrefix(p, prefix))
		}
	}
	return out
}

func (e *env) onlyRemoval() (id, source, state string) {
	e.t.Helper()
	if n := e.count(`SELECT count(*) FROM removals`); n != 1 {
		e.t.Fatalf("removals rows = %d, want 1", n)
	}
	if err := e.st.Reader().QueryRow(`SELECT id, source, state FROM removals`).Scan(&id, &source, &state); err != nil {
		e.t.Fatal(err)
	}
	return
}

func TestUploadHeldWholeBookHeldAndPutBack(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	book := filepath.Join(e.root, "Ann Author", "Great Title")
	orig := map[string][]byte{}
	for _, n := range []string{"01.mp3", "02.mp3", "cover.jpg"} {
		b, err := os.ReadFile(filepath.Join(book, n))
		if err != nil {
			t.Fatal(err)
		}
		orig[n] = b
	}
	res, err := e.svc.Remove(bg, me, bookID, "")
	if err != nil {
		t.Fatal(err)
	}
	rid, source, state := e.onlyRemoval()
	if res.RemovalID != rid || source != SourceUpload || state != RemovalHeld {
		t.Fatalf("result %+v, removal %s %s %s", res, rid, source, state)
	}
	if got := holdingFiles(t, e.root, rid); !reflect.DeepEqual(got, []string{"Ann Author/Great Title/01.mp3", "Ann Author/Great Title/02.mp3", "Ann Author/Great Title/cover.jpg"}) {
		t.Errorf("holding = %v", got)
	}
	if got := heldTree(t, e.root); len(got) != 0 {
		t.Errorf("the folders it made are still there: %v", got)
	}
	rm, err := e.svc.Removal(bg, rid)
	if err != nil {
		t.Fatal(err)
	}
	if want := e.svc.nowMs() + int64(RemovedKeepDays)*24*3600*1000; rm.PurgeAfter < want-60_000 || rm.PurgeAfter > want+60_000 {
		t.Errorf("purge_after = %d, want about %d", rm.PurgeAfter, want)
	}
	if booksCount(t, e) != 0 || e.count(`SELECT count(*) FROM upload_books`) != 0 {
		t.Error("book rows or who-added-it are still there")
	}
	if n := e.count(`SELECT count(*) FROM upload_paths WHERE kind = 'file' AND state = 'removed'`); n != 3 {
		t.Errorf("removed ledger files = %d, want 3", n)
	}
	if n := e.count(`SELECT count(*) FROM removal_ledger WHERE removal_id = ?`, rid); n == 0 {
		t.Error("no ledger snapshot")
	}
	if n := e.count(`SELECT count(*) FROM removal_upload_books WHERE removal_id = ?`, rid); n != 1 {
		t.Errorf("owner snapshot rows = %d, want 1", n)
	}

	scans := len(e.enq.of(scan.KindScan))
	out, err := e.svc.RestoreRemoval(bg, me, rid)
	if err != nil || len(out.Restored) != 3 || len(out.Kept) != 0 {
		t.Fatalf("restore = %+v, %v", out, err)
	}
	for n, want := range orig {
		got, err := os.ReadFile(filepath.Join(book, n))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s not back byte-identical: %v", n, err)
		}
	}
	if got := e.count(`SELECT count(*) FROM upload_paths WHERE state = 'present'`); got < 3 {
		t.Errorf("present ledger rows = %d", got)
	}
	if n := e.count(`SELECT count(*) FROM upload_books WHERE added_by = ?`, me); n != 1 {
		t.Errorf("upload_books after put back = %d, want 1", n)
	}
	if len(e.enq.of(scan.KindScan)) != scans+1 {
		t.Error("no scan queued")
	}
	if n := e.count(`SELECT count(*) FROM removal_ledger`) + e.count(`SELECT count(*) FROM removal_upload_books`); n != 0 {
		t.Errorf("snapshot rows left = %d", n)
	}
	// A rescan brings the book back as removable, and removable by the person who added it.
	e.scanLast()
	var again string
	if err := e.st.Reader().QueryRow(`SELECT id FROM books`).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if !e.svc.Removable(bg, again) || !e.svc.RemovableBy(bg, again, me) {
		t.Error("the restored book is not removable again")
	}
}

func TestUploadHeldContributorOwnHeld(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	res, err := e.svc.Remove(bg, me, bookID, me)
	if err != nil || res.RemovalID == "" {
		t.Fatalf("res %+v, err %v", res, err)
	}
	if e.count(`SELECT count(*) FROM removals WHERE removed_by = ?`, me) != 1 {
		// removed_by only holds a known user; the test env has none, so look at the row itself.
		_, source, state := e.onlyRemoval()
		if source != SourceUpload || state != RemovalHeld {
			t.Errorf("removal = %s %s", source, state)
		}
	}
}

func TestUploadHeldContributorOthersRefused(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	if _, err := e.st.Writer().Exec(`UPDATE upload_books SET added_by = 'us_other'`); err != nil {
		t.Fatal(err)
	}
	before := tree(t, e.root)
	_, err := e.svc.Remove(bg, me, bookID, me)
	var ue *Error
	if !errors.As(err, &ue) || ue.Status != 409 || ue.Code != "not_theirs" {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(before, tree(t, e.root)) || e.count(`SELECT count(*) FROM removals`) != 0 {
		t.Error("something moved or a removal was recorded")
	}
}

func TestUploadHeldChangedFileStays(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	book := filepath.Join(e.root, "Ann Author", "Great Title")
	cover := filepath.Join(book, "cover.jpg")
	if err := os.WriteFile(cover, []byte("my own cover, different size"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Remove(bg, me, bookID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Kept, []string{cover}) || len(res.Deleted) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if got := holdingFiles(t, e.root, res.RemovalID); len(got) != 2 {
		t.Errorf("holding = %v", got)
	}
	if b, _ := os.ReadFile(cover); string(b) != "my own cover, different size" {
		t.Error("their cover changed or moved")
	}
	if booksCount(t, e) != 0 {
		t.Error("the book stayed although no audio is left")
	}
	out, err := e.svc.RestoreRemoval(bg, me, res.RemovalID)
	if err != nil || len(out.Restored) != 2 || len(out.Kept) != 0 {
		t.Fatalf("restore = %+v, %v", out, err)
	}
	if n := e.count(`SELECT count(*) FROM upload_books`); n != 1 {
		t.Errorf("who-added-it after a full put back = %d", n)
	}
}

func TestUploadHeldChangedAudioKeepsBook(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	track := filepath.Join(e.root, "Ann Author", "Great Title", "01.mp3")
	data, _ := os.ReadFile(track)
	data[len(data)-1] ^= 0xff
	data[0] = 'T'
	if err := os.WriteFile(track, data, 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(track, future, future); err != nil {
		t.Fatal(err)
	}
	scans := len(e.enq.of(scan.KindScan))
	res, err := e.svc.Remove(bg, me, bookID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Kept) != 1 || len(res.Deleted) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if booksCount(t, e) != 1 {
		t.Error("the book row went although an audio file stayed")
	}
	if len(e.enq.of(scan.KindScan)) != scans+1 {
		t.Error("no scoped scan queued")
	}
	if _, _, state := e.onlyRemoval(); state != RemovalHeld {
		t.Errorf("state = %s", state)
	}
}

func TestUploadHeldAlreadyGoneFile(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	book := filepath.Join(e.root, "Ann Author", "Great Title")
	for _, n := range []string{"01.mp3", "02.mp3", "cover.jpg"} {
		if err := os.Remove(filepath.Join(book, n)); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.svc.Remove(bg, me, bookID, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovalID != "" || len(res.Deleted) != 0 {
		t.Errorf("result = %+v: nothing was moved", res)
	}
	if e.count(`SELECT count(*) FROM removals`) != 0 {
		t.Error("a removal row for nothing")
	}
	if booksCount(t, e) != 0 {
		t.Error("the book stayed")
	}
	if n := e.count(`SELECT count(*) FROM upload_paths WHERE kind = 'file' AND state = 'removed'`); n != 3 {
		t.Errorf("removed ledger rows = %d", n)
	}
}

func TestUploadHeldPutBackPlaceTaken(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	book := filepath.Join(e.root, "Ann Author", "Great Title")
	res, err := e.svc.Remove(bg, me, bookID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(book, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(book, "cover.jpg"), []byte("new cover"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e.svc.RestoreRemoval(bg, me, res.RemovalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Kept) != 1 || len(out.Restored) != 2 {
		t.Fatalf("restore = %+v", out)
	}
	if b, _ := os.ReadFile(filepath.Join(book, "cover.jpg")); string(b) != "new cover" {
		t.Error("Put back overwrote a file")
	}
	if n := e.count(`SELECT count(*) FROM upload_books`); n != 0 {
		t.Errorf("who-added-it returned although a file did not come back: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM upload_paths WHERE path LIKE '%cover.jpg' AND state = 'present'`); n != 0 {
		t.Errorf("the cover's ledger row was re-presented: %d", n)
	}
	if _, _, state := e.onlyRemoval(); state != RemovalHeld {
		t.Errorf("state = %s: it stays held while a file waits", state)
	}
}

func TestUploadHeldReadOnlyFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	if err := os.Chmod(e.root, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(e.root, 0o755)
	before := heldTree(t, e.root)
	_, err := e.svc.Remove(bg, me, bookID, "")
	var ue *Error
	if !errors.As(err, &ue) || ue.Status != 409 || ue.Code != CodeReadOnly {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(before, heldTree(t, e.root)) {
		t.Error("something moved")
	}
	if _, _, state := e.onlyRemoval(); state != RemovalFailed {
		t.Errorf("state = %s", state)
	}
	if booksCount(t, e) != 1 {
		t.Error("the book went")
	}
}

func TestUploadHeldCrashMidMoveAndStuckPlanned(t *testing.T) {
	boom := errors.New("simulated crash")
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	e := newEnv(t, func(c *Config) { c.Now = func() time.Time { return now } })
	_, bookID := placedBook(t, e)
	before := heldTree(t, e.root)
	e.svc.crash = func(p string) error {
		if p == "remove_files:after_move:0" {
			return boom
		}
		return nil
	}
	if _, err := e.svc.Remove(bg, me, bookID, ""); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	e.svc.crash = func(string) error { return nil }
	if _, _, state := e.onlyRemoval(); state != RemovalMoving {
		t.Fatalf("state = %s, want moving", state)
	}
	// The mover is dead, so the sweep takes its lock at once and puts the files back.
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	rid, _, state := e.onlyRemoval()
	if state != RemovalFailed {
		t.Fatalf("state = %s, want failed", state)
	}
	var code string
	if err := e.st.Reader().QueryRow(`SELECT error_code FROM removals WHERE id = ?`, rid).Scan(&code); err != nil || code != CodeInterrupted {
		t.Errorf("error_code = %q, %v", code, err)
	}
	if !reflect.DeepEqual(before, heldTree(t, e.root)) {
		t.Errorf("tree = %v, want %v", heldTree(t, e.root), before)
	}
	if booksCount(t, e) != 1 {
		t.Error("the book rows went")
	}

	// A planned upload removal that nothing runs fails as stuck once it is an hour old.
	if _, err := e.st.Writer().Exec(`INSERT INTO removals(id, library_id, folder_id, book_id, title, root_path, mode, state, source, plan_hash, created_at, updated_at)
  VALUES ('rmv_stuck', ?, ?, 'bk_none', 'T', ?, 'files', 'planned', 'upload', ?, ?, ?)`,
		e.lib.ID, e.folder.ID, filepath.Join(e.root, "x"), strings.Repeat("0", 64), now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	var st string
	_ = e.st.Reader().QueryRow(`SELECT state FROM removals WHERE id = 'rmv_stuck'`).Scan(&st)
	if st != RemovalPlanned {
		t.Errorf("fresh planned row: state = %s", st)
	}
	now = now.Add(2 * time.Hour)
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Reader().QueryRow(`SELECT state FROM removals WHERE id = 'rmv_stuck'`).Scan(&st); err != nil || st != RemovalFailed {
		t.Errorf("old planned row: state = %s, %v", st, err)
	}
}

func TestUploadHeldBusy(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	if _, err := e.st.Writer().Exec(`INSERT INTO removals(id, library_id, folder_id, book_id, title, root_path, mode, state, source, plan_hash, created_at, updated_at)
  VALUES ('rmv_busy', ?, ?, ?, 'T', ?, 'files', 'planned', 'upload', ?, 1, ?)`,
		e.lib.ID, e.folder.ID, bookID, filepath.Join(e.root, "x"), strings.Repeat("0", 64), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	before := heldTree(t, e.root)
	_, err := e.svc.Remove(bg, me, bookID, "")
	var ue *Error
	if !errors.As(err, &ue) || ue.Status != 409 || ue.Code != CodeBusy {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(before, heldTree(t, e.root)) {
		t.Error("something moved")
	}
}

func TestUploadHeldPurgeAfter30Days(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	e := newEnv(t, func(c *Config) { c.Now = func() time.Time { return now } })
	_, bookID := placedBook(t, e)
	res, err := e.svc.Remove(bg, me, bookID, "")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * 24 * time.Hour)
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if _, _, state := e.onlyRemoval(); state != RemovalPurged {
		t.Fatalf("state = %s", state)
	}
	if got := tree(t, e.root); len(got) != 0 {
		t.Errorf("holding folder left: %v", got)
	}
	if n := e.count(`SELECT count(*) FROM removal_ledger`) + e.count(`SELECT count(*) FROM removal_upload_books`); n != 0 {
		t.Errorf("snapshot rows left = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM upload_paths WHERE kind = 'file' AND state = 'removed'`); n != 3 {
		t.Errorf("ledger files = %d, want 3 still removed (%s)", n, res.RemovalID)
	}
}

// A book of 300 small files is removed inside the request; this measures how long that takes.
func TestUploadHeldLargeBookTime(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	book := filepath.Join(e.root, "Ann Author", "Great Title")
	// Add 300 more files to the book as if the upload had placed them: files, and ledger rows that match.
	var uploadID string
	if err := e.st.Reader().QueryRow(`SELECT upload_id FROM upload_paths WHERE kind = 'file' LIMIT 1`).Scan(&uploadID); err != nil {
		t.Fatal(err)
	}
	var folderID string
	if err := e.st.Reader().QueryRow(`SELECT library_folder_id FROM upload_paths LIMIT 1`).Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	setupStarted := time.Now()
	for i := 0; i < 300; i++ {
		name := fmt.Sprintf("extra-%03d.jpg", i)
		p := filepath.Join(book, name)
		if err := os.WriteFile(p, []byte(fmt.Sprintf("file %d %s", i, strings.Repeat("x", 100))), 0o644); err != nil {
			t.Fatal(err)
		}
		fi, _ := os.Stat(p)
		f, _ := os.Open(p)
		quick, _, _ := media.QuickHashFile(f)
		f.Close()
		if _, err := e.st.Writer().Exec(`INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, size, mtime_ns, quick_hash, created_at, updated_at)
  VALUES (?, ?, ?, ?, 'file', 'present', ?, ?, ?, 1, 1)`, fmt.Sprintf("upp_big%03d", i), uploadID, folderID, p, fi.Size(), fi.ModTime().UnixNano(), quick); err != nil {
			t.Fatal(err)
		}
	}
	setup := time.Since(setupStarted)
	started := time.Now()
	res, err := e.svc.Remove(bg, me, bookID, "")
	took := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("removing a book of %d files took %s (setup %s)", len(res.Deleted), took, setup)
	if len(res.Deleted) != 303 || len(res.Kept) != 0 {
		t.Errorf("result: %d deleted, %d kept", len(res.Deleted), len(res.Kept))
	}
	// Removal does per-file durable work (a rename and a database update each) that setup does not, so
	// it runs about 8 times slower than setup on an idle machine. The limit is therefore generous and
	// scales with the machine: 50 times the setup time, never under 30 s. A loaded runner slows both
	// together; a removal that goes quadratic or syncs far more per file still fails. The structural
	// guarantee (all 303 files moved, none kept) is asserted above.
	limit := 50 * setup
	if limit < 30*time.Second {
		limit = 30 * time.Second
	}
	if took > limit {
		t.Errorf("PLAN GAP: 300-file removal took %s, limit %s (setup took %s)", took, limit, setup)
	}
}
