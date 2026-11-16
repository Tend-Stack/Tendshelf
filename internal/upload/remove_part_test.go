package upload

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	if got := holdingFiles(t, e.root, res.RemovalID); len(got) != 1 {
		t.Errorf("the ebook file is not in the holding folder: %v", got)
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
	if got := holdingFiles(t, e.root, res.RemovalID); len(got) != 3 {
		t.Errorf("the audio is not in the holding folder: %v", got)
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

// storedUpload adds a copy "uploaded on the Text page" to a book, put there by userID.
func storedUpload(t *testing.T, e *env, bookID, userID string) string {
	t.Helper()
	if _, err := e.st.Writer().Exec(`INSERT INTO users(id, username, role, created_at) VALUES (?, ?, 'member', 1) ON CONFLICT(id) DO NOTHING`, userID, userID); err != nil {
		t.Fatal(err)
	}
	id := "ef_" + userID
	sha := strings.Repeat("a", 63) + string(rune('a'+len(userID)%6))
	if _, err := e.st.Writer().Exec(`INSERT INTO ebook_files(id, origin, library_id, book_id, paired_by, ext, size, quick_hash, sha256, format, state, original_name, uploaded_by, created_at, updated_at)
SELECT ?, 'upload', library_id, id, 'upload', 'epub', 10, ?, ?, 'epub', 'ready', 'x.epub', ?, 5, 5 FROM books WHERE id = ?`, id, sha, sha, userID, bookID); err != nil {
		t.Fatal(err)
	}
	return id
}

// dropper records which stored uploads were dropped and deletes the row like ebook.Service.RemoveUpload.
func dropper(e *env, dropped *[]string) func(context.Context, string) error {
	return func(_ context.Context, id string) error {
		*dropped = append(*dropped, id)
		_, err := e.st.Writer().Exec(`DELETE FROM ebook_files WHERE id = ?`, id)
		return err
	}
}

// textBookWithOthersUpload is a text-only book: one EPUB the contributor (me) placed and a stored copy another
// person uploaded on its Text page.
func textBookWithOthersUpload(t *testing.T, e *env) (bookID, theirs, epub string) {
	t.Helper()
	sc := eadmIngest(t, e)
	v := e.send("x", book{"a.epub": eadmEPUB(t, "Placed Text", "Ann Author")})
	e.mustPlace(v.ID)
	eadmScanLast(t, e, sc)
	eadmWaitBooks(t, e, 1)
	if err := e.st.Reader().QueryRow(`SELECT b.id, f.path FROM books b JOIN ebook_files f ON f.book_id = b.id`).Scan(&bookID, &epub); err != nil {
		t.Fatal(err)
	}
	return bookID, storedUpload(t, e, bookID, "us_other"), epub
}

func TestContributorCannotRemoveOthersStoredUploadWithTheWholeBook(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, other, epub := textBookWithOthersUpload(t, e)
	p, err := e.svc.PartsOf(bg, bookID, me)
	if err != nil || p.Whole {
		t.Fatalf("a contributor is offered the whole book: %+v, %v", p, err)
	}
	var dropped []string
	_, err = e.svc.RemoveWhole(bg, me, bookID, me, dropper(e, &dropped))
	var fe *Error
	if !errors.As(err, &fe) || fe.Status != 409 || fe.Code != "not_theirs" {
		t.Fatalf("err = %v", err)
	}
	if len(dropped) != 0 || countRows(t, e, "ebook_files") != 2 || booksCount(t, e) != 1 {
		t.Errorf("something went: dropped %v, files %d, books %d", dropped, countRows(t, e, "ebook_files"), booksCount(t, e))
	}
	if _, err := os.Stat(epub); err != nil {
		t.Errorf("the placed file went: %v", err)
	}
	// The owner may remove all of it.
	if _, err := e.svc.RemoveWhole(bg, me, bookID, "", dropper(e, &dropped)); err != nil || len(dropped) != 1 || dropped[0] != other {
		t.Fatalf("owner: dropped %v, %v", dropped, err)
	}
}

func TestContributorWholeBookOfCombinedKeepsOthersStoredUpload(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, _, epub, _ := combinedBook(t, e)
	other := storedUpload(t, e, bookID, "us_other")
	p, err := e.svc.PartsOf(bg, bookID, me)
	if err != nil || !p.Whole || !p.Combined {
		t.Fatalf("parts = %+v, %v", p, err)
	}
	var dropped []string
	res, err := e.svc.RemoveWhole(bg, me, bookID, me, dropper(e, &dropped))
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 0 {
		t.Errorf("another person's stored upload was dropped: %v", dropped)
	}
	if _, err := os.Stat(epub); !os.IsNotExist(err) {
		t.Error("the contributor's own ebook is still on disk")
	}
	var on sql.NullString
	if err := e.st.Reader().QueryRow(`SELECT book_id FROM ebook_files WHERE id = ?`, other).Scan(&on); err != nil || on.Valid {
		t.Errorf("stored upload: %v, still on %v", err, on)
	}
	if booksCount(t, e) != 0 || len(res.Deleted) == 0 {
		t.Errorf("books %d, result %+v", booksCount(t, e), res)
	}
}

func TestContributorRemovesAudioOfOwnCombinedBookAndKeepsTheTextBook(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, _, _, svc := combinedBook(t, e)
	var made []string
	if _, err := e.svc.RemoveAudio(bg, me, bookID, me, func(ctx context.Context) (err error) {
		made, err = svc.SplitText(ctx, bookID, me)
		return err
	}); err != nil || len(made) != 1 {
		t.Fatalf("remove audio: %v %v", made, err)
	}
	// The text book that stayed is still theirs to remove.
	p, err := e.svc.PartsOf(bg, made[0], me)
	if err != nil || !p.Whole || len(p.Text) != 1 || !p.Text[0].Removable {
		t.Fatalf("the contributor lost the text book: %+v, %v", p, err)
	}
	if _, err := e.svc.RemoveWhole(bg, me, made[0], me, nil); err != nil {
		t.Fatalf("remove the text book: %v", err)
	}
	if booksCount(t, e) != 0 {
		t.Errorf("books = %d", booksCount(t, e))
	}
}

func TestRemoveEbookFileKeepsRowWhenLstatFailsOtherwise(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	bookID, fileID, epub, _ := combinedBook(t, e)
	outside := filepath.Join(t.TempDir(), "elsewhere.epub")
	if _, err := e.st.Writer().Exec(`UPDATE upload_paths SET path = ? WHERE path = ?`, outside, epub); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET path = ? WHERE id = ?`, outside, fileID); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.RemoveEbookFile(bg, me, bookID, fileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Kept) != 1 || res.Kept[0] != outside || len(res.Deleted) != 0 {
		t.Errorf("result = %+v", res)
	}
	if n := countRows(t, e, "ebook_files"); n != 1 {
		t.Errorf("the row went: %d", n)
	}
}

// folderGone makes the book's folder unknown to the library without touching the book.
func folderGone(t *testing.T, e *env, bookID string) {
	t.Helper()
	ctx := context.Background()
	c, err := e.st.Writer().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{`PRAGMA foreign_keys = OFF`, `UPDATE books SET folder_id = 'fo_gone' WHERE id = '` + bookID + `'`, `PRAGMA foreign_keys = ON`} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemovalRefusedBeforeAnythingIsDropped(t *testing.T) {
	t.Run("whole combined book", func(t *testing.T) {
		e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
		bookID, _, epub, _ := combinedBook(t, e)
		mine := storedUpload(t, e, bookID, me)
		folderGone(t, e, bookID)
		var dropped []string
		_, err := e.svc.RemoveWhole(bg, me, bookID, "", dropper(e, &dropped))
		var fe *Error
		if !errors.As(err, &fe) || fe.Status != 409 {
			t.Fatalf("err = %v", err)
		}
		var on sql.NullString
		if err := e.st.Reader().QueryRow(`SELECT book_id FROM ebook_files WHERE id = ?`, mine).Scan(&on); err != nil || !on.Valid {
			t.Errorf("stored upload: %v, on %v", err, on)
		}
		if len(dropped) != 0 || countRows(t, e, "ebook_files") != 2 || booksCount(t, e) != 1 {
			t.Errorf("dropped %v, files %d, books %d", dropped, countRows(t, e, "ebook_files"), booksCount(t, e))
		}
		if _, err := os.Stat(epub); err != nil {
			t.Errorf("file touched: %v", err)
		}
	})
	t.Run("text-only book with a placed file", func(t *testing.T) {
		e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
		sc := eadmIngest(t, e)
		e.mustPlace(e.send("x", book{"a.epub": eadmEPUB(t, "Placed Text", "Ann Author")}).ID)
		eadmScanLast(t, e, sc)
		eadmWaitBooks(t, e, 1)
		var bookID string
		if err := e.st.Reader().QueryRow(`SELECT id FROM books`).Scan(&bookID); err != nil {
			t.Fatal(err)
		}
		storedUpload(t, e, bookID, me)
		folderGone(t, e, bookID)
		var dropped []string
		if _, err := e.svc.RemoveWhole(bg, me, bookID, "", dropper(e, &dropped)); err == nil {
			t.Fatal("removed")
		}
		if len(dropped) != 0 || countRows(t, e, "ebook_files") != 2 {
			t.Errorf("dropped %v, files %d", dropped, countRows(t, e, "ebook_files"))
		}
	})
	t.Run("audio only", func(t *testing.T) {
		e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
		bookID, fileID, _, _ := combinedBook(t, e)
		folderGone(t, e, bookID)
		split := false
		_, err := e.svc.RemoveAudio(bg, me, bookID, "", func(context.Context) error { split = true; return nil })
		var fe *Error
		if !errors.As(err, &fe) || fe.Status != 409 || split {
			t.Fatalf("err = %v, split %v", err, split)
		}
		var on string
		if err := e.st.Reader().QueryRow(`SELECT book_id FROM ebook_files WHERE id = ?`, fileID).Scan(&on); err != nil || on != bookID {
			t.Errorf("text moved: %v %q", err, on)
		}
	})
}
