package upload

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// placedBook uploads, places and imports greatBook; it returns the book id.
func placedBook(t *testing.T, e *env) (uploadID, bookID string) {
	t.Helper()
	v := e.send("My Book", greatBook(t))
	e.mustPlace(v.ID)
	var p scan.ScanPayload
	q := e.enq.of(scan.KindScan)
	if err := json.Unmarshal(q[len(q)-1].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if _, err := e.scanner().ScanSubtree(bg, p.LibraryID, p.FolderID, p.Subdir, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Reader().QueryRow(`SELECT id FROM books ORDER BY added_at DESC LIMIT 1`).Scan(&bookID); err != nil {
		t.Fatal(err)
	}
	return v.ID, bookID
}

func booksCount(t *testing.T, e *env) int { return countRows(t, e, "books") }

func TestRemoveDeletesOnlyLedgerPaths(t *testing.T) {
	e := newEnv(t, nil)
	// A book and a folder the user made, both beside the upload and never touched.
	mk(t, e.root, "Mine/keep.txt", "Ann Author/Other Book/their.mp3")
	_, bookID := placedBook(t, e)
	if !e.svc.Removable(bg, bookID) {
		t.Fatal("an uploaded, untouched book is not removable")
	}
	before := snapshot(t, filepath.Join(e.root, "Mine"))
	res, err := e.svc.Remove(bg, me, bookID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 3 || len(res.Kept) != 0 {
		t.Errorf("result = %+v", res)
	}
	// The author folder was made by the upload but now holds someone else's book: it stays.
	want := []string{"Ann Author/", "Ann Author/Other Book/", "Ann Author/Other Book/their.mp3", "Mine/", "Mine/keep.txt"}
	if got := tree(t, e.root); !reflect.DeepEqual(got, want) {
		t.Errorf("tree = %v", got)
	}
	if after := snapshot(t, filepath.Join(e.root, "Mine")); !reflect.DeepEqual(before, after) {
		t.Error("their folder changed")
	}
	if n := booksCount(t, e); n != 0 {
		t.Errorf("books = %d", n)
	}
	if n := countRows(t, e, "audio_files"); n != 0 {
		t.Errorf("audio_files = %d", n)
	}
	var removed int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM upload_paths WHERE state = 'removed' AND kind = 'file'`).Scan(&removed); err != nil || removed != 3 {
		t.Errorf("removed file rows = %d, %v", removed, err)
	}
	var audits int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'upload.remove'`).Scan(&audits); err != nil || audits != 1 {
		t.Errorf("remove audits = %d, %v", audits, err)
	}
}

func TestRemoveEmptiesWhatItMade(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	if _, err := e.svc.Remove(bg, me, bookID, ""); err != nil {
		t.Fatal(err)
	}
	if got := tree(t, e.root); len(got) != 0 {
		t.Errorf("folders it made are still there: %v", got)
	}
}

func TestRemoveKeepsChangedAndAddedFiles(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	book := filepath.Join(e.root, "Ann Author", "Great Title")
	// A picture the user replaced, and a note they added.
	cover := filepath.Join(book, "cover.jpg")
	if err := os.WriteFile(cover, []byte("my own cover, different size"), 0o644); err != nil {
		t.Fatal(err)
	}
	mk(t, book, "notes.txt")
	res, err := e.svc.Remove(bg, me, bookID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 2 || !reflect.DeepEqual(res.Kept, []string{cover}) {
		t.Errorf("result = %+v", res)
	}
	want := []string{"Ann Author/", "Ann Author/Great Title/", "Ann Author/Great Title/cover.jpg", "Ann Author/Great Title/notes.txt"}
	if got := tree(t, e.root); !reflect.DeepEqual(got, want) {
		t.Errorf("tree = %v", got)
	}
	if b, _ := os.ReadFile(cover); string(b) != "my own cover, different size" {
		t.Error("their cover changed")
	}
	// No audio is left, so the book is gone.
	if n := booksCount(t, e); n != 0 {
		t.Errorf("books = %d", n)
	}
}

func TestRemoveChangedAudioKeepsTheBook(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	track := filepath.Join(e.root, "Ann Author", "Great Title", "01.mp3")
	// Same size, new mtime and content: "write details into the file" would do this.
	data, _ := os.ReadFile(track)
	data[len(data)-1] ^= 0xff
	data[0] = 'T'
	future := time.Now().Add(time.Hour)
	if err := os.WriteFile(track, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(track, future, future); err != nil {
		t.Fatal(err)
	}
	before := len(e.enq.of(scan.KindScan))
	res, err := e.svc.Remove(bg, me, bookID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Kept, []string{track}) {
		t.Errorf("kept = %v", res.Kept)
	}
	if n := booksCount(t, e); n != 1 {
		t.Errorf("books = %d, want the book to stay", n)
	}
	if len(e.enq.of(scan.KindScan)) != before+1 {
		t.Error("no scoped scan was queued")
	}
	if _, err := os.Stat(track); err != nil {
		t.Errorf("changed track gone: %v", err)
	}
}

func TestRemovableOnlyWhenEveryAudioFileIsOurs(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	// Not an uploaded book at all.
	mediatest.WriteFake(t, filepath.Join(mustMkdir(t, e.root, "Plain", "Book"), "a.m4b"), mediatest.FakeFile{})
	if _, err := e.scanner().ScanLibrary(bg, e.lib.ID, nil); err != nil {
		t.Fatal(err)
	}
	var plain string
	if err := e.st.Reader().QueryRow(`SELECT id FROM books WHERE title = 'Book'`).Scan(&plain); err != nil {
		t.Fatal(err)
	}
	if e.svc.Removable(bg, plain) {
		t.Error("a scanned book is removable")
	}
	if _, err := e.svc.Remove(bg, me, plain, ""); err == nil {
		t.Error("Remove deleted a book Tendshelf did not add")
	}
	if _, err := os.Stat(filepath.Join(e.root, "Plain", "Book", "a.m4b")); err != nil {
		t.Errorf("their file is gone: %v", err)
	}
	// Now the user adds an audio file to the uploaded book's folder.
	mediatest.WriteFake(t, filepath.Join(e.root, "Ann Author", "Great Title", "03.mp3"), mediatest.FakeFile{Container: "mp3", Codec: "mp3"})
	if _, err := e.scanner().ScanSubtree(bg, e.lib.ID, e.folder.ID, "Ann Author/Great Title", nil); err != nil {
		t.Fatal(err)
	}
	if e.svc.Removable(bg, bookID) {
		t.Error("a book with a file the user added is removable")
	}
	if e.svc.Removable(bg, "bk_nope") {
		t.Error("unknown book is removable")
	}
	if _, err := e.svc.Remove(bg, me, "bk_nope", ""); err == nil {
		t.Error("unknown book removed")
	}
}

// Rows that point outside the folder are never followed.
func TestRemoveIgnoresLedgerRowsOutsideTheFolder(t *testing.T) {
	e := newEnv(t, nil)
	uploadID, bookID := placedBook(t, e)
	outside := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Writer().Exec(`INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, size, mtime_ns, created_at, updated_at)
  VALUES ('upp_evil', ?, ?, ?, 'file', 'present', 1, 1, 1, 1)`, uploadID, e.folder.ID, outside); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Remove(bg, me, bookID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("a path outside the library folder was deleted: %v", err)
	}
}
