package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// rdrLab is the Text tab lab with one ready ebook paired with the book: a title chapter (document chapter 1) and
// the chapters "Chapter 1" to "Chapter 3" (2 to 4).
type rdrLab struct {
	*textLab
	file ebook.File
	ed   ebook.Edition
	doc  *ebook.Doc
}

// rdrCh is the document chapter the highlight and bookmark tests work in.
const rdrCh = 2

func rdrSeed(t *testing.T) *rdrLab {
	t.Helper()
	l := &rdrLab{textLab: newTextLab(t)}
	l.run(t)
	l.file = l.addReady(t, "reader.epub", "Textword Lantern", 3)
	if err := l.a.runner.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	ed, ok, err := l.svc.Store().CurrentEdition(context.Background(), l.file.ID)
	if err != nil || !ok {
		t.Fatalf("edition: %v %v", ok, err)
	}
	l.ed = ed
	doc, err := ebook.ReadDoc(l.a.dir+"/text", ed.DocSHA256)
	if err != nil {
		t.Fatal(err)
	}
	l.doc = doc
	return l
}

func (l *rdrLab) readPath(q string) string {
	p := "/books/" + l.book.ID + "/read"
	if q != "" {
		p += "?" + q
	}
	return p
}

func (l *rdrLab) hx(t *testing.T, method, path string, form url.Values, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("_csrf", l.a.tokenFor(t, ck))
	return l.a.do(method, path, form, func(r *http.Request) { r.Header.Set("HX-Request", "true") }, ck)
}

// sidIn returns the first sentence id of block number p of chapter rdrCh and its block.
func (l *rdrLab) sidIn(p int) (string, ebook.Block) {
	b := l.doc.Chapters[rdrCh-1].Blocks[p]
	return ebook.SentenceID(b.ID, 0), b
}

// sids lists every sentence id of chapter rdrCh in order.
func (l *rdrLab) sids() []string {
	var out []string
	for _, b := range l.doc.Chapters[rdrCh-1].Blocks {
		for k := range b.Sentences {
			out = append(out, ebook.SentenceID(b.ID, k))
		}
	}
	return out
}

func TestReaderPage(t *testing.T) {
	l := rdrSeed(t)
	rec := l.get(l.owner, l.readPath(""), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("default: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`id="reader"`, `id="reader-ch-2"`, `class="ts-reader__next"`, `hx-trigger="ts-reader-next"`,
		`id="reader-dock"`, `id="reader-marks"`, `role="toolbar"`, `id="reader-hl-form"`, `aria-label="Close reader"`, `data-sid=`} {
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	for _, gone := range []string{`data-mode`, `data-timing`, `data-s="`, `ts-s--gap`, `ts-voicepill`, `ts-focus`, `data-ts-reader-eyes-free`, `data-ts-play`,
		`ts-mode`, `Read along`, `data-chapters`, `data-follow`, `data-view`, `data-word`, `readalong.js`, `Listen from here`} {
		if strings.Contains(body, gone) {
			t.Errorf("page still has %q", gone)
		}
	}
	if strings.Contains(body, `id="reader-ch-3"`) {
		t.Error("only the first chapter should be loaded")
	}
	// ch and at open the right chapter.
	if got := l.get(l.owner, l.readPath("at=c3.p1.s0"), false).Body.String(); !strings.Contains(got, `id="reader-ch-3"`) {
		t.Error("at= should open chapter 3")
	}
	if got := l.get(l.owner, l.readPath("ch=3&sheet=contents"), false).Body.String(); !strings.Contains(got, "Contents") {
		t.Error("contents sheet")
	}
	if got := l.get(l.owner, l.readPath("sheet=marks"), false).Body.String(); !strings.Contains(got, "Highlights and notes") ||
		!strings.Contains(got, "Your highlights, notes and bookmarks in this book.") || strings.Contains(got, "same moment") {
		t.Error("marks sheet")
	}
	if got := l.get(l.owner, l.readPath("sheet=reading"), false).Body.String(); !strings.Contains(got, "Reading settings") {
		t.Error("reading settings sheet")
	}
	for _, q := range []string{"ch=0", "ch=9", "ch=x", "at=nope", "at=c9.p1.s0", "at=c2.p1.s99", "sheet=nothing", "edition=ed_missing"} {
		if rec := l.get(l.owner, l.readPath(q), false); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

// TestXReaderOldModeLink: a link made while the reader had modes still opens the reader; mode= is ignored.
func TestXReaderOldModeLink(t *testing.T) {
	l := rdrSeed(t)
	for _, q := range []string{"mode=readalong", "mode=read", "mode=listen", "mode=readalong&ch=3"} {
		rec := l.get(l.owner, l.readPath(q), false)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="reader"`) || strings.Contains(rec.Body.String(), "read?mode=") || strings.Contains(rec.Body.String(), "&amp;mode=") {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	if rec := l.get(l.owner, "/hx/books/"+l.book.ID+"/read/chapters/3?edition="+l.ed.ID+"&mode=readalong", true); rec.Code != http.StatusOK {
		t.Errorf("fragment with mode=: %d", rec.Code)
	}
}

// TestXReaderTimingGone: the timing route is not there any more.
func TestXReaderTimingGone(t *testing.T) {
	l := rdrSeed(t)
	if rec := l.get(l.owner, "/books/"+l.book.ID+"/read/timing/2?edition="+l.ed.ID, false); rec.Code != http.StatusNotFound {
		t.Errorf("timing: %d", rec.Code)
	}
}

func TestReaderPageRoles(t *testing.T) {
	l := rdrSeed(t)
	for name, ck := range map[string]*http.Cookie{"owner": l.owner, "admin": l.admin, "member with can_read": l.reader, "guest with can_read": l.guest} {
		if rec := l.get(ck, l.readPath(""), false); rec.Code != http.StatusOK {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	for name, ck := range map[string]*http.Cookie{"member without can_read": l.noRead, "guest without can_read": l.guestNoR} {
		for _, p := range []string{l.readPath(""), "/hx/books/" + l.book.ID + "/read/chapters/2"} {
			if rec := l.get(ck, p, false); rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: %d", name, p, rec.Code)
			}
		}
		sid, b := l.sidIn(0)
		rec := l.hx(t, http.MethodPost, "/hx/books/"+l.book.ID+"/highlights", url.Values{"edition": {l.ed.ID}, "start_sid": {sid}, "start_off": {"0"}, "end_sid": {sid}, "end_off": {"3"}}, ck)
		if rec.Code != http.StatusNotFound || len(b.Text) == 0 {
			t.Errorf("%s highlight: %d", name, rec.Code)
		}
	}
	// A visitor with no session (a share link visitor) never reaches the reader.
	for _, p := range []string{l.readPath(""), "/hx/books/" + l.book.ID + "/read/chapters/2"} {
		rec := l.a.do(http.MethodGet, p, nil, nil)
		if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "Chapter 1") {
			t.Errorf("anonymous %s: %d", p, rec.Code)
		}
	}
}

func TestReaderChapterFragment(t *testing.T) {
	l := rdrSeed(t)
	p := "/hx/books/" + l.book.ID + "/read/chapters/"
	body := l.get(l.owner, p+"2?edition="+l.ed.ID, true).Body.String()
	if strings.Contains(body, "<html") || strings.Contains(body, `id="reader-dock"`) || !strings.Contains(body, `id="reader-ch-2"`) {
		t.Error("fragment should be the chapter only")
	}
	if !strings.Contains(body, `hx-get="/hx/books/`+l.book.ID+`/read/chapters/3?edition=`+l.ed.ID+`"`) || !strings.Contains(body, `href="/books/`+l.book.ID+`/read?ch=3`) {
		t.Errorf("sentinel missing its address and plain link:\n%s", body[max(0, len(body)-600):])
	}
	last := l.get(l.owner, p+"4?edition="+l.ed.ID, true).Body.String()
	if strings.Contains(last, "ts-reader__next") {
		t.Error("the last chapter has no sentinel")
	}
	for _, bad := range []string{"0", "9", "x"} {
		if rec := l.get(l.owner, p+bad, true); rec.Code != http.StatusNotFound {
			t.Errorf("idx %s: %d", bad, rec.Code)
		}
	}
}

func rdrBlock(text string, sents [][2]int, runs ...ebook.Run) ebook.Block {
	return ebook.Block{ID: "c1.p1", Kind: "p", Text: text, Sentences: sents, Runs: runs}
}

// rdrTextOf joins the text nodes of every sentence span in html, per sentence.
func rdrSentenceTexts(htmlText string) []string {
	var out []string
	parts := strings.Split(htmlText, `<span class="ts-s`)
	for _, p := range parts[1:] {
		i := strings.Index(p, `>`)
		rest := p[i+1:]
		var sb strings.Builder
		depth := 1
		for depth > 0 && rest != "" {
			lt := strings.IndexByte(rest, '<')
			if lt < 0 {
				break
			}
			sb.WriteString(rest[:lt])
			gt := strings.IndexByte(rest[lt:], '>')
			tag := rest[lt+1 : lt+gt]
			switch {
			case strings.HasPrefix(tag, "/"):
				depth--
			case !strings.HasSuffix(tag, "/") && !strings.HasPrefix(tag, "br"):
				depth++
			}
			rest = rest[lt+gt+1:]
		}
		out = append(out, strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&#39;", "'").Replace(sb.String()))
	}
	return out
}

func TestReaderRenderSentenceText(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		sents [][2]int
		runs  []ebook.Run
		hls   []progress.Highlight
	}{
		{"plain", "One. Two.", [][2]int{{0, 4}, {5, 9}}, nil, nil},
		{"emoji", "Hi \U0001F600 there. Ok.", [][2]int{{0, 12}, {13, 16}}, nil, nil},
		{"accents", "Él dijo ñandú. Fin.", [][2]int{{0, 14}, {15, 19}}, nil, nil},
		{"escapes", `A <b> & "q". Z.`, [][2]int{{0, 11}, {12, 14}}, nil, nil},
		{"em across edge", "Run on. And on.", [][2]int{{0, 7}, {8, 15}}, []ebook.Run{{Start: 4, End: 11, Style: "em"}}, nil},
		{"highlight across two", "First one. Second one.", [][2]int{{0, 10}, {11, 22}}, nil, []progress.Highlight{{ID: "hl_1", StartSID: "c1.p1.s0", EndSID: "c1.p1.s1", StartOff: 4, EndOff: 16}}},
		{"highlight with note", "First one. Second.", [][2]int{{0, 10}, {11, 18}}, nil, []progress.Highlight{{ID: "hl_2", Note: "n", StartSID: "c1.p1.s0", EndSID: "c1.p1.s0", StartOff: 0, EndOff: 5}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := rdrBlock(c.text, c.sents, c.runs...)
			html := rdrInner(b, rdrBlockOpts{Hls: c.hls})
			got := rdrSentenceTexts(html)
			if len(got) != len(c.sents) {
				t.Fatalf("sentences: %d in %s", len(got), html)
			}
			for i, s := range c.sents {
				want := rdrSlice(c.text, s[0], s[1])
				if got[i] != want {
					t.Errorf("sentence %d = %q, want %q (%s)", i, got[i], want, html)
				}
			}
			for _, bad := range []string{"<b>", `style=`} {
				if strings.Contains(html, bad) {
					t.Errorf("unsafe %q in %s", bad, html)
				}
			}
		})
	}
	html := rdrInner(rdrBlock("First one. Second.", [][2]int{{0, 10}, {11, 18}}), rdrBlockOpts{Hls: []progress.Highlight{{ID: "hl_2", Note: "n", StartSID: "c1.p1.s0", EndSID: "c1.p1.s0", StartOff: 0, EndOff: 5}}})
	if !strings.Contains(html, `data-hl-note="hl_2"`) || strings.Index(html, `data-hl-note`) < strings.Index(html, `</span></span><button`) {
		t.Errorf("note dot after the sentence span: %s", html)
	}
}

func rdrSlice(s string, a, b int) string {
	return string(utf16.Decode(utf16.Encode([]rune(s))[a:b]))
}

// TestXReaderRenderPlain: a sentence span carries its id and offset and nothing about audio.
func TestXReaderRenderPlain(t *testing.T) {
	b := rdrBlock("One. Two. Three.", [][2]int{{0, 4}, {5, 9}, {10, 16}})
	html := rdrInner(b, rdrBlockOpts{})
	if !strings.Contains(html, `<span class="ts-s" id="s-c1.p1.s0" data-sid="c1.p1.s0" data-o="0">`) ||
		!strings.Contains(html, `<span class="ts-s" id="s-c1.p1.s2" data-sid="c1.p1.s2" data-o="10">`) {
		t.Errorf("sentence spans: %s", html)
	}
	if strings.Contains(html, "data-s=") || strings.Contains(html, "--gap") {
		t.Errorf("audio markers left: %s", html)
	}
}

func TestReaderHighlightCreate(t *testing.T) {
	l := rdrSeed(t)
	post := "/hx/books/" + l.book.ID + "/highlights"
	s0, b0 := l.sidIn(1)
	form := url.Values{"edition": {l.ed.ID}, "start_sid": {s0}, "start_off": {"4"}, "end_sid": {s0}, "end_off": {"12"}, "client_id": {"clienthl0001"}}
	rec := l.hx(t, http.MethodPost, post, form, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="b-`+b0.ID+`"`) || !strings.Contains(body, `hx-swap-oob="outerHTML"`) || !strings.Contains(body, `id="reader-marks-count"`) || !strings.Contains(body, "Highlighted.") {
		t.Errorf("OOB parts: %s", body)
	}
	hls, err := l.a.srv.progress.Highlights(context.Background(), l.ownerID, l.book.ID)
	if err != nil || len(hls) != 1 {
		t.Fatalf("highlights: %v %v", hls, err)
	}
	h := hls[0]
	if h.Quote != rdrSlice(b0.Text, 4, 12) || h.Chapter != rdrCh || h.EditionID != l.ed.ID {
		t.Errorf("highlight: %+v", h)
	}
	if h.Prefix != rdrSlice(b0.Text, 0, 4) {
		t.Errorf("prefix %q", h.Prefix)
	}
	// A retry with the same client id gives the same one.
	l.hx(t, http.MethodPost, post, form, l.owner)
	if hls, _ = l.a.srv.progress.Highlights(context.Background(), l.ownerID, l.book.ID); len(hls) != 1 {
		t.Errorf("retry made %d", len(hls))
	}
	// A second one, on another sentence.
	second := l.sids()[2]
	rec = l.hx(t, http.MethodPost, post, url.Values{"edition": {l.ed.ID}, "start_sid": {second}, "start_off": {"0"}, "end_sid": {second}, "end_off": {"2"}}, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("second: %d", rec.Code)
	}
	if hls, _ = l.a.srv.progress.Highlights(context.Background(), l.ownerID, l.book.ID); len(hls) != 2 {
		t.Errorf("highlights: %+v", hls)
	}
	// Across chapters: 400 with the plain sentence.
	rec = l.hx(t, http.MethodPost, post, url.Values{"edition": {l.ed.ID}, "start_sid": {s0}, "start_off": {"0"}, "end_sid": {"c3.p1.s0"}, "end_off": {"2"}}, l.owner)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Highlight inside one chapter.") {
		t.Errorf("cross-chapter: %d %s", rec.Code, rec.Body.String())
	}
	// Not htmx: 400.
	plain := url.Values{"_csrf": {l.a.tokenFor(t, l.owner)}, "edition": {l.ed.ID}}
	if rec := l.a.do(http.MethodPost, post, plain, nil, l.owner); rec.Code != http.StatusBadRequest {
		t.Errorf("plain post: %d", rec.Code)
	}
	// The limit.
	old := progress.MaxHighlightsPerBook
	progress.MaxHighlightsPerBook = 2
	defer func() { progress.MaxHighlightsPerBook = old }()
	rec = l.hx(t, http.MethodPost, post, url.Values{"edition": {l.ed.ID}, "start_sid": {s0}, "start_off": {"0"}, "end_sid": {s0}, "end_off": {"2"}}, l.owner)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "2,000 highlights") {
		t.Errorf("limit: %d %s", rec.Code, rec.Body.String())
	}
}

func TestReaderHighlightPrivate(t *testing.T) {
	l := rdrSeed(t)
	s0, _ := l.sidIn(0)
	post := "/hx/books/" + l.book.ID + "/highlights"
	rec := l.hx(t, http.MethodPost, post, url.Values{"edition": {l.ed.ID}, "start_sid": {s0}, "start_off": {"0"}, "end_sid": {s0}, "end_off": {"5"}, "note": {"secret thought"}}, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d", rec.Code)
	}
	hls, _ := l.a.srv.progress.Highlights(context.Background(), l.ownerID, l.book.ID)
	id := hls[0].ID
	if !strings.Contains(l.get(l.owner, l.readPath("sheet=marks"), false).Body.String(), "secret thought") {
		t.Error("owner's marks sheet misses the note")
	}
	for name, ck := range map[string]*http.Cookie{"member": l.reader, "admin": l.admin, "guest": l.guest} {
		if rec := l.hx(t, http.MethodPatch, "/hx/highlights/"+id, url.Values{"note": {"x"}}, ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s patch: %d", name, rec.Code)
		}
		if rec := l.hx(t, http.MethodDelete, "/hx/highlights/"+id, nil, ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s delete: %d", name, rec.Code)
		}
		page := l.get(ck, l.readPath(""), false).Body.String() + l.get(ck, l.readPath("sheet=marks"), false).Body.String()
		if strings.Contains(page, "secret thought") || strings.Contains(page, id) {
			t.Errorf("%s sees the owner's highlight", name)
		}
	}
	for name, ck := range map[string]*http.Cookie{"noRead": l.noRead, "guestNoR": l.guestNoR} {
		if rec := l.hx(t, http.MethodPatch, "/hx/highlights/"+id, url.Values{"note": {"x"}}, ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s patch: %d", name, rec.Code)
		}
	}
	// The owner can change and delete it.
	if rec := l.hx(t, http.MethodPatch, "/hx/highlights/"+id, url.Values{"note": {"new words"}}, l.owner); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "new words") {
		t.Errorf("owner patch: %d", rec.Code)
	}
	rec = l.hx(t, http.MethodDelete, "/hx/highlights/"+id, nil, l.owner)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Highlight removed.") || !strings.Contains(rec.Body.String(), `hx-swap-oob="delete"`) {
		t.Errorf("owner delete: %d %s", rec.Code, rec.Body.String())
	}
}

func TestReaderBookmarkFromText(t *testing.T) {
	l := rdrSeed(t)
	post := "/books/" + l.book.ID + "/bookmarks"
	s0, _ := l.sidIn(0)
	rec := l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {s0}}, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("text bookmark: %d %s", rec.Code, rec.Body.String())
	}
	second := l.sids()[2]
	rec = l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {second}}, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("second bookmark: %d", rec.Code)
	}
	list, err := l.a.srv.progress.TextBookmarks(context.Background(), l.ownerID, l.book.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("bookmarks: %v %v", list, err)
	}
	got := map[string]int64{}
	for _, b := range list {
		if b.EditionID != l.ed.ID {
			t.Errorf("edition %q", b.EditionID)
		}
		got[b.SID] = b.PositionMs
	}
	if len(got) != 2 || got[s0] != 0 || got[second] != 0 {
		t.Errorf("a text bookmark stores no audio moment: %v", got)
	}
	if !strings.Contains(l.get(l.owner, l.readPath(""), false).Body.String(), `class="ts-reader__bm"`) {
		t.Error("the page shows no bookmark flag")
	}
	// Half a pair, an unknown sentence and a person who cannot read.
	if rec := l.hx(t, http.MethodPost, post, url.Values{"sid": {s0}}, l.owner); rec.Code != http.StatusBadRequest {
		t.Errorf("sid without edition: %d", rec.Code)
	}
	if rec := l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}}, l.owner); rec.Code != http.StatusBadRequest {
		t.Errorf("edition without sid: %d", rec.Code)
	}
	if rec := l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {"c2.p1.s99"}}, l.owner); rec.Code != http.StatusNotFound {
		t.Errorf("unknown sid: %d", rec.Code)
	}
	for name, ck := range map[string]*http.Cookie{"noRead": l.noRead, "guestNoR": l.guestNoR} {
		if rec := l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {s0}}, ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

func TestReaderTextTabLink(t *testing.T) {
	l := rdrSeed(t)
	href := `href="/books/` + l.book.ID + `/read?edition=` + l.ed.ID + `"`
	body := l.get(l.owner, l.path(""), false).Body.String()
	if !strings.Contains(body, href) || !strings.Contains(body, ">Read<") || strings.Contains(body, "Read along") {
		t.Error("owner's tab: Read link")
	}
	if !strings.Contains(l.get(l.reader, l.path(""), false).Body.String(), ">Read<") {
		t.Error("reader's tab: Read link")
	}
	if strings.Contains(l.get(l.noRead, l.path(""), false).Body.String(), "/read?edition=") {
		t.Error("a person who cannot read sees the link")
	}
}
