package web

import (
	"bytes"
	"context"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// Print pages (M5 G3): the reader shows the book's own page labels. Helpers start with "pp".

// ppWebEPUB has a front chapter before the first page mark, and a long second chapter that is cut into
// several parts, with a mark in a part after the first.
func ppWebEPUB(t *testing.T) []byte {
	t.Helper()
	var sb strings.Builder
	for i := 1; i <= 400; i++ {
		id := ""
		if i == 1 || i == 350 {
			id = ` id="m` + string(rune('a'+i%26)) + `"`
		}
		sb.WriteString("<p" + id + ">Paragraph " + strings.Repeat("number words go on and on here. ", 12) + "</p>")
	}
	return ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Paged", Authors: []string{"A. Writer"},
		PageLabels: []string{"xii", "13"},
		PageHrefs:  []string{"ch2.xhtml#" + ppID(1), "ch2.xhtml#" + ppID(350)},
		Chapters: []ebooktest.EPUBChapter{
			{Title: "Front", Raw: `<h1>Front</h1><p>Before any page.</p>`},
			{Title: "Body", Raw: sb.String()},
		}})
}

func ppID(i int) string { return "m" + string(rune('a'+i%26)) }

var ppPg = regexp.MustCompile(`data-pg="([^"]*)"`)

func ppLabels(s string) []string {
	var out []string
	for _, m := range ppPg.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestPrintPagesReader(t *testing.T) {
	l := rsSeed(t, ppWebEPUB(t))
	if got := len(l.doc.PageList); got != 2 {
		t.Fatalf("marks = %v", l.doc.PageList)
	}
	// The front chapter is before the first mark: blank labels and no "p. x of y" line.
	front := l.get(l.owner, "/hx/books/"+l.book.ID+"/read/chapters/1", true).Body.String()
	for _, p := range ppLabels(front) {
		if p != "" {
			t.Errorf("front block labelled %q", p)
		}
	}
	if strings.Contains(front, "p. ") {
		t.Error("the front chapter shows a page line")
	}
	// Chapter 2, part 1: the first block opens page xii and the footer says so.
	body := l.get(l.owner, "/hx/books/"+l.book.ID+"/read/chapters/2", true).Body.String()
	if ls := ppLabels(body); len(ls) == 0 || ls[0] != "xii" || ls[len(ls)-1] != "xii" {
		t.Errorf("part 1 labels = %v", ls)
	}
	if want := "p. xii of 13"; !strings.Contains(html.UnescapeString(body), want) {
		t.Errorf("no %q in the chapter head", want)
	}
	// A later part: the label changes to 13 inside it, at the marked block, not before.
	var found bool
	for _, part := range ofrManifest(t, l.get(l.owner, "/offline/books/"+l.book.ID+"/manifest", false)).Parts {
		ls := ppLabels(l.get(l.owner, part, true).Body.String())
		for _, p := range ls {
			switch {
			case p == "13":
				found = true
			case p == "xii" && found:
				t.Error("page xii after page 13")
			case p == "" && !found:
				// the front chapter, before the first mark
			case p != "xii":
				t.Errorf("unexpected label %q", p)
			}
		}
	}
	if !found {
		t.Error("no part carries page 13")
	}
	// A book with no page list keeps the arithmetic: numbers, from 1.
	l2 := rsSeed(t, ofrEPUB(t))
	p := ppLabels(l2.get(l2.owner, "/hx/books/"+l2.book.ID+"/read/chapters/1", true).Body.String())
	if len(p) == 0 || p[0] != "1" {
		t.Errorf("fallback labels start %v", p[:min(3, len(p))])
	}
}

// TestPrintPagesOffline: the recipe bump is the first real run of the offline revision path. A book whose
// edition was made by recipe v2 is read again; the offline manifest then names the new edition and a new
// revision (so a saved copy is stored again), the old edition's addresses refuse, and the highlight
// moves to the new edition on the same sentence.
func TestPrintPagesOffline(t *testing.T) {
	// Like rsSeed, but the runner keeps going: the second read happens in this test.
	l := &rsLab{textLab: newTextLab(t)}
	l.run(t)
	f, err := l.svc.AddUpload(context.Background(), l.book, l.ownerID, "serve.epub", bytes.NewReader(ppWebEPUB(t)))
	if err != nil {
		t.Fatal(err)
	}
	l.file = l.waitState(t, f.ID, "ready")
	l.ed, _, err = l.svc.Store().CurrentEdition(context.Background(), l.file.ID)
	if err != nil {
		t.Fatal(err)
	}
	if l.doc, err = ebook.ReadDoc(l.a.dir+"/text", l.ed.DocSHA256); err != nil {
		t.Fatal(err)
	}
	ofrAllowDownload(t, l)
	ctx := context.Background()
	path := "/offline/books/" + l.book.ID + "/manifest"

	// Make the stored edition what recipe v2 made: the same text and no page list.
	old := *l.doc
	old.PageList = nil
	old.Recipe = "tendshelf-ebook-v2"
	js, err := ebook.Encode(&old)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := ebook.WriteDoc(l.a.dir+"/text", js)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE editions SET recipe = 'tendshelf-ebook-v2', doc_sha256 = ?, doc_bytes = ? WHERE id = ?`, sha, len(js), l.ed.ID); err != nil {
		t.Fatal(err)
	}
	sid := ebook.SentenceID("c2.p3", 0)
	hl, _, err := l.a.srv.progress.AddHighlight(ctx, progress.Highlight{UserID: l.ownerID, BookID: l.book.ID, EditionID: l.ed.ID, Chapter: 2,
		StartSID: sid, EndSID: sid, StartOff: 0, EndOff: 9, Quote: "Paragraph"})
	if err != nil {
		t.Fatal(err)
	}
	before := ofrManifest(t, l.get(l.owner, path, false))
	oldPart := before.Parts[0]
	// The saved copy's parts have no labels: v2 had a page list of none.
	if got := l.get(l.owner, oldPart, true); got.Code != http.StatusOK {
		t.Fatalf("old part: %d", got.Code)
	}

	// Read again: the stale file goes pending and the old edition answers meanwhile.
	if err := l.svc.EnqueueStale(ctx); err != nil {
		t.Fatal(err)
	}
	if rec := l.get(l.owner, oldPart, true); rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
		t.Fatalf("while pending: %d", rec.Code)
	}
	if err := l.svc.EnqueuePending(ctx); err != nil {
		t.Fatal(err)
	}
	var cur ebook.Edition
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(25 * time.Millisecond) {
		e, ok, err := l.svc.Store().CurrentEdition(ctx, l.file.ID)
		if err == nil && ok && e.ID != l.ed.ID && e.Recipe == ebook.Recipe {
			cur = e
			break
		}
	}
	if cur.ID == "" {
		t.Fatal("the book was not read again")
	}

	after := ofrManifest(t, l.get(l.owner, path, false))
	if after.EditionID != cur.ID || after.EditionID == before.EditionID || after.Revision == before.Revision {
		t.Errorf("manifest did not move: before %+v after %+v", before.Revision, after.Revision)
	}
	// The old copy's address refuses safely; the new one answers with the labels.
	if rec := l.get(l.owner, oldPart, true); rec.Code != http.StatusNotFound {
		t.Errorf("old edition address: %d, want 404", rec.Code)
	}
	// A place a phone queued against the old edition is refused plainly, and nothing is stored.
	form := map[string][]string{"_csrf": {l.a.tokenFor(t, l.owner)}, "edition_id": {l.ed.ID}, "chapter_idx": {"2"}, "sid": {sid}, "off": {"0"},
		"quote": {"Paragraph"}, "char_pos": {"10"}, "percent_bp": {"10"}, "client_updated_at": {"5"}, "device_id": {"phone"}}
	if got := l.a.do(http.MethodPost, "/reading/"+l.book.ID, form, func(r *http.Request) { r.AddCookie(l.owner) }); got.Code != http.StatusUnprocessableEntity {
		t.Errorf("place on the old edition: %d, want 422", got.Code)
	}
	rec := l.get(l.owner, after.Parts[0], true)
	if rec.Code != http.StatusOK {
		t.Fatalf("new part: %d", rec.Code)
	}
	if ls := ppLabels(rec.Body.String()); len(ls) == 0 {
		t.Error("the new part has no blocks")
	}
	// The highlight moved to the new edition at the same place.
	var ed, ssid string
	var off int
	if err := l.a.st.Reader().QueryRow(`SELECT COALESCE(edition_id,''), start_sid, start_off FROM highlights WHERE id = ?`, hl.ID).Scan(&ed, &ssid, &off); err != nil {
		t.Fatal(err)
	}
	if ed != cur.ID || ssid != sid || off != 0 {
		t.Errorf("highlight = %q %q %d", ed, ssid, off)
	}
}
