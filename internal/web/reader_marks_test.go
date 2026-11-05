package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// Footnotes, search, export and plain errors of the reader (M5 RD3). Helpers are rn*.

// rnNoteLab is an EPUB whose first chapter has three footnote links: two short notes and one with fifteen paragraphs.
func rnNoteLab(t *testing.T) *rsLab {
	t.Helper()
	long := strings.Repeat(`<p>Long note paragraph.</p>`, 15)
	epub := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Notes", Authors: []string{"A. Writer"}, Chapters: []ebooktest.EPUBChapter{
		{Title: "Text", EpubType: "chapter", Raw: `<p>Alpha<a epub:type="noteref" href="ch2.xhtml#n1">1</a> beta<a epub:type="noteref" href="ch2.xhtml#n2">2</a> gamma` +
			`<a epub:type="noteref" href="ch2.xhtml#big">3</a>.</p>`},
		{Title: "Notes", EpubType: "chapter", Raw: `<aside epub:type="footnote" id="n1"><p>Note one.</p><p>Still note one.</p></aside>` +
			`<aside epub:type="footnote" id="n2"><p>Note two.</p></aside><aside epub:type="footnote" id="big">` + long + `</aside>`},
	}})
	return rsSeed(t, epub)
}

// rnNoteTargets lists the block ids the note links of the document point to, in order.
func rnNoteTargets(d *ebook.Doc) []string {
	var out []string
	for _, c := range d.Chapters {
		for _, b := range c.Blocks {
			for _, l := range b.Links {
				if l.Note {
					out = append(out, l.To)
				}
			}
		}
	}
	return out
}

func TestReaderNoteFragment(t *testing.T) {
	l := rnNoteLab(t)
	ids := rnNoteTargets(l.doc)
	if len(ids) != 3 {
		t.Fatalf("note links: %v", ids)
	}
	note := func(ck *http.Cookie, id, q string) *struct {
		code int
		body string
	} {
		rec := l.get(ck, "/hx/books/"+l.book.ID+"/read/note/"+id+q, true)
		return &struct {
			code int
			body string
		}{rec.Code, rec.Body.String()}
	}
	// The first note: its own paragraphs, up to the next note that has a link; a way to the note and a way back.
	one := note(l.owner, ids[0], "")
	for _, want := range []string{"<dialog", "Note one.", "Still note one.", "Go to the note", "Close", `href="/books/` + l.book.ID + `/read?`} {
		if !strings.Contains(one.body, want) {
			t.Errorf("first note lacks %q: %s", want, one.body)
		}
	}
	if one.code != http.StatusOK || strings.Contains(one.body, "Note two.") || strings.Contains(one.body, `id="s-`) {
		t.Errorf("first note: %d, leaks the next note or sentence ids: %s", one.code, one.body)
	}
	// The chain stops at ten blocks.
	if got := strings.Count(note(l.owner, ids[2], "").body, "Long note paragraph."); got != readerNoteChain {
		t.Errorf("long note shows %d paragraphs, want %d", got, readerNoteChain)
	}
	// Rights: a reader may; a member without can_read, a guest without it and a visitor with no session may not.
	if c := note(l.reader, ids[0], "").code; c != http.StatusOK {
		t.Errorf("reader: %d", c)
	}
	for name, ck := range map[string]*http.Cookie{"member without can_read": l.noRead, "guest without can_read": l.guestNoR} {
		if c := note(ck, ids[0], "").code; c != http.StatusNotFound {
			t.Errorf("%s: %d", name, c)
		}
	}
	if rec := l.a.do(http.MethodGet, "/hx/books/"+l.book.ID+"/read/note/"+ids[0], nil, nil); rec.Code == http.StatusOK {
		t.Error("anonymous visitor got a footnote")
	}
	// Another edition, an edition that is not this book's, and a block that does not exist are all 404.
	if c := note(l.owner, ids[0], "?edition=ed_nope").code; c != http.StatusNotFound {
		t.Errorf("other edition: %d", c)
	}
	if c := note(l.owner, "c9.p9", "").code; c != http.StatusNotFound {
		t.Errorf("missing block: %d", c)
	}
	if c := note(l.owner, ids[0], "?edition="+l.ed.ID).code; c != http.StatusOK {
		t.Errorf("own edition: %d", c)
	}
	// The page carries the popup address on the link and the script that opens it.
	page := l.get(l.owner, l.page("ch=1"), false).Body.String()
	if !strings.Contains(page, `data-note-href="/hx/books/`+l.book.ID+`/read/note/`+ids[0]) || !strings.Contains(page, `data-notes-src="/static/`) {
		t.Errorf("page lacks the footnote hooks")
	}
}

// rnDoc is a one-chapter document of plain paragraphs, for the search that needs no server.
func rnDoc(paras ...string) *ebook.Doc {
	var blocks []ebook.Block
	for i, p := range paras {
		blocks = append(blocks, ebook.Block{ID: "c1.p" + strconv.Itoa(i+1), Kind: ebook.KindP, Text: p})
	}
	return &ebook.Doc{Chapters: []ebook.Chapter{{Idx: 1, Title: "One", Blocks: blocks}}}
}

func TestReaderSearchMatching(t *testing.T) {
	d := rnDoc("Un café au lait, s'il vous plaît.", "😀 find me, FIND ME.", "Two  spaces\nand a newline.", "ß and Straße")
	for _, c := range []struct {
		q          string
		n          int
		start, end int // first hit, UTF-16 offsets
	}{
		{"CAFE", 1, 3, 7},            // accents and case are ignored
		{"café au", 1, 3, 10},        // a phrase
		{"find me", 2, 3, 10},        // after an emoji that takes two units; case ignored
		{"spaces and", 1, 5, 15},     // runs of white space are one
		{"strasse", 1, 6, 12},        // ß folds to ss
		{"plait.", 1, 27, 33},        // punctuation is kept
		{"nothing here", 0, 0, 0},    //
		{"café  au  lait", 1, 3, 15}, // the question's own white space too
	} {
		hits, capped := rdrSearch(d, c.q, 200)
		if len(hits) != c.n || capped {
			t.Errorf("%q: %d hits (capped %v), want %d", c.q, len(hits), capped, c.n)
			continue
		}
		if c.n > 0 && (hits[0].Start != c.start || hits[0].End != c.end) {
			t.Errorf("%q: first hit %d-%d, want %d-%d", c.q, hits[0].Start, hits[0].End, c.start, c.end)
		}
	}
	// The cap: 250 matches give 200 and say so.
	var many []string
	for range 250 {
		many = append(many, "needle")
	}
	hits, capped := rdrSearch(rnDoc(many...), "needle", readerSearchHits)
	if len(hits) != readerSearchHits || !capped {
		t.Errorf("cap: %d hits, capped %v", len(hits), capped)
	}
	// The snippet keeps a pair whole and marks the cut.
	before, hit, after := rdrSnippet(strings.Repeat("😀", 60)+"x"+strings.Repeat("😀", 60), 120, 121)
	if hit != "x" || !strings.HasPrefix(before, "…") || !strings.HasSuffix(after, "…") || strings.ContainsRune(before+after, '�') {
		t.Errorf("snippet: %q %q %q", before, hit, after)
	}
}

func TestReaderSearch(t *testing.T) {
	var needle strings.Builder
	for i := 0; i < 230; i++ {
		needle.WriteString("<p>Needle number " + strconv.Itoa(i) + ".</p>")
	}
	l := rsSeed(t, ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Find", Authors: []string{"A. Writer"}, Chapters: []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"Find"}},
		{Title: "Cafés", EpubType: "chapter", Paras: []string{"Un café au lait avec un croissant."}},
		{Title: "Needles", EpubType: "chapter", Raw: needle.String()},
	}}))
	hx := func(ck *http.Cookie, q string) (int, string) {
		rec := l.get(ck, "/hx/books/"+l.book.ID+"/read/search?q="+url.QueryEscape(q), true)
		return rec.Code, rec.Body.String()
	}
	// A hit: chapter title, the words, and a link that opens at the sentence with the hit's length.
	code, body := hx(l.owner, "CAFE")
	if code != http.StatusOK || !strings.Contains(body, "Cafés") || !strings.Contains(body, "<mark>café</mark>") || !strings.Contains(body, "&amp;hit=4") {
		t.Errorf("hit: %d %s", code, body)
	}
	if m := regexp.MustCompile(`href="(/books/[^"]+)"`).FindStringSubmatch(body); m == nil {
		t.Errorf("no link: %s", body)
	} else if rec := l.get(l.owner, strings.ReplaceAll(m[1], "&amp;", "&"), false); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "croissant") {
		t.Errorf("hit link opens: %d", rec.Code)
	}
	// The cap.
	_, body = hx(l.owner, "needle")
	if n := strings.Count(body, "<li>"); n != readerSearchHits || !strings.Contains(body, readerSearchMax) {
		t.Errorf("cap: %d hits, note %v", n, strings.Contains(body, readerSearchMax))
	}
	// Words that are not in the book, and a question that is too short.
	if _, body = hx(l.owner, "zzyzx"); !strings.Contains(body, "Nothing in this book matches") {
		t.Errorf("no hits: %s", body)
	}
	if _, body = hx(l.owner, "c"); !strings.Contains(body, readerSearchShort) {
		t.Errorf("short: %s", body)
	}
	// The question is escaped, in the fragment and on the full page.
	bad := `<script>alert(1)</script>`
	_, body = hx(l.owner, bad)
	full := l.get(l.owner, l.page("sheet=search&q="+url.QueryEscape(bad)), false).Body.String()
	for name, s := range map[string]string{"fragment": body, "page": full} {
		if strings.Contains(s, bad) || !strings.Contains(s, "&lt;script&gt;") {
			t.Errorf("%s does not escape the question", name)
		}
	}
	// The plain page answers without htmx and carries the form.
	if page := l.get(l.owner, l.page("sheet=search&q=croissant"), false).Body.String(); !strings.Contains(page, `name="q"`) || !strings.Contains(page, "<mark>croissant</mark>") {
		t.Errorf("plain search page lacks the form or the hit")
	}
	// One edition only: the named one answers, another id is a 404, and links keep the edition once it was named.
	named := l.get(l.owner, "/hx/books/"+l.book.ID+"/read/search?q=croissant&edition="+l.ed.ID, true)
	if named.Code != http.StatusOK || !strings.Contains(named.Body.String(), "edition="+l.ed.ID) {
		t.Errorf("named edition: %d %s", named.Code, named.Body.String())
	}
	if rec := l.get(l.owner, "/hx/books/"+l.book.ID+"/read/search?q=croissant&edition=ed_nope", true); rec.Code != http.StatusNotFound {
		t.Errorf("other edition: %d", rec.Code)
	}
	// Rights: no title or snippet reaches a person who may not read, or a visitor with no session.
	for name, ck := range map[string]*http.Cookie{"member without can_read": l.noRead, "guest without can_read": l.guestNoR} {
		if c, body := hx(ck, "croissant"); c != http.StatusNotFound || strings.Contains(body, "croissant") {
			t.Errorf("%s: %d", name, c)
		}
	}
	if rec := l.a.do(http.MethodGet, "/hx/books/"+l.book.ID+"/read/search?q=croissant", nil, nil); rec.Code == http.StatusOK {
		t.Errorf("anonymous visitor: %d", rec.Code)
	}
	if c, _ := hx(l.reader, "croissant"); c != http.StatusOK {
		t.Errorf("reader: %d", c)
	}
}

// rnMarks makes a highlight with a note and a text bookmark for the owner and returns the highlight's id.
func rnMarks(t *testing.T, l *rdrLab) string {
	t.Helper()
	s0, _ := l.sidIn(1)
	rec := l.hx(t, http.MethodPost, "/hx/books/"+l.book.ID+"/highlights", url.Values{"edition": {l.ed.ID}, "start_sid": {s0}, "start_off": {"0"}, "end_sid": {s0}, "end_off": {"6"},
		"colour": {"green"}, "note": {"A \"quoted\" thought"}, "client_id": {"rnclient0001"}}, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("highlight: %d %s", rec.Code, rec.Body.String())
	}
	rec = l.hx(t, http.MethodPost, "/books/"+l.book.ID+"/bookmarks", url.Values{"edition_id": {l.ed.ID}, "sid": {l.sids()[2]}, "note": {"come back"}}, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("bookmark: %d %s", rec.Code, rec.Body.String())
	}
	hls, err := l.a.srv.progress.Highlights(context.Background(), l.ownerID, l.book.ID)
	if err != nil || len(hls) != 1 {
		t.Fatalf("highlights: %v %v", hls, err)
	}
	return hls[0].ID
}

func TestMarksExport(t *testing.T) {
	l := rdrSeed(t)
	if _, err := l.a.st.Writer().Exec(`UPDATE books SET title = ? WHERE id = ?`, `The "Quoted" Book`, l.book.ID); err != nil {
		t.Fatal(err)
	}
	id := rnMarks(t, l)
	get := func(ck *http.Cookie, ext string) (int, string, http.Header) {
		rec := l.get(ck, "/books/"+l.book.ID+"/read/marks."+ext, false)
		return rec.Code, rec.Body.String(), rec.Header()
	}
	// Markdown: the title as it is, the chapter, the quote as a quote, the note, the colour and a date.
	code, md, head := get(l.owner, "md")
	if code != http.StatusOK || !strings.HasPrefix(md, "# The \"Quoted\" Book\n") {
		t.Fatalf("md: %d %q", code, md)
	}
	_, b1 := l.sidIn(1)
	for _, want := range []string{"## Chapter 1", "> " + rdrSlice(b1.Text, 0, 6), `A "quoted" thought`, "come back", "*Bookmark, ", "*Highlight, green, "} {
		if !strings.Contains(md, want) {
			t.Errorf("md lacks %q:\n%s", want, md)
		}
	}
	if !regexp.MustCompile(`\*Highlight, green, \d{4}-\d{2}-\d{2}\*`).MatchString(md) {
		t.Errorf("md has no created date:\n%s", md)
	}
	if got := head.Get("Content-Disposition"); got != `attachment; filename="the-quoted-book-highlights.md"` || !strings.HasPrefix(head.Get("Content-Type"), "text/markdown") {
		t.Errorf("md headers: %q %q", got, head.Get("Content-Type"))
	}
	// Plain text and JSON carry the same rows.
	_, txt, head := get(l.owner, "txt")
	if !strings.Contains(txt, `A "quoted" thought`) || strings.Contains(txt, "> ") || !strings.HasPrefix(head.Get("Content-Type"), "text/plain") {
		t.Errorf("txt: %q", txt)
	}
	_, js, head := get(l.owner, "json")
	var out struct {
		Book  string
		Marks []struct {
			Kind, ChapterTitle, Quote, Note, Colour, Created string
			Chapter                                          int
		}
	}
	if err := json.Unmarshal([]byte(js), &out); err != nil || out.Book != `The "Quoted" Book` || len(out.Marks) != 2 {
		t.Fatalf("json: %v %s", err, js)
	}
	if m := out.Marks[0]; m.Kind != "highlight" || m.Colour != "green" || m.Note != `A "quoted" thought` || m.Chapter != rdrCh || m.Quote != rdrSlice(b1.Text, 0, 6) || m.Created == "" {
		t.Errorf("json highlight: %+v", m)
	}
	if m := out.Marks[1]; m.Kind != "bookmark" || m.Note != "come back" {
		t.Errorf("json bookmark: %+v", m)
	}
	if head.Get("Content-Type") != "application/json" || !strings.HasPrefix(head.Get("Content-Disposition"), "attachment;") {
		t.Errorf("json headers: %v", head)
	}
	// Another person's export is empty; so is the admin's. Nothing of the owner's leaks.
	for name, ck := range map[string]*http.Cookie{"member": l.reader, "admin": l.admin, "guest": l.guest} {
		_, md, _ := get(ck, "md")
		_, js, _ := get(ck, "json")
		if strings.Contains(md, "quoted") || strings.Contains(md, "come back") || strings.Contains(js, "quoted") || !strings.Contains(js, `"marks": []`) {
			t.Errorf("%s sees the owner's marks:\n%s\n%s", name, md, js)
		}
	}
	// A person who may not read gets 404; a visitor with no session gets no file.
	if c, _, _ := get(l.noRead, "md"); c != http.StatusNotFound {
		t.Errorf("no can_read: %d", c)
	}
	if rec := l.a.do(http.MethodGet, "/books/"+l.book.ID+"/read/marks.md", nil, nil); rec.Code == http.StatusOK {
		t.Error("anonymous visitor got an export")
	}
	// The sheet offers both downloads.
	if sheet := l.get(l.owner, l.readPath("sheet=marks"), false).Body.String(); !strings.Contains(sheet, "Export highlights and notes: Markdown") || !strings.Contains(sheet, "/read/marks.txt") {
		t.Errorf("marks sheet lacks the export links")
	}
	// A deleted highlight is gone, and a deleted bookmark too.
	if rec := l.hx(t, http.MethodDelete, "/hx/highlights/"+id, nil, l.owner); rec.Code != http.StatusOK {
		t.Fatalf("delete highlight: %d", rec.Code)
	}
	bms, _ := l.a.srv.progress.TextBookmarks(context.Background(), l.ownerID, l.book.ID)
	if len(bms) != 1 {
		t.Fatalf("bookmarks: %v", bms)
	}
	if rec := l.hx(t, http.MethodPost, "/bookmarks/"+bms[0].ID+"/delete", nil, l.owner); rec.Code != http.StatusOK {
		t.Fatalf("delete bookmark: %d %s", rec.Code, rec.Body.String())
	}
	if _, md, _ = get(l.owner, "md"); strings.Contains(md, "quoted") || strings.Contains(md, "come back") {
		t.Errorf("deleted marks still exported:\n%s", md)
	}
}

// TestReaderNoteBookmarkRows: a bookmark in the text shows on the marks sheet with a delete button, never on the
// Bookmarks tab (audio moments only), and the JSON api carries its sentence id and edition.
func TestReaderNoteBookmarkRows(t *testing.T) {
	l := rdrSeed(t)
	rnMarks(t, l)
	sheet := l.get(l.owner, l.readPath("sheet=marks"), false).Body.String()
	if !strings.Contains(sheet, "Bookmark") || !strings.Contains(sheet, `hx-post="/bookmarks/bm_`) {
		t.Errorf("marks sheet lacks the bookmark row")
	}
	list, err := l.a.srv.progress.Bookmarks(context.Background(), l.ownerID, l.book.ID, false)
	if err != nil || len(list) != 0 {
		t.Errorf("the Bookmarks tab holds a text place: %v %v", list, err)
	}
	text, _ := l.a.srv.progress.TextBookmarks(context.Background(), l.ownerID, l.book.ID)
	j, _ := json.Marshal(apiBookmarkOf(text[0]))
	if !strings.Contains(string(j), `"sid":"`+text[0].SID+`"`) || !strings.Contains(string(j), `"editionId":"`+l.ed.ID+`"`) {
		t.Errorf("api bookmark: %s", j)
	}
	audio, _ := l.a.srv.progress.AddBookmark(context.Background(), l.ownerID, l.book.ID, 1000, "")
	aj, _ := json.Marshal(apiBookmarkOf(audio))
	if strings.Contains(string(aj), "sid") || strings.Contains(string(aj), "editionId") {
		t.Errorf("audio bookmark carries text fields: %s", aj)
	}
	// The page paints the colour of each highlight.
	page := l.get(l.owner, l.readPath("ch="+strconv.Itoa(rdrCh)), false).Body.String()
	if !strings.Contains(page, `class="ts-hl"`) || !strings.Contains(page, `data-colour="green"`) {
		t.Errorf("page does not paint the highlight colour")
	}
}

func TestReaderErrors(t *testing.T) {
	l := rdrSeed(t)
	docs, _ := filepath.Glob(filepath.Join(l.a.dir, "text", "*"))
	if len(docs) == 0 {
		t.Fatal("no document file")
	}
	good, err := os.ReadFile(docs[0])
	if err != nil {
		t.Fatal(err)
	}
	reload := func() { l.a.srv.readerDocs = readerDocCache{} }
	check := func(name string) {
		t.Helper()
		reload()
		rec := l.get(l.owner, l.readPath(""), false)
		body := rec.Body.String()
		if rec.Code != http.StatusInternalServerError || !strings.Contains(body, "Tendshelf cannot open this book right now.") ||
			!strings.Contains(body, `href="/books/`+l.book.ID+`"`) || strings.Contains(body, ".json.gz") || strings.Contains(body, l.a.dir) {
			t.Errorf("%s page: %d %s", name, rec.Code, body)
		}
		frag := l.get(l.owner, "/hx/books/"+l.book.ID+"/read/chapters/2", true)
		if frag.Code != http.StatusInternalServerError || !strings.Contains(frag.Body.String(), "Tendshelf cannot open this book right now.") || strings.Contains(frag.Body.String(), "<html") {
			t.Errorf("%s fragment: %d %s", name, frag.Code, frag.Body.String())
		}
		// People who may not read still get 404, not the sentence.
		if c := l.get(l.noRead, l.readPath(""), false).Code; c != http.StatusNotFound {
			t.Errorf("%s no can_read: %d", name, c)
		}
	}
	if err := os.WriteFile(docs[0], []byte("not a document"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("damaged")
	if err := os.Remove(docs[0]); err != nil {
		t.Fatal(err)
	}
	check("missing")
	// The file back: the reader opens again.
	if err := os.WriteFile(docs[0], good, 0o600); err != nil {
		t.Fatal(err)
	}
	reload()
	if rec := l.get(l.owner, l.readPath(""), false); rec.Code != http.StatusOK {
		t.Errorf("restored: %d", rec.Code)
	}
	// The part that fails to load is worded in data-copy for the client.
	page := l.get(l.owner, l.readPath(""), false).Body.String()
	if !strings.Contains(page, "This part did not load.") || !strings.Contains(page, "Try again") {
		t.Errorf("the page lacks the part-failed words")
	}
}
