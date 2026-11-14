package upload

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// combinedBook uploads an audiobook and an ebook of the same title in one folder, scans them and waits until
// the ebook is attached to the audiobook. It returns the audiobook's id, the ebook file's id and its path.
func combinedBook(t *testing.T, e *env) (bookID, fileID, epub string, svc *ebook.Service) {
	t.Helper()
	if _, err := e.st.Writer().Exec(`INSERT INTO users(id, username, role, created_at) VALUES (?, 'owner', 'owner', 1) ON CONFLICT(id) DO NOTHING`, me); err != nil {
		t.Fatal(err)
	}
	sc, svc := eadmIngestSvc(t, e)
	b := greatBook(t)
	b["My Book/My Book.epub"] = eadmEPUB(t, "Great Title", "Ann Author")
	v := e.send("My Book", b)
	e.mustPlace(v.ID)
	eadmScanLast(t, e, sc)
	for i := 0; i < 200; i++ {
		_ = e.st.Reader().QueryRow(`SELECT book_id, id FROM ebook_files WHERE book_id IN (SELECT id FROM books WHERE kind = 'audio')`).Scan(&bookID, &fileID)
		if fileID != "" {
			break
		}
		_ = svc.PairPending(bg)
		time.Sleep(25 * time.Millisecond)
	}
	if fileID == "" {
		t.Fatal("the ebook never attached to the audiobook")
	}
	if err := e.st.Reader().QueryRow(`SELECT path FROM ebook_files WHERE id = ?`, fileID).Scan(&epub); err != nil {
		t.Fatal(err)
	}
	return bookID, fileID, epub, svc
}

func TestRemoveTextPartKeepsAudio(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, fileID, epub, _ := combinedBook(t, e)
	if _, err := e.st.Writer().Exec(`INSERT INTO bookmarks(id, user_id, book_id, position_ms, created_at, updated_at) VALUES ('bm1', ?, ?, 5000, 1, 1)`, me, bookID); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.PartsOf(bg, bookID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Combined || !p.Audio || len(p.Text) != 1 || !p.Text[0].Removable {
		t.Fatalf("parts = %+v", p)
	}
	res, err := e.svc.RemoveEbookFile(bg, me, bookID, fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0] != epub {
		t.Errorf("result = %+v", res)
	}
	if _, err := os.Stat(epub); !os.IsNotExist(err) {
		t.Error("the ebook file is still on disk")
	}
	if n := countRows(t, e, "ebook_files"); n != 0 {
		t.Errorf("ebook_files = %d", n)
	}
	if n := countRows(t, e, "audio_files"); n != 2 {
		t.Errorf("audio_files = %d, want 2", n)
	}
	if n := countRows(t, e, "books"); n != 1 {
		t.Errorf("books = %d, want 1", n)
	}
	if n := countRows(t, e, "bookmarks"); n != 1 {
		t.Errorf("bookmarks = %d, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(epub), "01.mp3")); err != nil {
		t.Errorf("audio file gone: %v", err)
	}
}

func TestRemoveTextPartRefusesChangedAndLibraryFiles(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, fileID, epub, _ := combinedBook(t, e)
	if err := os.WriteFile(epub, []byte("my own copy, different"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.RemoveEbookFile(bg, me, bookID, fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Kept) != 1 || len(res.Deleted) != 0 {
		t.Errorf("result = %+v", res)
	}
	if n := countRows(t, e, "ebook_files"); n != 1 {
		t.Errorf("the row of a kept file went: %d", n)
	}
	// A file the ledger does not know is not offered and is refused.
	if _, err := e.st.Writer().Exec(`DELETE FROM upload_paths WHERE path = ?`, epub); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.PartsOf(bg, bookID, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.AnyText() || !p.Audio {
		t.Errorf("parts = %+v", p)
	}
	if !p.Whole || p.Staying() != 1 {
		t.Errorf("whole removal: %v, staying %d", p.Whole, p.Staying())
	}
	if _, err := e.svc.RemoveEbookFile(bg, me, bookID, fileID, ""); err == nil {
		t.Error("a library ebook was removed")
	}
	if _, err := os.Stat(epub); err != nil {
		t.Errorf("library file touched: %v", err)
	}
}

func TestRemoveAudioPartLeavesTextBook(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, fileID, epub, svc := combinedBook(t, e)
	var made []string
	res, err := e.svc.RemoveAudio(bg, me, bookID, "", func(ctx context.Context) (err error) {
		made, err = svc.SplitText(ctx, bookID, me)
		return err
	})
	if err != nil || len(made) != 1 {
		t.Fatalf("split = %v, %v", made, err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 3 || len(res.Kept) != 0 {
		t.Errorf("result = %+v", res)
	}
	if _, err := os.Stat(epub); err != nil {
		t.Errorf("the text file went: %v", err)
	}
	var kind, on string
	if err := e.st.Reader().QueryRow(`SELECT b.kind, f.book_id FROM ebook_files f JOIN books b ON b.id = f.book_id WHERE f.id = ?`, fileID).Scan(&kind, &on); err != nil {
		t.Fatal(err)
	}
	if kind != "text" || on != made[0] {
		t.Errorf("kind = %q on %q, want text on %q", kind, on, made[0])
	}
	if n := countRows(t, e, "audio_files"); n != 0 {
		t.Errorf("audio_files = %d", n)
	}
	if n := countRows(t, e, "books"); n != 1 {
		t.Errorf("books = %d", n)
	}
}
