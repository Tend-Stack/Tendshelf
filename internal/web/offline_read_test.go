package web

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// Offline reading, server side (G6a, decision 0019). Helpers start with "ofr".

// ofrEPUB is a book with a 2 MB first chapter (many parts), then a chapter with a PNG and an SVG picture.
func ofrEPUB(t *testing.T) []byte {
	t.Helper()
	para := strings.Repeat("The long sentence goes on and on without any pause at all. ", 7)
	seed := uint32(11)
	word := func() string {
		seed = seed*1664525 + 1013904223
		return strconv.FormatUint(uint64(seed), 36)
	}
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		var p strings.Builder
		for p.Len() < len(para) {
			p.WriteString(word() + " ")
		}
		sb.WriteString("<p>" + p.String() + "</p>")
	}
	return ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Offline", Authors: []string{"A. Writer"},
		Chapters: []ebooktest.EPUBChapter{
			{Title: "Everything", Raw: sb.String()},
			{Title: "Pictures", Raw: `<p>Before.</p><img src="pic.png" alt="Lamp"/><img src="pic.svg" alt="Vector"/><p>After.</p>`},
		},
		ManifestXML: `<item id="p" href="pic.png" media-type="image/png"/><item id="v" href="pic.svg" media-type="image/svg+xml"/>`,
		Extra: []ebooktest.Entry{{Name: "OEBPS/pic.png", Data: rsPNG(5, 4)},
			{Name: "OEBPS/pic.svg", Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="5" height="4"/>`)}}})
}

// ofrManifest decodes the manifest answer.
func ofrManifest(t *testing.T, rec *httptest.ResponseRecorder) offlineManifest {
	t.Helper()
	var m offlineManifest
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// ofrAllowDownload gives the member who may read the right to download too.
func ofrAllowDownload(t *testing.T, l *rsLab) {
	t.Helper()
	id := sweepUserID(t, l.a, l.reader)
	if _, err := l.a.st.Writer().Exec(`UPDATE grants SET can_download = 1 WHERE user_id = ?`, id); err != nil {
		t.Fatal(err)
	}
}

func TestOffReadManifest(t *testing.T) {
	l := rsSeed(t, ofrEPUB(t))
	path := "/offline/books/" + l.book.ID + "/manifest"
	// A book with no text, in this library and in another one.
	lib2, folder2 := librarytest.NewLibrary(t, l.a.lib, "Other library", adminAudioDir(t, true))
	_ = lib2
	audio := librarytest.AddBook(t, l.a.lib, folder2, librarytest.BookSpec{Title: "Only audio", Authors: []string{"B. Reader"}, DurationMs: sweepHourMs, AddedAt: 6, Files: 1})

	m := ofrManifest(t, l.get(l.owner, path, false))
	if rec := l.get(l.owner, path, false); rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if m.BookID != l.book.ID || m.EditionID != l.ed.ID || m.Title != l.book.Title || len(m.Revision) != 16 || m.FinishedAt != 0 {
		t.Errorf("manifest = %+v", m)
	}

	// The parts are exactly the addresses a reader walks with the next links, from part 1.
	var walked []string
	next := m.Parts[0]
	for next != "" && len(walked) <= len(m.Parts) {
		walked = append(walked, next)
		rec := l.get(l.owner, next, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", next, rec.Code)
		}
		next = html.UnescapeString(rsAttr(rec.Body.String(), "data-next-href"))
	}
	if strings.Join(walked, "\n") != strings.Join(m.Parts, "\n") {
		t.Errorf("manifest has %d parts, the next links walk %d", len(m.Parts), len(walked))
	}
	if len(m.Parts) < 40 {
		t.Errorf("%d parts for 2 MB", len(m.Parts))
	}

	// The PNG is listed, the SVG (which the image route refuses) is not.
	if len(m.Images) != 1 {
		t.Fatalf("images = %v", m.Images)
	}
	if rec := l.get(l.owner, m.Images[0], false); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("the listed picture answers %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if want := 3*l.ed.CharCount + 150_000; m.Bytes != want {
		t.Errorf("bytes = %d, want %d", m.Bytes, want)
	}

	// Who may ask.
	table := []struct {
		name string
		ck   *http.Cookie
		path string
		want int
	}{
		{"member with ReadText, no download", l.reader, path, http.StatusNotFound},
		{"member without ReadText", l.noRead, path, http.StatusNotFound},
		{"owner, book with no text", l.owner, "/offline/books/" + audio.ID + "/manifest", http.StatusNotFound},
		{"member, book of another library", l.reader, "/offline/books/" + audio.ID + "/manifest", http.StatusNotFound},
		{"unknown book", l.owner, "/offline/books/nope/manifest", http.StatusNotFound},
		{"signed out", nil, path, http.StatusSeeOther},
	}
	for _, c := range table {
		rec := l.get(c.ck, c.path, false)
		if rec.Code != c.want && !(c.ck == nil && rec.Code == http.StatusFound) {
			t.Errorf("%s: %d, want %d", c.name, rec.Code, c.want)
		}
	}
	ofrAllowDownload(t, l)
	if rec := l.get(l.reader, path, false); rec.Code != http.StatusOK {
		t.Errorf("member with ReadText and DownloadBook: %d", rec.Code)
	}

	// The revision follows the person's highlights.
	ctx := context.Background()
	sid := ebook.SentenceID(l.doc.Chapters[0].Blocks[0].ID, 0)
	hl, _, err := l.a.srv.progress.AddHighlight(ctx, progress.Highlight{UserID: l.ownerID, BookID: l.book.ID, EditionID: l.ed.ID, Chapter: 1,
		StartSID: sid, EndSID: sid, StartOff: 0, EndOff: 3, Quote: "The"})
	if err != nil {
		t.Fatal(err)
	}
	withHL := ofrManifest(t, l.get(l.owner, path, false)).Revision
	if withHL == m.Revision {
		t.Error("the revision did not change after a highlight")
	}
	if err := l.a.srv.progress.DeleteHighlight(ctx, l.ownerID, hl.ID); err != nil {
		t.Fatal(err)
	}
	if after := ofrManifest(t, l.get(l.owner, path, false)).Revision; after == withHL {
		t.Error("the revision did not change after a deleted highlight")
	}
}

func TestOffReadShell(t *testing.T) {
	l := rsSeed(t, ofrEPUB(t))
	path := "/offline/books/" + l.book.ID + "/read"
	rec := l.get(l.owner, path, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("shell: %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	b := rec.Body.String()
	for _, absent := range []string{`class="ts-reader__text"`, `id="reader-ch-`, "sheet=search", "sheet=marks", "sheet=reading", `data-ts-mark="bookmark"`,
		`data-ts-mark="note"`, `data-ts-mark="highlight"`, `ts-aa`, "/bookmarks", "data-ts-offline-shell", "data-ts-module=\"/static/js/reader."} {
		if strings.Contains(b, absent) {
			t.Errorf("the shell holds %q", absent)
		}
	}
	for _, present := range []string{"data-offline-shell", `id="reader-text"`, "data-reader-src=", "data-placed=", `data-start-ch="1"`, `data-start-part="1"`,
		`data-frag="/hx/books/` + l.book.ID + `/read/chapters/{c}?edition=` + l.ed.ID + `"`, `data-part-starts="{&#34;1&#34;:[1,`, `data-ts-mark="copy"`, "sheet=contents"} {
		if !strings.Contains(b, present) {
			t.Errorf("the shell lacks %q", present)
		}
	}
	// The contents variant has the sheet, and closing it goes to the plain shell address.
	c := l.get(l.owner, path+"?sheet=contents", false)
	if c.Code != http.StatusOK || !strings.Contains(c.Body.String(), `id="reader-sheet-title"`) ||
		!strings.Contains(c.Body.String(), `href="/books/`+l.book.ID+`/read"`) || strings.Contains(c.Body.String(), "ch=1&amp;sheet") {
		t.Errorf("contents variant: %d", c.Code)
	}
	for _, q := range []string{"?sheet=marks", "?sheet=search", "?sheet=reading", "?sheet=other"} {
		if rec := l.get(l.owner, path+q, false); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	if l.get(l.reader, path, false).Code != http.StatusNotFound {
		t.Error("a member without the download right opened the shell")
	}
	// An ordinary reader page names the offline module too, without the shell attributes.
	page := l.get(l.owner, l.page(""), false).Body.String()
	if !strings.Contains(page, "js/reader-offline.") || strings.Contains(page, "data-offline-shell") {
		t.Error("the online reader page must carry the offline marker, not the shell attributes")
	}
}

func TestOffReadFragURL(t *testing.T) {
	l := rsSeed(t, ofrEPUB(t))
	b := l.get(l.owner, "/offline/books/"+l.book.ID+"/read", false).Body.String()
	frag := html.UnescapeString(rsAttr(b, "data-frag"))
	rc := readerCtx{book: library.Book{ID: l.book.ID}, ed: l.ed}
	for part := 1; part <= 2; part++ {
		got := strings.Replace(frag, "{c}", "1", 1)
		if part > 1 {
			got += "&part=" + strconv.Itoa(part)
		}
		if want := rc.rsFragURL(1, part); got != want {
			t.Errorf("part %d: %q, want %q", part, got, want)
		}
	}
}

func TestOffReadPrecache(t *testing.T) {
	l := rsSeed(t, textEPUB(t, "Precache", 1))
	rec := l.a.do(http.MethodGet, "/sw.js", nil, nil)
	a, ok := l.a.srv.assets.Asset("css/pages-reader.css")
	if rec.Code != http.StatusOK || !ok || !strings.Contains(rec.Body.String(), "/static/"+a.Hashed) {
		t.Errorf("the worker's asset list lacks the reader stylesheet (%d)", rec.Code)
	}
}

func TestOffReadSessionDate(t *testing.T) {
	l := rsSeed(t, textEPUB(t, "Session date", 2))
	sid := ebook.SentenceID(l.doc.Chapters[1].Blocks[0].ID, 0)
	now := time.Now().UnixMilli()
	day := int64(86_400_000)
	cases := []struct {
		name     string
		cu       int64
		device   string
		wantNear int64
	}{
		{"2 days old counts then", now - 2*day, "ofrdevice001", now - 2*day},
		{"the future counts now", now + 5*day, "ofrdevice002", now},
		{"31 days old counts now", now - 31*day, "ofrdevice003", now},
	}
	for _, c := range cases {
		form := url.Values{"edition_id": {l.ed.ID}, "chapter_idx": {"2"}, "sid": {sid}, "off": {"0"}, "quote": {"first"}, "char_pos": {"10"},
			"client_updated_at": {strconv.FormatInt(c.cu, 10)}, "device_id": {c.device}, "chars_read": {"100"}, "active_ms": {"5000"}, "_csrf": {l.a.tokenFor(t, l.owner)}}
		if rec := l.a.do(http.MethodPost, "/reading/"+l.book.ID, form, nil, l.owner); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
		var started int64
		if err := l.a.st.Reader().QueryRow(`SELECT started_at FROM reading_sessions WHERE device_id = ?`, c.device).Scan(&started); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if d := started - c.wantNear; d < -60_000 || d > 60_000 {
			t.Errorf("%s: session started %d ms from %d", c.name, d, c.wantNear)
		}
	}
}
