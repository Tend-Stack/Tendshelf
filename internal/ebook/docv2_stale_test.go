package ebook

import (
	"os"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// TestDocV2StaleReread: a file read by an older recipe keeps serving its old edition while it is read
// again, then the marks move to the new document and the old edition and its document are gone.
func TestDocV2StaleReread(t *testing.T) {
	e := newIEnv(t, true)
	path := e.write("Book/book.epub", sampleEPUB(t, "Harbour"))
	e.scan()
	f := e.waitState(e.fileAt(path).ID)
	if f.State != "ready" {
		t.Fatalf("state %s", f.State)
	}
	pairTExec(e, `INSERT INTO users(id, username, role, created_at) VALUES ('u1', 'Ana', 'owner', 1)`)
	pairTBook(e, "b1", "Harbour", "Ann Author")
	pairTExec(e, `UPDATE ebook_files SET book_id = 'b1', book_default = 1 WHERE id = ?`, f.ID)

	// Make the stored edition look like a v1 reading whose document has one more paragraph in front.
	older := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Harbour", Authors: []string{"Ann Author"},
		Chapters: []ebooktest.EPUBChapter{
			{Title: "One", Paras: []string{"Preface line.", "It was a quiet morning in the harbour town and nobody expected anything at all."}},
			{Title: "Two", Paras: []string{"The second chapter begins with a short line.", "Another paragraph follows it."}},
		}})
	od := mustRead(t, "older.epub", older)
	od.Recipe = "tendshelf-ebook-v1"
	od.SourceSHA256 = f.SHA256
	js, _ := Encode(od)
	oldSHA, err := WriteDoc(e.svc.textDir(), js)
	if err != nil {
		t.Fatal(err)
	}
	var newDocSHA, oldID string
	if err := e.st.Reader().QueryRow(`SELECT id, doc_sha256 FROM editions WHERE ebook_file_id = ?`, f.ID).Scan(&oldID, &newDocSHA); err != nil {
		t.Fatal(err)
	}
	pairTExec(e, `UPDATE editions SET recipe = 'tendshelf-ebook-v1', doc_sha256 = ?, doc_bytes = ? WHERE id = ?`, oldSHA, len(js), oldID)

	pairTExec(e, `INSERT INTO highlights(id, user_id, book_id, edition_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote, quote_prefix, quote_suffix, created_at, updated_at)
VALUES ('h1','u1','b1',?,1,'c1.p3.s0',9,'c1.p3.s0',22,'quiet morning','a ',' in',1,1),
       ('h2','u1','b1',?,1,'c1.p2.s0',0,'c1.p2.s0',13,'Preface line.','','',1,1)`, oldID, oldID)
	pairTExec(e, `INSERT INTO bookmarks(id, user_id, book_id, position_ms, created_at, updated_at, edition_id, sid) VALUES ('m1','u1','b1',0,1,1,?,'c1.p3.s0')`, oldID)
	pairTExec(e, `INSERT INTO reading_positions(user_id, book_id, edition_id, chapter_idx, sid, off, quote, char_pos, percent_bp, client_updated_at, server_updated_at, device_id)
VALUES ('u1','b1',?,1,'c1.p3.s0',9,'quiet morning in the harbour',30,500,5,5,'dev')`, oldID)

	// Stale: pending, but the old edition still answers.
	if err := e.svc.EnqueueStale(bg); err != nil {
		t.Fatal(err)
	}
	if got := e.file(f.ID); got.State != "pending" {
		t.Fatalf("file state %q, want pending", got.State)
	}
	_, ed, ok, err := e.svc.Store().EditionFor(bg, "u1", "b1")
	if err != nil || !ok || ed.ID != oldID {
		t.Fatalf("while pending EditionFor = %q ok=%v err=%v, want the old edition %q", ed.ID, ok, err, oldID)
	}

	// Read again.
	if err := e.svc.EnqueuePending(bg); err != nil {
		t.Fatal(err)
	}
	if got := e.waitState(f.ID); got.State != "ready" {
		t.Fatalf("after the re-read state %q", got.State)
	}
	if n := e.count(`SELECT count(*) FROM editions WHERE ebook_file_id = ?`, f.ID); n != 1 {
		t.Fatalf("%d editions, want 1", n)
	}
	var newID, recipe string
	if err := e.st.Reader().QueryRow(`SELECT id, recipe FROM editions WHERE ebook_file_id = ?`, f.ID).Scan(&newID, &recipe); err != nil || recipe != Recipe {
		t.Fatalf("edition %q recipe %q err %v", newID, recipe, err)
	}
	if _, err := os.Stat(DocPath(e.svc.textDir(), oldSHA)); !os.IsNotExist(err) {
		t.Errorf("the older document is still on disk: %v", err)
	}
	type hl struct {
		ed       *string
		sid      string
		off, end int
	}
	read := func(id string) hl {
		var h hl
		if err := e.st.Reader().QueryRow(`SELECT edition_id, start_sid, start_off, end_off FROM highlights WHERE id = ?`, id).Scan(&h.ed, &h.sid, &h.off, &h.end); err != nil {
			t.Fatal(err)
		}
		return h
	}
	if h := read("h1"); h.ed == nil || *h.ed != newID || h.sid != "c1.p2.s0" || h.off != 9 || h.end != 22 {
		t.Errorf("moved highlight = %+v", h)
	}
	if h := read("h2"); h.ed != nil || h.sid != "c1.p2.s0" {
		t.Errorf("lost highlight must keep its row with no edition: %+v", h)
	}
	var bed *string
	var bsid string
	if err := e.st.Reader().QueryRow(`SELECT edition_id, sid FROM bookmarks WHERE id = 'm1'`).Scan(&bed, &bsid); err != nil || bed == nil || *bed != newID || bsid != "c1.p2.s0" {
		t.Errorf("bookmark = %v %q %v", bed, bsid, err)
	}
	var ped *string
	var psid string
	var poff, pchar, pbp int
	if err := e.st.Reader().QueryRow(`SELECT edition_id, sid, off, char_pos, percent_bp FROM reading_positions WHERE user_id='u1'`).Scan(&ped, &psid, &poff, &pchar, &pbp); err != nil {
		t.Fatal(err)
	}
	if ped == nil || *ped != newID || psid != "c1.p2.s0" || poff != 9 || pchar != 3+9 || pbp <= 0 {
		t.Errorf("position = %v %q %d char=%d bp=%d", ped, psid, poff, pchar, pbp)
	}
	_, ed, ok, err = e.svc.Store().EditionFor(bg, "u1", "b1")
	if err != nil || !ok || ed.ID != newID {
		t.Errorf("after the re-read EditionFor = %q", ed.ID)
	}
}
