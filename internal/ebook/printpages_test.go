package ebook

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// ppEPUB is a two-chapter book with ids on its blocks, one id inside a sentence, and a page list that
// points at them. Version 3 uses the nav page-list, version 2 the NCX pageList.
func ppEPUB(t *testing.T, version int, labels, hrefs []string) []byte {
	t.Helper()
	return ebooktest.EPUB(t, ebooktest.EPUBOptions{
		Version: version, Title: "Paged", PageLabels: labels, PageHrefs: hrefs,
		Chapters: []ebooktest.EPUBChapter{
			{Title: "One", Raw: `<h1 id="top">One</h1><p id="a">First paragraph.</p><p>Second <span id="mid"></span>paragraph, cut by a page.</p><p id="c">Third.</p>`},
			{Title: "Two", Raw: `<h1>Two</h1><p id="d">Fourth.</p><p id="e">Fifth.</p>`},
		},
	})
}

func ppMarks(d *Doc) string {
	var parts []string
	for _, m := range d.PageList {
		parts = append(parts, m.Label+"@"+m.Block)
	}
	return strings.Join(parts, " ")
}

func TestPrintPagesEPUB3(t *testing.T) {
	d := mustRead(t, "a.epub", ppEPUB(t, 3,
		[]string{"i", "ii", "1", "2", "3"},
		[]string{"ch1.xhtml#top", "ch1.xhtml#a", "ch1.xhtml#mid", "ch2.xhtml#d", "ch2.xhtml#e"}))
	// "mid" sits inside the third block: the page starts at that block's start.
	want := "i@c1.p1 ii@c1.p2 1@c1.p3 2@c2.p2 3@c2.p3"
	if got := ppMarks(d); got != want {
		t.Errorf("marks = %q, want %q", got, want)
	}
	if d.PageCount != 3 {
		t.Errorf("pageCount = %d, want 3 (the largest number)", d.PageCount)
	}
	if d.Recipe != "tendshelf-ebook-v3" {
		t.Errorf("recipe %q", d.Recipe)
	}
}

func TestPrintPagesNCX(t *testing.T) {
	d := mustRead(t, "a.epub", ppEPUB(t, 2,
		[]string{"xii", "gone", "", "this label is far too long to be a page", "7", "8"},
		[]string{"ch1.xhtml#a", "ch1.xhtml#nowhere", "ch1.xhtml#c", "ch1.xhtml#c", "ch2.xhtml#d", "ch2.xhtml#d"}))
	// Unresolved, empty and over-long labels are dropped; of two on one block the first wins.
	if got, want := ppMarks(d), "xii@c1.p2 7@c2.p2"; got != want {
		t.Errorf("marks = %q, want %q", got, want)
	}
	if d.PageCount != 7 {
		t.Errorf("pageCount = %d, want 7", d.PageCount)
	}
	// Roman only: the count is how many marks there are.
	d = mustRead(t, "a.epub", ppEPUB(t, 3, []string{"i", "ii", "iii"}, []string{"ch1.xhtml#a", "ch1.xhtml#c", "ch2.xhtml#d"}))
	if d.PageCount != 3 || len(d.PageList) != 3 {
		t.Errorf("roman: pageCount %d, marks %d", d.PageCount, len(d.PageList))
	}
	// A target with no fragment is the part's first block.
	d = mustRead(t, "a.epub", ppEPUB(t, 3, []string{"9"}, []string{"ch2.xhtml"}))
	if got := ppMarks(d); got != "9@c2.p1" {
		t.Errorf("no fragment: %q", got)
	}
}

func TestPrintPagesFallback(t *testing.T) {
	d := mustRead(t, "a.epub", ppEPUB(t, 3, nil, nil))
	if len(d.PageList) != 0 {
		t.Errorf("marks %q on a book with no page list", ppMarks(d))
	}
	if d.PageCount != 1 {
		t.Errorf("pageCount = %d, want ceil(chars/1800) = 1", d.PageCount)
	}
	js, _ := Encode(d)
	if bytes.Contains(js, []byte("pageList")) {
		t.Error("an empty list must not reach the JSON")
	}
	// All targets unresolved: exactly the same.
	d = mustRead(t, "a.epub", ppEPUB(t, 3, []string{"1", "2"}, []string{"ch1.xhtml#x", "nope.xhtml#y"}))
	if len(d.PageList) != 0 || d.PageCount != 1 {
		t.Errorf("unresolved list: marks %d, pageCount %d", len(d.PageList), d.PageCount)
	}
}

func TestPrintPagesPDF(t *testing.T) {
	o := ebooktest.PDFOptions{Title: "Spike", Pages: [][]ebooktest.Line{
		proseLines(1, 5, 700), proseLines(6, 5, 700), proseLines(11, 5, 700),
	}}
	d := mustRead(t, "p.pdf", ebooktest.PDF(t, o))
	if len(d.PageList) == 0 {
		t.Fatal("no marks")
	}
	if d.PageList[0].Label != "1" {
		t.Errorf("first label %q", d.PageList[0].Label)
	}
	seen := map[string]bool{}
	for _, m := range d.PageList {
		if seen[m.Label] {
			t.Errorf("label %s twice", m.Label)
		}
		seen[m.Label] = true
	}
	if d.PageCount != 3 {
		t.Errorf("pageCount = %d, want 3 (the number of pages, as before)", d.PageCount)
	}
}

func TestPrintPagesAt(t *testing.T) {
	d := &Doc{PageList: []PageMark{{"ii", "c1.p2"}, {"1", "c1.p5"}, {"2", "c3.p1"}}}
	for _, c := range []struct {
		name         string
		chapter, blk int
		want         string
	}{
		{"before the first", 1, 0, ""},
		{"on the first", 1, 1, "ii"},
		{"after the first", 1, 3, "ii"},
		{"on the second", 1, 4, "1"},
		{"later in the chapter", 1, 90, "1"},
		{"a chapter with no mark", 2, 0, "1"},
		{"the third", 3, 0, "2"},
		{"past the end", 9, 4, "2"},
	} {
		if got := PageAt(d, c.chapter, c.blk); got != c.want {
			t.Errorf("%s: PageAt(%d,%d) = %q, want %q", c.name, c.chapter, c.blk, got, c.want)
		}
	}
	if got := PageAt(&Doc{}, 1, 1); got != "" {
		t.Errorf("no list: %q", got)
	}
}

func TestPrintPagesDeterminism(t *testing.T) {
	data := ppEPUB(t, 3, []string{"i", "1"}, []string{"ch1.xhtml#a", "ch2.xhtml#d"})
	ja, _ := Encode(mustRead(t, "a.epub", data))
	jb, _ := Encode(mustRead(t, "a.epub", data))
	if !bytes.Equal(ja, jb) || DocSHA(ja) != DocSHA(jb) {
		t.Error("the same file gave different documents")
	}
	if !bytes.Contains(ja, []byte(`"pageList":[{"label":"i","block":"c1.p2"},{"label":"1","block":"c2.p2"}]`)) {
		t.Errorf("pageList not in the document")
	}
}

// TestPrintPagesReread: a book read by recipe v2 keeps its marks, and the highlight, bookmark and place
// move to the v3 edition on the same sentence ids. Ids do not change, so the re-anchor is exact.
func TestPrintPagesReread(t *testing.T) {
	e := newIEnv(t, true)
	chapters := []ebooktest.EPUBChapter{
		{Title: "One", Raw: `<h1>One</h1><p id="a">It was a quiet morning in the harbour town and nobody expected anything at all.</p><p id="b">Second paragraph.</p>`},
	}
	opts := ebooktest.EPUBOptions{Version: 3, Title: "Harbour", Authors: []string{"Ann Author"}, Chapters: chapters}
	older := ebooktest.EPUB(t, opts)
	opts.PageLabels, opts.PageHrefs = []string{"iv", "5"}, []string{"ch1.xhtml#a", "ch1.xhtml#b"}
	path := e.write("Book/book.epub", ebooktest.EPUB(t, opts))
	e.scan()
	f := e.waitState(e.fileAt(path).ID)
	if f.State != "ready" {
		t.Fatalf("state %s", f.State)
	}
	pairTExec(e, `INSERT INTO users(id, username, role, created_at) VALUES ('u1', 'Ana', 'owner', 1)`)
	pairTBook(e, "b1", "Harbour", "Ann Author")
	pairTExec(e, `UPDATE ebook_files SET book_id = 'b1', book_default = 1 WHERE id = ?`, f.ID)

	// The stored edition becomes what recipe v2 made: the same text, no page list.
	od := mustRead(t, "older.epub", older)
	od.Recipe = "tendshelf-ebook-v2"
	od.SourceSHA256 = f.SHA256
	js, _ := Encode(od)
	if bytes.Contains(js, []byte("pageList")) {
		t.Fatal("the older document has a page list")
	}
	oldSHA, err := WriteDoc(e.svc.textDir(), js)
	if err != nil {
		t.Fatal(err)
	}
	var oldID, curSHA string
	if err := e.st.Reader().QueryRow(`SELECT id, doc_sha256 FROM editions WHERE ebook_file_id = ?`, f.ID).Scan(&oldID, &curSHA); err != nil {
		t.Fatal(err)
	}
	pairTExec(e, `UPDATE editions SET recipe = 'tendshelf-ebook-v2', doc_sha256 = ?, doc_bytes = ? WHERE id = ?`, oldSHA, len(js), oldID)
	pairTExec(e, `INSERT INTO highlights(id, user_id, book_id, edition_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote, quote_prefix, quote_suffix, created_at, updated_at)
VALUES ('h1','u1','b1',?,1,'c1.p2.s0',9,'c1.p2.s0',22,'quiet morning','a ',' in',1,1)`, oldID)
	pairTExec(e, `INSERT INTO bookmarks(id, user_id, book_id, position_ms, created_at, updated_at, edition_id, sid) VALUES ('m1','u1','b1',0,1,1,?,'c1.p3.s0')`, oldID)
	pairTExec(e, `INSERT INTO reading_positions(user_id, book_id, edition_id, chapter_idx, sid, off, quote, char_pos, percent_bp, client_updated_at, server_updated_at, device_id)
VALUES ('u1','b1',?,1,'c1.p2.s0',9,'quiet morning in the harbour',30,500,5,5,'dev')`, oldID)

	e.waitIdle(t)
	if err := e.svc.EnqueueStale(bg); err != nil {
		t.Fatal(err)
	}
	if _, ed, ok, err := e.svc.Store().EditionFor(bg, "u1", "b1"); err != nil || !ok || ed.ID != oldID {
		t.Fatalf("while pending, the old edition must answer: %q ok=%v err=%v", ed.ID, ok, err)
	}
	if err := e.svc.EnqueuePending(bg); err != nil {
		t.Fatal(err)
	}
	if got := e.waitState(f.ID); got.State != "ready" {
		t.Fatalf("after the re-read state %q", got.State)
	}
	var newID, recipe, sha string
	if err := e.st.Reader().QueryRow(`SELECT id, recipe, doc_sha256 FROM editions WHERE ebook_file_id = ?`, f.ID).Scan(&newID, &recipe, &sha); err != nil || recipe != Recipe {
		t.Fatalf("edition %q recipe %q err %v", newID, recipe, err)
	}
	nd, err := ReadDoc(e.svc.textDir(), sha)
	if err != nil {
		t.Fatal(err)
	}
	if got := ppMarks(nd); got != "iv@c1.p2 5@c1.p3" {
		t.Errorf("marks = %q", got)
	}
	if _, err := os.Stat(DocPath(e.svc.textDir(), oldSHA)); !os.IsNotExist(err) {
		t.Errorf("the v2 document is still on disk: %v", err)
	}
	var hed, bed, ped *string
	var hsid, bsid, psid string
	var hoff, hend, poff int
	if err := e.st.Reader().QueryRow(`SELECT edition_id, start_sid, start_off, end_off FROM highlights WHERE id='h1'`).Scan(&hed, &hsid, &hoff, &hend); err != nil {
		t.Fatal(err)
	}
	if hed == nil || *hed != newID || hsid != "c1.p2.s0" || hoff != 9 || hend != 22 {
		t.Errorf("highlight = %v %q %d-%d", hed, hsid, hoff, hend)
	}
	if err := e.st.Reader().QueryRow(`SELECT edition_id, sid FROM bookmarks WHERE id='m1'`).Scan(&bed, &bsid); err != nil || bed == nil || *bed != newID || bsid != "c1.p3.s0" {
		t.Errorf("bookmark = %v %q %v", bed, bsid, err)
	}
	if err := e.st.Reader().QueryRow(`SELECT edition_id, sid, off FROM reading_positions WHERE user_id='u1'`).Scan(&ped, &psid, &poff); err != nil || ped == nil || *ped != newID || psid != "c1.p2.s0" || poff != 9 {
		t.Errorf("position = %v %q %d %v", ped, psid, poff, err)
	}
}
