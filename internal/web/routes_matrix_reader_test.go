package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// rdrSweep names what the reader rows act on: the edition, and a highlight per verb for each staff person
// (delete uses its own up).
type rdrSweep struct {
	ed        string
	patch, hl [2]string
}

// readerSeed gives the sweep app a ready edition with a one-chapter synthetic document and highlights for the owner and the admin.
func readerSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	a := e.a
	ctx := context.Background()
	blk := ebook.Block{ID: "c1.p1", Kind: ebook.KindP, Text: "Alpha beta. Gamma delta.", Runs: []ebook.Run{},
		Sentences: [][2]int{{0, 11}, {12, 24}}, Words: [][2]int{{0, 5}, {6, 10}, {12, 17}, {18, 23}}}
	doc := &ebook.Doc{V: ebook.DocV, Recipe: ebook.Recipe, Format: "epub", Layout: "reflow", PageCount: 3,
		Chapters: []ebook.Chapter{{Idx: 1, Title: "One", Blocks: []ebook.Block{blk}}}}
	raw, err := ebook.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := ebook.WriteDoc(a.dir+"/text", raw)
	if err != nil {
		t.Fatal(err)
	}
	id := placeEbook(t, a, e.book, e.uid[sweepOwner], "reader.epub", "ready", "")
	fsha := fmt.Sprintf("%064x", 9100)
	if _, err := a.st.Writer().ExecContext(ctx, `UPDATE ebook_files SET sha256 = ?, quick_hash = ? WHERE id = ?`, fsha, fsha, id); err != nil {
		t.Fatal(err)
	}
	e.rdr.ed = "ed_rdrsweep"
	if _, err := a.st.Writer().ExecContext(ctx, `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, language, chapter_count, word_count, char_count, created_at)
VALUES (?, ?, ?, ?, ?, 5, 'epub', 'en', 1, 4, 24, 1)`, e.rdr.ed, id, fsha, ebook.Recipe, sha); err != nil {
		t.Fatal(err)
	}
	for person := 0; person < 2; person++ {
		for _, dst := range []*[2]string{&e.rdr.patch, &e.rdr.hl} {
			h, _, err := a.srv.progress.AddHighlight(ctx, progress.Highlight{UserID: e.uid[person], BookID: e.book.ID, EditionID: e.rdr.ed, Chapter: 1,
				StartSID: "c1.p1.s0", EndSID: "c1.p1.s0", StartOff: 0, EndOff: 5, Quote: "Alpha"})
			if err != nil {
				t.Fatal(err)
			}
			dst[person] = h.ID
		}
	}
}

// readerSweepRows are the rows of the reader . Reading needs share.ReadText, which the sweep's member
// and guest lack (TestRouteMatrixReaderGranted gives them can_read); highlights are the caller's own.
func readerSweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	q := func(e *sweepEnv) string { return "?edition=" + url.QueryEscape(e.rdr.ed) }
	page := sweepGet("GET /books/{id}/read", func(e *sweepEnv, _ int) string { return "/books/" + e.book.ID + "/read" + q(e) }, staff)
	chapter := sweepGet("GET /hx/books/{id}/read/chapters/{idx}", func(e *sweepEnv, _ int) string {
		return "/hx/books/" + e.book.ID + "/read/chapters/1" + q(e)
	}, staff)
	create := sweepPost("POST /hx/books/{id}/highlights", func(e *sweepEnv, _ int) string { return "/hx/books/" + e.book.ID + "/highlights" },
		func(e *sweepEnv, _ int) url.Values {
			return url.Values{"edition": {e.rdr.ed}, "start_sid": {"c1.p1.s0"}, "start_off": {"0"}, "end_sid": {"c1.p1.s0"}, "end_off": {"5"}}
		}, staff, "")
	patch := sweepRow{Pattern: "PATCH /hx/highlights/{id}", Method: http.MethodPatch, Want: staff, HX: true,
		Path: func(e *sweepEnv, role int) string { return "/hx/highlights/" + e.rdr.patch[min(role, 1)] },
		Form: sweepFormOf(url.Values{"note": {"a note"}})}
	del := sweepRow{Pattern: "DELETE /hx/highlights/{id}", Method: http.MethodDelete, Want: staff, HX: true,
		Path: func(e *sweepEnv, role int) string { return "/hx/highlights/" + e.rdr.hl[min(role, 1)] }}
	create.HX = true
	// RD1: the picture route (the seeded block is text, so everyone gets 404; the rights and the picture
	// itself are in TestReaderServeImage), and the reading place API on the /progress pattern.
	image := sweepGet("GET /books/{id}/read/images/{blockID}", func(e *sweepEnv, _ int) string {
		return "/books/" + e.book.ID + "/read/images/c1.p1" + q(e)
	}, sweepSame(http.StatusNotFound))
	readingGet := sweepGet("GET /reading/{bookID}", func(e *sweepEnv, _ int) string { return "/reading/" + e.book.ID }, staff)
	readingPost := sweepPost("POST /reading/{bookID}", func(e *sweepEnv, _ int) string { return "/reading/" + e.book.ID },
		func(e *sweepEnv, _ int) url.Values {
			return url.Values{"edition_id": {e.rdr.ed}, "chapter_idx": {"1"}, "sid": {"c1.p1.s0"}, "off": {"0"}, "quote": {"Alpha"},
				"char_pos": {"3"}, "client_updated_at": {"1800000000000"}, "device_id": {"sweepdevice03"}}
		}, staff, "")
	// RD2: "Mark as finished" at the end of the book writes the reading place only.
	finished := sweepPost("POST /reading/{bookID}/finished", func(e *sweepEnv, _ int) string { return "/reading/" + e.book.ID + "/finished" },
		sweepFormOf(url.Values{"finished": {"1"}}), sweepStaff(http.StatusSeeOther, http.StatusNotFound), "")
	finished.LocOf = func(e *sweepEnv) string { return "/books/" + e.book.ID }
	// RD3: the footnote popup, search and the marks export are reads of the text and answer to ReadText like the page.
	note := sweepGet("GET /hx/books/{id}/read/note/{blockID}", func(e *sweepEnv, _ int) string {
		return "/hx/books/" + e.book.ID + "/read/note/c1.p1" + q(e)
	}, staff)
	search := sweepGet("GET /hx/books/{id}/read/search", func(e *sweepEnv, _ int) string {
		return "/hx/books/" + e.book.ID + "/read/search" + q(e) + "&q=alpha"
	}, staff)
	rows := []sweepRow{page, chapter, create, patch, del, image, readingGet, readingPost, finished, note, search}
	for _, ext := range []string{"md", "txt", "json"} {
		rows = append(rows, sweepGet("GET /books/{id}/read/marks."+ext, func(e *sweepEnv, _ int) string {
			return "/books/" + e.book.ID + "/read/marks." + ext + q(e)
		}, staff))
	}
	// G6a (decision 0019): the offline reading manifest and shell need ReadText and DownloadBook.
	for _, tail := range []string{"manifest", "read"} {
		rows = append(rows, sweepGet("GET /offline/books/{id}/"+tail, func(e *sweepEnv, _ int) string {
			return "/offline/books/" + e.book.ID + "/" + tail
		}, staff))
	}
	return rows
}

// TestRouteMatrixReaderGranted: a member and a guest with can_read reach the reader pages; with a grant that
// lacks can_read they get 404 on every text route.
func TestRouteMatrixReaderGranted(t *testing.T) {
	for _, canRead := range []int{1, 0} {
		e := sweepSeed(t)
		for _, role := range []int{sweepMember, sweepGuest} {
			if _, err := e.a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at)
VALUES (?, ?, 'library', ?, 0, ?, 1, 1)`, "gr_rdr_"+sweepRoleNames[role], e.uid[role], e.libID, canRead); err != nil {
				t.Fatal(err)
			}
		}
		want := http.StatusNotFound
		if canRead == 1 {
			want = http.StatusOK
		}
		for _, row := range readerSweepRows()[:3] {
			for _, role := range []int{sweepMember, sweepGuest} {
				if rec := e.send(t, row, role, true); rec.Code != want {
					t.Errorf("can_read=%d %s as %s: %d, want %d", canRead, row.Pattern, sweepRoleNames[role], rec.Code, want)
				}
			}
		}
	}
}
