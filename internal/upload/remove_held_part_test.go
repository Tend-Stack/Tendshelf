package upload

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// The removal of one part of a combined book, and of a text-only book, is held like a whole book (decision 0045 addendum).

func TestUploadHeldTextPartHeld(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, fileID, epub, _ := combinedBook(t, e)
	owners, scans := e.count(`SELECT count(*) FROM upload_books`), len(e.enq.of(scan.KindScan))
	res, err := e.svc.RemoveEbookFile(bg, me, bookID, fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovalID == "" || len(res.Deleted) != 1 || res.Deleted[0] != epub {
		t.Fatalf("result = %+v", res)
	}
	rel, _ := filepath.Rel(e.root, epub)
	if got := holdingFiles(t, e.root, res.RemovalID); !reflect.DeepEqual(got, []string{filepath.ToSlash(rel)}) {
		t.Errorf("holding = %v", got)
	}
	if countRows(t, e, "ebook_files") != 0 || countRows(t, e, "audio_files") != 2 || booksCount(t, e) != 1 {
		t.Error("rows: the ebook row should go and the audio and book stay")
	}
	if e.count(`SELECT count(*) FROM upload_books`) != owners || len(e.enq.of(scan.KindScan)) != scans {
		t.Error("who-added-it changed or a scan was queued")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'upload.remove_text'`); n != 1 {
		t.Errorf("audit entries = %d", n)
	}
	out, err := e.svc.RestoreRemoval(bg, me, res.RemovalID)
	if err != nil || len(out.Restored) != 1 {
		t.Fatalf("restore = %+v, %v", out, err)
	}
	if _, err := os.Stat(epub); err != nil {
		t.Errorf("the edition is not back: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM upload_paths WHERE path = ? AND state = 'present'`, epub); n != 1 {
		t.Errorf("the edition's ledger row is not present: %d", n)
	}
}

// After Put back and a rescan, does the text edition pair with its audiobook again? The test records the answer.
func TestUploadHeldPutBackTextRepairs(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, fileID, _, svc := combinedBook(t, e)
	res, err := e.svc.RemoveEbookFile(bg, me, bookID, fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RestoreRemoval(bg, me, res.RemovalID); err != nil {
		t.Fatal(err)
	}
	sc := eadmIngest(t, e)
	eadmScanLast(t, e, sc)
	var attached bool
	for i := 0; i < 200 && !attached; i++ {
		_ = svc.PairPending(bg)
		var n int
		_ = e.st.Reader().QueryRow(`SELECT count(*) FROM ebook_files WHERE book_id IN (SELECT id FROM books WHERE kind = 'audio')`).Scan(&n)
		attached = n == 1
		if !attached {
			time.Sleep(25 * time.Millisecond)
		}
	}
	t.Logf("re-paired with the audiobook after Put back and a rescan: %v", attached)
	if !attached {
		t.Log("LIMIT: a put-back text edition is not re-paired automatically")
	}
}

func TestUploadHeldAudioPartHeld(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, fileID, epub, svc := combinedBook(t, e)
	owners := e.count(`SELECT count(*) FROM upload_books`)
	res, err := e.svc.RemoveAudio(bg, me, bookID, "", func(ctx context.Context) error {
		_, err := svc.SplitText(ctx, bookID, me)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovalID == "" || len(res.Deleted) != 3 {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(epub); err != nil {
		t.Errorf("the text book moved: %v", err)
	}
	if e.count(`SELECT count(*) FROM upload_books`) != owners {
		t.Error("who-added-it changed: the text book that stays still needs it")
	}
	var on string
	if err := e.st.Reader().QueryRow(`SELECT COALESCE(book_id,'') FROM ebook_files WHERE id = ?`, fileID).Scan(&on); err != nil || on == "" {
		t.Errorf("the edition lost its book: %q %v", on, err)
	}
}

func TestUploadHeldWholeCombinedOneRemoval(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, _, epub, _ := combinedBook(t, e)
	res, err := e.svc.RemoveWhole(bg, me, bookID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM removals`); n != 1 || res.RemovalID == "" {
		t.Fatalf("removals = %d, result %+v", n, res)
	}
	if !slices.Contains(res.Deleted, epub) || len(res.Deleted) != 4 {
		t.Errorf("deleted = %v", res.Deleted)
	}
	if _, err := os.Stat(epub); !os.IsNotExist(err) {
		t.Error("the owned ebook is still in place")
	}
	if booksCount(t, e) != 0 || countRows(t, e, "ebook_files") != 0 {
		t.Error("rows left")
	}
	out, err := e.svc.RestoreRemoval(bg, me, res.RemovalID)
	if err != nil || len(out.Restored) != 4 || len(out.Kept) != 0 {
		t.Fatalf("restore = %+v, %v", out, err)
	}
	if _, err := os.Stat(epub); err != nil {
		t.Errorf("not back: %v", err)
	}
}

func TestUploadHeldWholeCombinedUnownedEbookStays(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, fileID, epub, _ := combinedBook(t, e)
	if _, err := e.st.Writer().Exec(`DELETE FROM upload_paths WHERE path = ?`, epub); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.RemoveWhole(bg, me, bookID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovalID == "" || slices.Contains(res.Deleted, epub) {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(epub); err != nil {
		t.Errorf("the library ebook moved: %v", err)
	}
	var on *string
	if err := e.st.Reader().QueryRow(`SELECT book_id FROM ebook_files WHERE id = ?`, fileID).Scan(&on); err != nil || on != nil {
		t.Errorf("the ebook is still paired: %v %v", on, err)
	}
}

func TestUploadHeldTextOnlyLedger(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	sc := eadmIngest(t, e)
	v := e.send("x", book{"a.epub": eadmEPUB(t, "Placed Text", "Ann Author")})
	e.mustPlace(v.ID)
	eadmScanLast(t, e, sc)
	eadmWaitBooks(t, e, 1)
	var id string
	if err := e.st.Reader().QueryRow(`SELECT id FROM books`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.RemoveWhole(bg, me, id, "", nil)
	if err != nil || res.RemovalID == "" || len(res.Deleted) != 1 {
		t.Fatalf("remove: %+v %v", res, err)
	}
	if got := heldTree(t, e.root); len(got) != 0 {
		t.Errorf("tree = %v", got)
	}
	out, err := e.svc.RestoreRemoval(bg, me, res.RemovalID)
	if err != nil || len(out.Restored) != 1 {
		t.Fatalf("restore = %+v, %v", out, err)
	}
}
