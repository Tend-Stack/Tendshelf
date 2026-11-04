package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Tend-Stack/tendshelf/internal/align"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// rdrLab is the Text tab lab with a ready two-chapter-plus-title ebook and a finished alignment: the chapter
// "Chapter 1" (document chapter 2) is aligned with one gap sentence, "Chapter 2" (3) is unmatched.
type rdrLab struct {
	*talignLab
	doc    *ebook.Doc
	key    string
	timing *align.Timing
	alID   string
}

const rdrAlignedCh = 2

func rdrSeed(t *testing.T) *rdrLab {
	t.Helper()
	l := &rdrLab{talignLab: newTalignLab(t, true)}
	doc, err := ebook.ReadDoc(l.a.dir+"/text", l.ed.DocSHA256)
	if err != nil {
		t.Fatal(err)
	}
	l.doc = doc
	tm := &align.Timing{V: align.TimingV, Chapter: rdrAlignedCh}
	n := 0
	for _, b := range doc.Chapters[rdrAlignedCh-1].Blocks {
		for k := range b.Sentences {
			gap := n == 2
			tm.SID = append(tm.SID, ebook.SentenceID(b.ID, k))
			if gap {
				reason := "no_match"
				tm.SS, tm.SE, tm.SC, tm.Gap = append(tm.SS, -1), append(tm.SE, -1), append(tm.SC, -1), append(tm.Gap, &reason)
			} else {
				tm.SS, tm.SE, tm.SC, tm.Gap = append(tm.SS, int64(n)*1000), append(tm.SE, int64(n)*1000+900), append(tm.SC, 0.9), append(tm.Gap, nil)
			}
			n++
		}
	}
	for si, sid := range tm.SID {
		bid := rdrBlockOf(sid)
		var blk ebook.Block
		for _, b := range doc.Chapters[rdrAlignedCh-1].Blocks {
			if b.ID == bid {
				blk = b
			}
		}
		var k int
		_, _, k, _ = ebook.ParseSID(sid)
		for wi, w := range blk.Words {
			if w[0] < blk.Sentences[k][0] || w[0] >= blk.Sentences[k][1] {
				continue
			}
			tm.WS, tm.WO0, tm.WO1 = append(tm.WS, si), append(tm.WO0, w[0]), append(tm.WO1, w[1])
			if tm.Gap[si] != nil {
				tm.W0, tm.W1, tm.WM = append(tm.W0, -1), append(tm.W1, -1), append(tm.WM, align.WordNotTimed)
			} else {
				tm.W0, tm.W1, tm.WM = append(tm.W0, tm.SS[si]+int64(wi)*10), append(tm.W1, tm.SS[si]+int64(wi)*10+9), append(tm.WM, align.WordTimed)
			}
		}
	}
	l.timing, l.key = tm, strings.Repeat("ab", 32)
	if err := align.WriteTiming(l.a.dir+"/align", l.key, tm); err != nil {
		t.Fatal(err)
	}
	l.alID = "al_rdr"
	st := l.svc2.Store()
	if err := st.Insert(context.Background(), align.Alignment{ID: l.alID, EditionID: l.ed.ID, BookID: l.book.ID, AudioKey: strings.Repeat("cd", 32),
		DocSHA256: l.ed.DocSHA256, State: align.StateDone, FinishedAt: 5, SentencesTotal: n, SentencesTimed: n - 1}); err != nil {
		t.Fatal(err)
	}
	w := l.a.st.Writer()
	if _, err := w.Exec(`INSERT INTO alignment_chapters(alignment_id, chapter_idx, state, chapter_key, start_ms, end_ms, sentences_total, sentences_timed, updated_at)
VALUES (?, 2, 'aligned', ?, 0, 5000, ?, ?, 1)`, l.alID, l.key, n, n-1); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`INSERT INTO alignment_chapters(alignment_id, chapter_idx, state, sentences_total, sentences_timed, updated_at) VALUES (?, 3, 'unmatched', 3, 0, 1)`, l.alID); err != nil {
		t.Fatal(err)
	}
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

// sidIn returns the first sentence id of block number p of the aligned chapter and its block.
func (l *rdrLab) sidIn(p int) (string, ebook.Block) {
	b := l.doc.Chapters[rdrAlignedCh-1].Blocks[p]
	return ebook.SentenceID(b.ID, 0), b
}

func TestReaderPageModes(t *testing.T) {
	l := rdrSeed(t)
	rec := l.get(l.owner, l.readPath(""), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("default: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`id="reader"`, `data-mode="readalong"`, `data-hl="wash"`, `data-word="on"`, `data-view="page"`, `data-follow="on"`,
		`data-follow-style="steady"`, `data-return="off"`, `data-chapters="-1,-1;0,5000;-1,-1;-1,-1"`, `id="reader-ch-2"`, `data-timing="/books/` + l.book.ID + `/read/timing/2?edition=`,
		`ts-s--gap`, `class="ts-reader__next"`, `hx-trigger="revealed, ts-reader-next"`, `id="reader-dock"`, `id="reader-marks"`, `role="toolbar"`,
		`id="reader-hl-form"`, `data-ts-reader-eyes-free`, `aria-current="page"`, `Close reader`, `Read along works up to chapter`} {
		if want == `Read along works up to chapter` {
			if strings.Contains(body, want) {
				t.Error("banner shown for a finished alignment")
			}
			continue
		}
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if !strings.Contains(body, `data-s="0"`) || strings.Contains(body, `id="reader-ch-3"`) {
		t.Error("first chapter should be the aligned one with data-s, only one chapter loaded")
	}
	// Read mode: the thin footer, one continue button, no focus line.
	read := l.get(l.owner, l.readPath("mode=read"), false).Body.String()
	if strings.Contains(read, "data-ts-focus") || !strings.Contains(read, `data-mode="read"`) {
		t.Error("read mode page")
	}
	// ch and at open the right chapter.
	sid := fmt.Sprintf("c3.p1.s0")
	if got := l.get(l.owner, l.readPath("at="+sid), false).Body.String(); !strings.Contains(got, `id="reader-ch-3"`) || strings.Contains(got, `data-timing=`) {
		t.Error("at= should open the unaligned chapter 3")
	}
	if got := l.get(l.owner, l.readPath("ch=3&sheet=contents"), false).Body.String(); !strings.Contains(got, "Contents") || !strings.Contains(got, "text only, not narrated") {
		t.Error("contents sheet")
	}
	if got := l.get(l.owner, l.readPath("sheet=marks"), false).Body.String(); !strings.Contains(got, "Highlights and notes") || !strings.Contains(got, "Each one opens at the same moment") {
		t.Error("marks sheet")
	}
	for _, q := range []string{"mode=listen", "ch=0", "ch=9", "ch=x", "at=nope", "at=c9.p1.s0", "at=c2.p1.s99", "sheet=reading", "edition=ed_missing"} {
		if rec := l.get(l.owner, l.readPath(q), false); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	// Without an alignment the default is read and readalong falls back.
	if _, err := l.a.st.Writer().Exec(`DELETE FROM alignments`); err != nil {
		t.Fatal(err)
	}
	if got := l.get(l.owner, l.readPath(""), false).Body.String(); !strings.Contains(got, `data-mode="read"`) || strings.Contains(got, `data-timing=`) {
		t.Error("no alignment: read mode, no timing")
	}
	if got := l.get(l.owner, l.readPath("mode=readalong"), false).Body.String(); !strings.Contains(got, `data-mode="read"`) {
		t.Error("readalong without alignment falls back to read")
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
		for _, p := range []string{l.readPath(""), "/hx/books/" + l.book.ID + "/read/chapters/2", "/books/" + l.book.ID + "/read/timing/2?edition=" + l.ed.ID} {
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
	for _, p := range []string{l.readPath(""), "/hx/books/" + l.book.ID + "/read/chapters/2", "/books/" + l.book.ID + "/read/timing/2"} {
		rec := l.a.do(http.MethodGet, p, nil, nil)
		if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "Chapter 1") {
			t.Errorf("anonymous %s: %d", p, rec.Code)
		}
	}
}

func TestReaderChapterFragment(t *testing.T) {
	l := rdrSeed(t)
	p := "/hx/books/" + l.book.ID + "/read/chapters/"
	body := l.get(l.owner, p+"2?edition="+l.ed.ID+"&mode=readalong", true).Body.String()
	if strings.Contains(body, "<html") || strings.Contains(body, `id="reader-dock"`) || !strings.Contains(body, `id="reader-ch-2"`) {
		t.Error("fragment should be the chapter only")
	}
	if !strings.Contains(body, `hx-get="/hx/books/`+l.book.ID+`/read/chapters/3?edition=`+l.ed.ID+`&amp;mode=readalong"`) || !strings.Contains(body, `href="/books/`+l.book.ID+`/read?ch=3`) {
		t.Errorf("sentinel missing its address and plain link:\n%s", body[max(0, len(body)-600):])
	}
	last := l.get(l.owner, p+"4?edition="+l.ed.ID, true).Body.String()
	if strings.Contains(last, "data-s=") || strings.Contains(last, "data-timing") || strings.Contains(last, "ts-reader__next") {
		t.Error("the unaligned last chapter has no data-s, timing or sentinel")
	}
	for _, bad := range []string{"0", "9", "x"} {
		if rec := l.get(l.owner, p+bad, true); rec.Code != http.StatusNotFound {
			t.Errorf("idx %s: %d", bad, rec.Code)
		}
	}
}

func TestReaderTimingGzip(t *testing.T) {
	l := rdrSeed(t)
	p := "/books/" + l.book.ID + "/read/timing/2?edition=" + l.ed.ID
	want, err := os.ReadFile(align.TimingPath(l.a.dir+"/align", l.key))
	if err != nil {
		t.Fatal(err)
	}
	gz := func(r *http.Request) { r.Header.Set("Accept-Encoding", "gzip") }
	rec := l.a.do(http.MethodGet, p, nil, gz, l.owner)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("gzip: %d, bytes equal %v", rec.Code, bytes.Equal(rec.Body.Bytes(), want))
	}
	h := rec.Header()
	if h.Get("Content-Encoding") != "gzip" || h.Get("Content-Type") != "application/json" || h.Get("ETag") != `"`+l.key+`"` ||
		h.Get("Cache-Control") != "private, no-cache" || h.Get("Vary") != "Accept-Encoding" {
		t.Errorf("headers: %v", h)
	}
	if rec := l.a.do(http.MethodGet, p, nil, func(r *http.Request) { gz(r); r.Header.Set("If-None-Match", `"`+l.key+`"`) }, l.owner); rec.Code != http.StatusNotModified {
		t.Errorf("etag: %d", rec.Code)
	}
	plain := l.a.do(http.MethodGet, p, nil, nil, l.owner)
	zr, err := gzip.NewReader(bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	if plain.Code != http.StatusOK || plain.Header().Get("Content-Encoding") != "" || !bytes.Equal(plain.Body.Bytes(), raw) {
		t.Errorf("plain fallback: %d", plain.Code)
	}
	if rec := l.a.do(http.MethodGet, "/books/"+l.book.ID+"/read/timing/3?edition="+l.ed.ID, nil, gz, l.owner); rec.Code != http.StatusNotFound {
		t.Errorf("unaligned: %d", rec.Code)
	}
	// An edition of another book is "not found".
	if rec := l.a.do(http.MethodGet, "/books/"+l.book.ID+"/read/timing/2?edition=ed_other", nil, gz, l.owner); rec.Code != http.StatusNotFound {
		t.Errorf("other edition: %d", rec.Code)
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
			html := rdrInner(b, rdrBlockOpts{Hls: c.hls, Aligned: false})
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

func TestReaderRenderGap(t *testing.T) {
	b := rdrBlock("One. Two. Three.", [][2]int{{0, 4}, {5, 9}, {10, 16}})
	tm := &align.Timing{SID: []string{"c1.p1.s0", "c1.p1.s1"}, SS: []int64{0, -1}, SE: []int64{500, -1}, SC: []float64{1, -1}, Gap: []*string{nil, new(string)}}
	html := rdrInner(b, rdrBlockOpts{Timing: rdrTimingOf(tm), Aligned: true})
	if !strings.Contains(html, `id="s-c1.p1.s0" data-sid="c1.p1.s0" data-o="0" data-s="0"`) {
		t.Errorf("timed sentence: %s", html)
	}
	for _, sid := range []string{"c1.p1.s1", "c1.p1.s2"} {
		i := strings.Index(html, `id="s-`+sid+`"`)
		if i < 0 || !strings.Contains(html[max(0, i-30):i], `ts-s ts-s--gap`) || strings.Contains(html[i:min(len(html), i+80)], "data-s=") {
			t.Errorf("%s should be a gap without data-s: %s", sid, html)
		}
	}
	if un := rdrInner(b, rdrBlockOpts{}); strings.Contains(un, "data-s=") || strings.Contains(un, "--gap") {
		t.Errorf("unaligned chapter: %s", un)
	}
}

func TestReaderHighlightCreate(t *testing.T) {
	l := rdrSeed(t)
	post := "/hx/books/" + l.book.ID + "/highlights"
	s0, b0 := l.sidIn(1)
	// Block 0's first sentence is timed (index 0); a highlight over its first characters takes word times.
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
	if h.Quote != rdrSlice(b0.Text, 4, 12) || h.Chapter != rdrAlignedCh || h.EditionID != l.ed.ID || h.AlignmentID != l.alID || h.StartMs == nil || *h.StartMs < 0 || *h.EndMs < *h.StartMs {
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
	// The gap sentence (timing index 1) gives no times.
	var gapSID string
	gapSID = l.timing.SID[2]
	rec = l.hx(t, http.MethodPost, post, url.Values{"edition": {l.ed.ID}, "start_sid": {gapSID}, "start_off": {"0"}, "end_sid": {gapSID}, "end_off": {"2"}}, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("gap: %d", rec.Code)
	}
	hls, _ = l.a.srv.progress.Highlights(context.Background(), l.ownerID, l.book.ID)
	if len(hls) != 2 || hls[1].StartMs != nil || hls[1].EndMs != nil {
		t.Errorf("gap highlight times: %+v", hls)
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
	// The gap sentence takes the previous timed sentence's end.
	rec = l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {l.timing.SID[2]}}, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("gap bookmark: %d", rec.Code)
	}
	list, err := l.a.srv.progress.Bookmarks(context.Background(), l.ownerID, l.book.ID, false)
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
	if got[s0] != l.timing.SS[0] || got[l.timing.SID[2]] != l.timing.SE[1] {
		t.Errorf("positions %v", got)
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
	if !strings.Contains(body, href) || !strings.Contains(body, ">Read along<") {
		t.Error("owner's tab: Read along link")
	}
	if !strings.Contains(l.get(l.reader, l.path(""), false).Body.String(), ">Read along<") {
		t.Error("reader's tab: Read along link")
	}
	if _, err := l.a.st.Writer().Exec(`DELETE FROM alignments`); err != nil {
		t.Fatal(err)
	}
	body = l.get(l.owner, l.path(""), false).Body.String()
	if !strings.Contains(body, href) || strings.Contains(body, ">Read along<") || !strings.Contains(body, ">Read<") {
		t.Error("without an alignment the link says Read")
	}
	if strings.Contains(l.get(l.noRead, l.path(""), false).Body.String(), "/read?edition=") {
		t.Error("a person who cannot read sees the link")
	}
}
