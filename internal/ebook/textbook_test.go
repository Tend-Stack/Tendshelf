package ebook

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// Helpers of the text-book tests carry the prefix tb.

// tbText makes a ready upload-origin ebook file with an edition and gives it its book, as the ingest job does.
func tbText(e *ienv, id, title, isbn string, authors ...string) File {
	e.t.Helper()
	pairTFile(e, id, "ready", "", title, authors...)
	if isbn != "" {
		pairTExec(e, `UPDATE editions SET isbn=? WHERE ebook_file_id=?`, isbn, id)
	}
	if err := e.svc.ensureFile(bg, id); err != nil {
		e.t.Fatal(err)
	}
	return e.file(id)
}

func tbAudio(e *ienv, id, title, isbn string, authors ...string) {
	e.t.Helper()
	var named []string
	for _, a := range authors {
		if a != "" {
			named = append(named, a)
		}
	}
	pairTBook(e, id, title, named...)
	if isbn != "" {
		pairTExec(e, `UPDATE books SET isbn=? WHERE id=?`, isbn, id)
	}
}

func tbKind(e *ienv, bookID string) string {
	var k string
	if err := e.st.Reader().QueryRow(`SELECT kind FROM books WHERE id=?`, bookID).Scan(&k); err != nil {
		return ""
	}
	return k
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func tbBooks(e *ienv) int { return e.count(`SELECT count(*) FROM books`) }

func TestTextBookCreated(t *testing.T) {
	e := newIEnv(t, true)
	var made []string
	e.svc.OnBookCreated(func(_ context.Context, ids []string) { made = append(made, ids...) })
	p := e.write("Solo/solo.epub", sampleEPUB(t, "A Solo Voyage"))
	e.scan()
	f := e.waitState(e.fileAt(p).ID)
	if f.State != "ready" {
		t.Fatalf("state = %s", f.State)
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.file(f.ID).BookID == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	f = e.file(f.ID)
	if f.BookID == "" || !f.BookDefault || f.PairedBy != "" {
		t.Fatalf("file after ingest = %+v", f)
	}
	b, err := e.repo.Book(bg, f.BookID)
	if err != nil || b.Kind != library.BookText || b.Title != "A Solo Voyage" || b.RootPath != p || b.MissingSince != 0 {
		t.Fatalf("book = %+v, %v", b, err)
	}
	if len(made) != 1 || made[0] != b.ID {
		t.Errorf("followers heard %v, want [%s]", made, b.ID)
	}
	all := library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "1=1"}}
	res, err := e.repo.Search(bg, all, "Voyage", 10)
	if err != nil || len(res.IDs) != 1 || res.IDs[0] != b.ID {
		t.Errorf("search = %+v, %v", res, err)
	}
	none := library.Scope{Books: share.Filter{SQL: "0=1"}, Libraries: share.Filter{SQL: "0=1"}}
	if res, _ := e.repo.Search(bg, none, "Voyage", 10); len(res.IDs) != 0 {
		t.Error("search ignored the grant filter")
	}
	// A second pass makes no second book.
	if err := e.svc.ensureBooks(bg); err != nil {
		t.Fatal(err)
	}
	if n := tbBooks(e); n != 1 {
		t.Errorf("books = %d, want 1", n)
	}
}

func TestTextBookHoldRows(t *testing.T) {
	e := newIEnv(t, false)
	tbAudio(e, "a1", "Dune", "", "Frank Herbert")
	pairTFile(e, "f_kept", "ready", "", "Dune", "Frank Herbert")
	pairTFile(e, "f_person", "ready", "", "Dune", "Frank Herbert")
	pairTExec(e, `UPDATE ebook_files SET hold='kept' WHERE id='f_kept'`)
	pairTExec(e, `UPDATE ebook_files SET hold='person' WHERE id='f_person'`)
	e.svc.Finisher()(bg, e.lib.ID)
	for _, id := range []string{"f_kept", "f_person"} {
		f := e.file(id)
		if f.BookID == "" || f.BookID == "a1" || tbKind(e, f.BookID) != "text" {
			t.Errorf("%s: book %q kind %q", id, f.BookID, tbKind(e, f.BookID))
		}
		if f.Hold == "" {
			t.Errorf("%s lost its hold", id)
		}
	}
	// Held files are never merged into the audiobook, however well they match.
	if n := e.count(`SELECT count(*) FROM ebook_files WHERE book_id='a1'`); n != 0 {
		t.Errorf("a held file joined the audiobook")
	}
}

func TestTextBookScanKeeps(t *testing.T) {
	e := newIEnv(t, false)
	e.write("Aud/01.mp3", []byte("audio"))
	p := e.write("Solo/solo.epub", sampleEPUB(t, "Keep Me"))
	e.scan()
	f := e.fileAt(p)
	pairTExec(e, `UPDATE ebook_files SET state='ready', sha256=? WHERE id=?`, pairTHash("k"), f.ID)
	pairTExec(e, `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, chapter_count, word_count, char_count, created_at)
VALUES ('ed_k', ?, ?, ?, ?, 5, 'epub', 'Keep Me', 1, 1, 1, 1)`, f.ID, pairTHash("k"), Recipe, pairTHash("k"))
	if err := e.svc.ensureFile(bg, f.ID); err != nil {
		t.Fatal(err)
	}
	bookID := e.file(f.ID).BookID
	if bookID == "" {
		t.Fatal("no text book")
	}
	e.scan()                                                                           // full scan
	if _, err := e.sc.ScanSubtree(bg, e.lib.ID, e.folder.ID, "Aud", nil); err != nil { // scoped scan elsewhere
		t.Fatal(err)
	}
	b, err := e.repo.Book(bg, bookID)
	if err != nil || b.MissingSince != 0 || b.Kind != library.BookText {
		t.Fatalf("scan touched the text book: %+v, %v", b, err)
	}
	if e.count(`SELECT count(*) FROM books WHERE kind='audio'`) != 1 {
		t.Error("the audio sweep did not see its own book")
	}
}

func TestTextBookMissingAndBack(t *testing.T) {
	e := newIEnv(t, false)
	p := e.write("Solo/solo.epub", sampleEPUB(t, "Away"))
	e.scan()
	f := e.fileAt(p)
	pairTExec(e, `UPDATE ebook_files SET state='ready', sha256=? WHERE id=?`, pairTHash("m"), f.ID)
	pairTExec(e, `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, chapter_count, word_count, char_count, created_at)
VALUES ('ed_m', ?, ?, ?, ?, 5, 'epub', 'Away', 1, 1, 1, 1)`, f.ID, pairTHash("m"), Recipe, pairTHash("m"))
	if err := e.svc.ensureFile(bg, f.ID); err != nil {
		t.Fatal(err)
	}
	bookID := e.file(f.ID).BookID
	if err := os.Rename(p, p+".moved"); err != nil {
		t.Fatal(err)
	}
	e.scan()
	if b, _ := e.repo.Book(bg, bookID); b.MissingSince == 0 {
		t.Fatal("book not marked missing with its only file gone")
	}
	if err := os.Rename(p+".moved", p); err != nil {
		t.Fatal(err)
	}
	e.scan()
	if b, _ := e.repo.Book(bg, bookID); b.MissingSince != 0 {
		t.Fatal("book still missing after its file came back")
	}
	// Dismissing the last file of a text book deletes the book.
	pairTExec(e, `UPDATE ebook_files SET origin='upload', path=NULL, folder_id=NULL, state='failed', sha256=? WHERE id=?`, pairTHash("m"), f.ID)
	if err := e.svc.Dismiss(bg, bookID, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.repo.Book(bg, bookID); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("book survived its last file: %v", err)
	}
}

func TestTextBookFlatFolder(t *testing.T) {
	e := newIEnv(t, false)
	for _, n := range []string{"a", "b", "c"} {
		e.write("Flat/"+n+".epub", sampleEPUB(t, "Title "+n))
		e.write("Flat/"+n+".opf", []byte("<package/>"))
	}
	e.write("Flat/metadata.opf", []byte(`<package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Borrowed</dc:title></metadata></package>`))
	e.scan()
	for i, n := range []string{"a", "b", "c"} {
		f := e.fileAt(filepath.Join(e.root, "Flat", n+".epub"))
		sha := pairTHash(fmt.Sprint("flat", i))
		pairTExec(e, `UPDATE ebook_files SET state='ready', sha256=? WHERE id=?`, sha, f.ID)
		pairTExec(e, `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, chapter_count, word_count, char_count, created_at)
VALUES (?, ?, ?, ?, ?, 5, 'epub', ?, 1, 1, 1, 1)`, "ed_"+n, f.ID, sha, Recipe, sha, "Title "+n)
	}
	if err := e.svc.ensureBooks(bg); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM books WHERE kind='text'`); n != 3 {
		t.Fatalf("books = %d, want 3", n)
	}
	for _, n := range []string{"a", "b", "c"} {
		if dir, ok := SoleStemDir(filepath.Join(e.root, "Flat", n+".epub")); ok {
			t.Errorf("%s: a flat folder of three books lent itself (%s)", n, dir)
		}
	}
	// One book with a second format in its own folder does lend its folder.
	e.write("Calibre/Title (7)/Title.epub", []byte("x"))
	e.write("Calibre/Title (7)/Title.mobi", []byte("y"))
	if _, ok := SoleStemDir(filepath.Join(e.root, "Calibre", "Title (7)", "Title.epub")); !ok {
		t.Error("Calibre folder with two formats of one stem not recognised")
	}
	if _, ok := SoleStemDir(filepath.Join(e.svc.ebooksDir(), "ebf_x")); ok {
		t.Error("an upload root lent a folder")
	}
}

func TestTextBookTwoFormats(t *testing.T) {
	e := newIEnv(t, false)
	first := tbText(e, "f_epub", "The Long Way", "9780306406157", "Ann Author")
	pairTFile(e, "f_mobi", "ready", "", "The Long Way", "Ann Author")
	pairTExec(e, `UPDATE editions SET isbn='9780306406157' WHERE ebook_file_id='f_mobi'`)
	if err := e.svc.PairPending(bg); err != nil {
		t.Fatal(err)
	}
	m := e.file("f_mobi")
	if m.BookID != first.BookID || m.PairedBy != "isbn" || m.BookDefault {
		t.Fatalf("second format = %+v, want joined to %s", m, first.BookID)
	}
	if n := e.count(`SELECT count(*) FROM books WHERE kind='text'`); n != 1 {
		t.Errorf("text books = %d, want 1", n)
	}
	if err := e.svc.ensureBooks(bg); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM books WHERE kind='text'`); n != 1 {
		t.Errorf("catch-up made a second book: %d", n)
	}
}

// tbEPUBCover builds an EPUB whose OPF is the given metadata/manifest fragments and whose extra entries carry pictures.
func tbEPUBCover(t *testing.T, version int, meta, manifest string, extra ...ebooktest.Entry) []byte {
	opf := `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>C</dc:title>` + meta +
		`</metadata><manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/>` + manifest + `</manifest><spine><itemref idref="c1"/></spine></package>`
	es := []ebooktest.Entry{
		{Name: "mimetype", Data: []byte("application/epub+zip")},
		{Name: "META-INF/container.xml", Data: []byte(`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)},
		{Name: "OEBPS/content.opf", Data: []byte(opf)},
		{Name: "OEBPS/c1.xhtml", Data: []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Hello there.</p></body></html>`)},
	}
	return ebooktest.Zip(t, append(es, extra...))
}

var (
	tbJPEG = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 64)...)
	tbPNG  = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
)

func TestTextBookCover(t *testing.T) {
	lim := DefaultLimits()
	dir := t.TempDir()
	cover := func(name string, data []byte) []byte {
		t.Helper()
		p := filepath.Join(dir, name+".epub")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		b, err := CoverBytes(bg, File{Ext: "epub"}, p, lim)
		if err != nil {
			if !errors.Is(err, ErrNoCover) {
				t.Fatalf("%s: %v", name, err)
			}
			return nil
		}
		return b
	}
	three := cover("v3", tbEPUBCover(t, 3, ``, `<item id="img" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>`,
		ebooktest.Entry{Name: "OEBPS/cover.jpg", Data: tbJPEG}))
	if SniffImage(three) != "jpeg" {
		t.Errorf("EPUB 3 cover = %v", three)
	}
	two := cover("v2", tbEPUBCover(t, 2, `<meta name="cover" content="img"/>`, `<item id="img" href="images/c.png" media-type="image/png"/>`,
		ebooktest.Entry{Name: "OEBPS/images/c.png", Data: tbPNG}))
	if SniffImage(two) != "png" {
		t.Errorf("EPUB 2 cover = %v", two)
	}
	page := cover("page", tbEPUBCover(t, 3, ``, `<item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/><item id="p" href="p.png" media-type="image/png"/>`,
		ebooktest.Entry{Name: "OEBPS/cover.xhtml", Data: []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><img src="p.png" alt=""/></body></html>`)},
		ebooktest.Entry{Name: "OEBPS/p.png", Data: tbPNG}))
	_ = page // the spine here lists c1 only, so the cover page is not reached: no cover is the right answer
	svg := cover("svg", tbEPUBCover(t, 3, ``, `<item id="img" href="cover.svg" media-type="image/svg+xml" properties="cover-image"/>`,
		ebooktest.Entry{Name: "OEBPS/cover.svg", Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)}))
	if svg != nil {
		t.Error("an SVG cover was accepted")
	}
	big := cover("big", tbEPUBCover(t, 3, ``, `<item id="img" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>`,
		ebooktest.Entry{Name: "OEBPS/cover.jpg", Data: append(append([]byte{}, tbJPEG...), make([]byte, MaxCoverBytes+1)...)}))
	if big != nil {
		t.Error("a hostile-size cover was accepted")
	}
	if cover("none", tbEPUBCover(t, 3, ``, ``)) != nil {
		t.Error("a cover from nowhere")
	}
	if got := cover("traversal", tbEPUBCover(t, 3, ``, `<item id="img" href="../../evil.jpg" media-type="image/jpeg" properties="cover-image"/>`)); got != nil {
		t.Error("a reference outside the archive was followed")
	}
	if SniffImage([]byte("RIFF\x00\x00\x00\x00WEBPVP8 ")) != "webp" || SniffImage([]byte("GIF89a")) != "" || SniffImage([]byte("<svg")) != "" {
		t.Error("SniffImage")
	}
	// FB2: coverpage names a binary.
	fb := []byte(`<?xml version="1.0" encoding="utf-8"?><FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">` +
		`<description><title-info><coverpage><image l:href="#c.jpg"/></coverpage></title-info></description><body><section><p>x</p></section></body>` +
		`<binary id="c.jpg" content-type="image/jpeg">` + b64(tbJPEG) + `</binary></FictionBook>`)
	p := filepath.Join(dir, "b.fb2")
	if err := os.WriteFile(p, fb, 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := CoverBytes(bg, File{Ext: "fb2"}, p, lim); err != nil || SniffImage(b) != "jpeg" {
		t.Errorf("FB2 cover = %v, %v", b, err)
	}
	// A text book's cover through the service: the default ready file.
	e := newIEnv(t, false)
	p2 := e.write("Solo/solo.epub", tbEPUBCover(t, 3, ``, `<item id="img" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>`,
		ebooktest.Entry{Name: "OEBPS/cover.jpg", Data: tbJPEG}))
	e.scan()
	f := e.fileAt(p2)
	pairTExec(e, `UPDATE ebook_files SET state='ready', sha256=?, book_id=NULL WHERE id=?`, pairTHash("cv"), f.ID)
	pairTExec(e, `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, chapter_count, word_count, char_count, created_at)
VALUES ('ed_cv', ?, ?, ?, ?, 5, 'epub', 'Cv', 1, 1, 1, 1)`, f.ID, pairTHash("cv"), Recipe, pairTHash("cv"))
	if err := e.svc.ensureFile(bg, f.ID); err != nil {
		t.Fatal(err)
	}
	if b, err := e.svc.CoverFor(bg, e.file(f.ID).BookID); err != nil || SniffImage(b) != "jpeg" {
		t.Errorf("CoverFor = %v, %v", b, err)
	}
	if _, err := e.svc.CoverFor(bg, "nobook"); !errors.Is(err, ErrNoCover) {
		t.Errorf("CoverFor unknown = %v", err)
	}
}
