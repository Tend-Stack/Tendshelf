package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// rsLab is the text lab with one ready EPUB read from the given bytes.
type rsLab struct {
	*textLab
	file ebook.File
	ed   ebook.Edition
	doc  *ebook.Doc
}

// rsSeed uploads epub as the owner and waits until it is read.
func rsSeed(t *testing.T, epub []byte) *rsLab {
	t.Helper()
	l := &rsLab{textLab: newTextLab(t)}
	l.run(t)
	f, err := l.svc.AddUpload(context.Background(), l.book, l.ownerID, "serve.epub", bytes.NewReader(epub))
	if err != nil {
		t.Fatal(err)
	}
	l.file = l.waitState(t, f.ID, "ready")
	if err := l.a.runner.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	ed, ok, err := l.svc.Store().CurrentEdition(context.Background(), l.file.ID)
	if err != nil || !ok {
		t.Fatalf("edition: %v %v", ok, err)
	}
	l.ed = ed
	if l.doc, err = ebook.ReadDoc(l.a.dir+"/text", ed.DocSHA256); err != nil {
		t.Fatal(err)
	}
	return l
}

func (l *rsLab) page(q string) string {
	p := "/books/" + l.book.ID + "/read"
	if q != "" {
		p += "?" + q
	}
	return p
}

// rsPNG is a small picture the tests put in an EPUB.
func rsPNG(w, h int) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)))
	return buf.Bytes()
}

// rsAttr reads the value of an attribute of the first tag in s that has it.
func rsAttr(s, name string) string {
	m := regexp.MustCompile(` ` + name + `="([^"]*)"`).FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

// rsReaderText is the markup of the reading area of a page, without the layout around it.
func rsReaderText(s string) string {
	i := strings.Index(s, `<article`)
	j := strings.Index(s, `</article>`)
	if i < 0 || j < i {
		return s
	}
	return s[i:j]
}

func TestReaderServeHostile(t *testing.T) {
	evilSVG := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert("svgscript")</script></svg>`
	body := `<script>alert("bodyscript")</script><style>p{background:url(javascript:alert(1))}</style>` +
		`<p onclick="alert('click')" style="color:red">Plain <a href="javascript:alert('href')">bad link</a> ` +
		`<span style="x">span</span> <a href="https://example.com/out">out</a> text.</p>` +
		`<p><img src="evil.svg" alt="an svg" onerror="alert('onerror')"/></p>` +
		`<svg xmlns="http://www.w3.org/2000/svg"><script>alert("inline")</script><image href="evil.svg"/></svg>` +
		`<p><img src="pic.png" alt="A picture" width="9999" height="1"/></p>`
	epub := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Hostile", Authors: []string{"A. Writer"},
		Chapters:    []ebooktest.EPUBChapter{{Title: "One", Raw: body}},
		ManifestXML: `<item id="e" href="evil.svg" media-type="image/svg+xml"/><item id="p" href="pic.png" media-type="image/png"/>`,
		Extra:       []ebooktest.Entry{{Name: "OEBPS/evil.svg", Data: []byte(evilSVG)}, {Name: "OEBPS/pic.png", Data: rsPNG(4, 3)}}})
	l := rsSeed(t, epub)
	var answers []*httptest.ResponseRecorder
	answers = append(answers, l.get(l.owner, l.page(""), false), l.get(l.owner, "/hx/books/"+l.book.ID+"/read/chapters/1", true))
	for _, b := range l.doc.Chapters[0].Blocks {
		if b.Kind == ebook.KindImg {
			answers = append(answers, l.get(l.owner, "/books/"+l.book.ID+"/read/images/"+b.ID, false))
		}
	}
	if len(answers) < 4 {
		t.Fatalf("expected the page, the fragment and two picture answers, got %d", len(answers))
	}
	for i, rec := range answers {
		s := rec.Body.String()
		text := rsReaderText(s)
		inArticle := strings.Contains(s, "<article")
		for _, bad := range []string{"bodyscript", "inline", "svgscript", "onerror", "onclick", "javascript:", "alert(", "<style", ` style=`, "<svg", "url("} {
			// The error page of a refused picture draws its own icons; only the reading area must be free of markup.
			if !inArticle && (bad == "<svg" || bad == "<style" || bad == "url(" || bad == " style=") {
				continue
			}
			if strings.Contains(text, bad) {
				t.Errorf("answer %d carries %q", i, bad)
			}
		}
	}
	page := answers[0]
	if got := page.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("page CSP changed: %q", got)
	}
	if !strings.Contains(page.Body.String(), `rel="noopener noreferrer" target="_blank"`) || !strings.Contains(page.Body.String(), `href="https://example.com/out"`) {
		t.Error("the outside link lost its address or its rel")
	}
	if !strings.Contains(page.Body.String(), "bad link") {
		t.Error("the text of the dropped link must stay")
	}
	// The SVG block is refused, the PNG is served.
	served := 0
	for _, rec := range answers[2:] {
		switch rec.Code {
		case http.StatusOK:
			served++
			if rec.Header().Get("Content-Type") != "image/png" {
				t.Errorf("served %q", rec.Header().Get("Content-Type"))
			}
		case http.StatusNotFound:
			if strings.Contains(rec.Body.String(), "svgscript") {
				t.Error("svg bytes in a refusal")
			}
		default:
			t.Errorf("picture answer %d", rec.Code)
		}
	}
	if served != 1 {
		t.Errorf("%d pictures served, want 1", served)
	}
}

func TestReaderServeParts(t *testing.T) {
	var sb strings.Builder
	para := strings.Repeat("The long sentence goes on and on without any pause at all. ", 7) // about 400 characters
	// Words that differ from paragraph to paragraph, so the zip does not look like a bomb.
	seed := uint32(7)
	word := func() string {
		seed = seed*1664525 + 1013904223
		return strconv.FormatUint(uint64(seed), 36)
	}
	for i := 0; i < 5000; i++ {
		var p strings.Builder
		for p.Len() < len(para) {
			p.WriteString(word() + " ")
		}
		sb.WriteString("<p>" + p.String() + "</p>")
	}
	epub := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Long", Authors: []string{"A. Writer"},
		Chapters: []ebooktest.EPUBChapter{{Title: "Everything", Raw: sb.String()}}})
	l := rsSeed(t, epub)
	if len(l.doc.Chapters) != 1 {
		t.Fatalf("%d chapters", len(l.doc.Chapters))
	}
	ch := l.doc.Chapters[0]
	parts := rsParts(ch)
	if len(parts) < 40 {
		t.Fatalf("%d parts for 2 MB", len(parts))
	}
	next, units := 0, 0
	for n := 1; n <= len(parts); n++ {
		rec := l.get(l.owner, "/hx/books/"+l.book.ID+"/read/chapters/1?part="+strconv.Itoa(n), true)
		if rec.Code != http.StatusOK {
			t.Fatalf("part %d: %d", n, rec.Code)
		}
		s := rec.Body.String()
		if rsAttr(s, "data-part") != strconv.Itoa(n) {
			t.Errorf("part %d says %q", n, rsAttr(s, "data-part"))
		}
		ids := regexp.MustCompile(`id="b-c1\.p(\d+)"`).FindAllStringSubmatch(s, -1)
		partUnits := 0
		for _, m := range ids {
			if got, _ := strconv.Atoi(m[1]); got != next+1 {
				t.Fatalf("part %d: block %s after %d", n, m[1], next)
			}
			next++
			partUnits += rsUnits(ch.Blocks[next-1].Text)
		}
		if partUnits >= 60000 || len(s) > 300000 {
			t.Errorf("part %d has %d units of text", n, partUnits)
		}
		units += partUnits
		if n < len(parts) && !strings.Contains(s, `data-next-href="/hx/books/`+l.book.ID+`/read/chapters/1?edition=`) && !strings.Contains(s, "part="+strconv.Itoa(n+1)) {
			t.Errorf("part %d has no next address", n)
		}
	}
	if next != len(ch.Blocks) {
		t.Errorf("parts cover %d of %d blocks", next, len(ch.Blocks))
	}
	// `at` finds its part, and so does a link to a block inside a late part.
	mid := ch.Blocks[len(ch.Blocks)*3/4]
	sid := ebook.SentenceID(mid.ID, 0)
	rec := l.get(l.owner, l.page("at="+sid), false)
	want := rsPartIndex(parts, len(ch.Blocks)*3/4)
	if rec.Code != http.StatusOK || rsAttr(rec.Body.String(), "data-part") != strconv.Itoa(want) || !strings.Contains(rec.Body.String(), `id="b-`+mid.ID+`"`) {
		t.Errorf("at=%s: status %d, part %q want %d", sid, rec.Code, rsAttr(rec.Body.String(), "data-part"), want)
	}
	if l.get(l.owner, l.page("ch=1&part=999"), false).Code != http.StatusNotFound {
		t.Error("a part that does not exist must be 404")
	}
}

func TestReaderServeImage(t *testing.T) {
	epub := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Pictures", Authors: []string{"A. Writer"},
		Chapters:    []ebooktest.EPUBChapter{{Title: "One", Raw: `<p>Before.</p><img src="pic.png" alt="Lamp"/><p>After.</p>`}},
		ManifestXML: `<item id="p" href="pic.png" media-type="image/png"/>`,
		Extra:       []ebooktest.Entry{{Name: "OEBPS/pic.png", Data: rsPNG(5, 4)}}})
	l := rsSeed(t, epub)
	var img ebook.Block
	for _, b := range l.doc.Chapters[0].Blocks {
		if b.Kind == ebook.KindImg {
			img = b
		}
	}
	if img.ID == "" || img.ImgW != 5 || img.ImgH != 4 {
		t.Fatalf("image block %+v", img)
	}
	path := "/books/" + l.book.ID + "/read/images/" + img.ID
	rec := l.get(l.owner, path, false)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("owner: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	for k, v := range map[string]string{"X-Content-Type-Options": "nosniff", "Content-Security-Policy": "default-src 'none'; sandbox", "Cache-Control": "private, max-age=86400"} {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if !bytes.Equal(rec.Body.Bytes(), rsPNG(5, 4)) {
		t.Error("the picture bytes changed")
	}
	page := l.get(l.owner, l.page(""), false).Body.String()
	if !strings.Contains(page, `src="`+path+`?edition=`) || !strings.Contains(page, `width="5" height="4"`) || !strings.Contains(page, `loading="lazy"`) {
		t.Error("the page does not point at the picture route with its size")
	}
	for name, ck := range map[string]*http.Cookie{"reader": l.reader, "guest with can_read": l.guest} {
		if rec := l.get(ck, path, false); rec.Code != http.StatusOK {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	for name, ck := range map[string]*http.Cookie{"member without can_read": l.noRead, "guest without can_read": l.guestNoR} {
		if rec := l.get(ck, path, false); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, rec.Code)
		}
	}
	if rec := l.get(nil, path, false); rec.Code == http.StatusOK {
		t.Error("anonymous got the picture")
	}
	for name, p := range map[string]string{
		"another edition":   path + "?edition=ed_other",
		"a text block":      "/books/" + l.book.ID + "/read/images/c1.p1",
		"traversal":         "/books/" + l.book.ID + "/read/images/..%2f..%2fetc%2fpasswd",
		"zip path":          "/books/" + l.book.ID + "/read/images/OEBPS%2fpic.png",
		"a block too far":   "/books/" + l.book.ID + "/read/images/c1.p99",
		"a chapter too far": "/books/" + l.book.ID + "/read/images/c9.p1",
	} {
		if rec := l.get(l.owner, p, false); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, rec.Code)
		}
	}
}

func TestReaderServeResume(t *testing.T) {
	var chs []ebooktest.EPUBChapter
	chs = append(chs, ebooktest.EPUBChapter{Title: "Title Page", EpubType: "titlepage", Paras: []string{"Resume Book"}})
	for i := 1; i <= 4; i++ {
		chs = append(chs, ebooktest.EPUBChapter{Title: fmt.Sprintf("Chapter %d", i), EpubType: "chapter",
			Paras: []string{fmt.Sprintf("Opening words of chapter %d with a lantern of number %d.", i, i*111), "A second paragraph follows."}})
	}
	l := rsSeed(t, ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Resume Book", Authors: []string{"A. Writer"}, Chapters: chs}))
	ctx := context.Background()
	at := func(body string) string { return rsAttr(rsReaderText(body), "data-at") }
	open := func(q string) string {
		rec := l.get(l.owner, l.page(q), false)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %q: %d", q, rec.Code)
		}
		return rec.Body.String()
	}
	save := func(ed, sid, quote string, off, ch, bp int) {
		t.Helper()
		if _, _, err := l.a.srv.progress.SaveReading(ctx, progress.ReadingSave{UserID: l.ownerID, BookID: l.book.ID, EditionID: ed, ChapterIdx: ch,
			SID: sid, Off: off, Quote: quote, CharPos: 1, PercentBP: bp, ClientUpdatedAt: time.Now().UnixMilli(), DeviceID: "rsdevice01"}); err != nil {
			t.Fatal(err)
		}
	}
	// Rung 1: nothing saved, nothing asked: the first chapter that is not front matter.
	body := open("")
	if !strings.Contains(body, `id="reader-ch-2"`) || at(body) != "" {
		t.Errorf("no place: want chapter 2 with no sentence, got at=%q", at(body))
	}
	// Rung 2: the place saved on this edition.
	b3 := l.doc.Chapters[3].Blocks[1]
	sid3 := ebook.SentenceID(b3.ID, 0)
	save(l.ed.ID, sid3, "lantern of number 333", 20, 4, 5000)
	body = open("")
	if !strings.Contains(body, `id="reader-ch-4"`) || at(body) != sid3 || rsAttr(rsReaderText(body), "data-off") != "20" {
		t.Errorf("saved place: at=%q off=%q", at(body), rsAttr(rsReaderText(body), "data-off"))
	}
	// An address that names a place wins over the saved one.
	if body = open("ch=3"); !strings.Contains(body, `id="reader-ch-3"`) || at(body) != "" {
		t.Error("ch=3 must win over the saved place")
	}
	if body = open("at=" + ebook.SentenceID(l.doc.Chapters[1].Blocks[0].ID, 0) + "&off=4"); rsAttr(rsReaderText(body), "data-off") != "4" {
		t.Error("off= lost")
	}
	// Rung 3: the edition is gone from the mark (NULL) and the sentence id points nowhere: found again by its quote.
	save("", "c4.p9.s0", "lantern of number 444", 0, 4, 5000)
	if _, err := l.a.st.Writer().Exec(`UPDATE reading_positions SET client_updated_at = client_updated_at + 1 WHERE user_id = ?`, l.ownerID); err != nil {
		t.Fatal(err)
	}
	b4 := l.doc.Chapters[4].Blocks[1]
	if body = open(""); at(body) != ebook.SentenceID(b4.ID, 0) {
		t.Errorf("quote rung: at=%q, want %s", at(body), ebook.SentenceID(b4.ID, 0))
	}
	// Rung 4: no quote that matches: the percent decides.
	save("", "c4.p9.s0", "words that are nowhere in the book", 0, 4, 9999)
	if _, err := l.a.st.Writer().Exec(`UPDATE reading_positions SET client_updated_at = client_updated_at + 5 WHERE user_id = ?`, l.ownerID); err != nil {
		t.Fatal(err)
	}
	body = open("")
	last := l.doc.Chapters[len(l.doc.Chapters)-1]
	if c, _ := rdrSIDExists(l.doc, at(body)); c != len(l.doc.Chapters) || !strings.HasPrefix(at(body), "c"+strconv.Itoa(last.Idx)+".") {
		t.Errorf("percent rung: at=%q", at(body))
	}
	// Another person starts at the beginning.
	rec := l.get(l.reader, l.page(""), false)
	if rec.Code != http.StatusOK || at(rec.Body.String()) != "" {
		t.Errorf("reader B inherited the owner's place: %q", at(rec.Body.String()))
	}
	// Listening progress is not a reading place.
	if _, err := l.a.srv.progress.Save(ctx, progress.Update{UserID: l.ownerID, BookID: l.book.ID, PositionMs: 90_000, DurationMs: sweepHourMs,
		ClientUpdatedAt: time.Now().UnixMilli(), DeviceID: "rsdevice02"}); err != nil {
		t.Fatal(err)
	}
	if got := at(open("")); got == "" {
		t.Error("the saved reading place disappeared after listening")
	}
}

func TestReaderServeReading(t *testing.T) {
	l := rsSeed(t, textEPUB(t, "Reading API", 2))
	ctx := context.Background()
	sid := ebook.SentenceID(l.doc.Chapters[1].Blocks[0].ID, 0)
	form := func(mod func(url.Values)) url.Values {
		v := url.Values{"edition_id": {l.ed.ID}, "chapter_idx": {"2"}, "sid": {sid}, "off": {"5"}, "quote": {"first line"},
			"char_pos": {"40"}, "client_updated_at": {strconv.FormatInt(time.Now().UnixMilli(), 10)}, "device_id": {"rsphone0001"}}
		if mod != nil {
			mod(v)
		}
		return v
	}
	post := func(ck *http.Cookie, path string, v url.Values) *httptest.ResponseRecorder {
		t.Helper()
		v.Set("_csrf", l.a.tokenFor(t, ck))
		return l.a.do(http.MethodPost, path, v, nil, ck)
	}
	type answer struct {
		Applied bool
		Reading map[string]any
	}
	decode := func(rec *httptest.ResponseRecorder) answer {
		t.Helper()
		var a answer
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			t.Fatalf("%s: %v", rec.Body.String(), err)
		}
		return a
	}
	path := "/reading/" + l.book.ID
	rec := post(l.owner, path, form(nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	a := decode(rec)
	_, total := rsCharsBefore(l.doc, 0)
	if !a.Applied || int(a.Reading["charPos"].(float64)) != 40 || int(a.Reading["percentBp"].(float64)) != 40*10000/total {
		t.Errorf("saved = %v (total %d)", a.Reading, total)
	}
	// The percent is the server's: a char_pos past the end is clamped to the book.
	if a = decode(post(l.owner, path, form(func(v url.Values) { v.Set("char_pos", "99999999") }))); !a.Applied || int(a.Reading["percentBp"].(float64)) != 10000 || int(a.Reading["charPos"].(float64)) != total {
		t.Errorf("clamp: %v", a.Reading)
	}
	// Newest wins: an older save from another device does not replace it.
	old := post(l.owner, path, form(func(v url.Values) {
		v.Set("client_updated_at", "1000")
		v.Set("device_id", "rstablet001")
		v.Set("char_pos", "1")
	}))
	if a = decode(old); a.Applied || a.Reading["deviceId"] != "rsphone0001" {
		t.Errorf("an old save won: %v", a)
	}
	// Reading time is counted when the save carries it.
	post(l.owner, path, form(func(v url.Values) { v.Set("chars_read", "900"); v.Set("active_ms", "30000") }))
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM reading_sessions WHERE user_id = ? AND chars_read >= 900`, l.ownerID).Scan(&n); err != nil || n != 1 {
		t.Errorf("sessions %d (%v)", n, err)
	}
	// GET returns it; user B has nothing; listening progress is untouched.
	got := decode(l.get(l.owner, path, false))
	if got.Reading["sid"] != sid || int(got.Reading["off"].(float64)) != 5 {
		t.Errorf("GET = %v", got.Reading)
	}
	if rec = l.get(l.reader, path, false); !strings.Contains(rec.Body.String(), `"reading":null`) {
		t.Errorf("user B: %s", rec.Body.String())
	}
	if _, err := l.a.srv.progress.Get(ctx, l.ownerID, l.book.ID); err == nil {
		t.Error("a reading save wrote listening progress")
	}
	// A merged-away id lands on the book that took over.
	if _, err := l.a.st.Writer().Exec(`INSERT INTO merged_books(from_id, to_id, merged_at) VALUES ('bk_merged_away', ?, 1)`, l.book.ID); err != nil {
		t.Fatal(err)
	}
	via := post(l.reader, "/reading/bk_merged_away", form(nil))
	if via.Code != http.StatusOK || decode(via).Reading["bookId"] != l.book.ID {
		t.Errorf("alias: %d %s", via.Code, via.Body.String())
	}
	// The 422s.
	for name, mod := range map[string]func(url.Values){
		"edition":        func(v url.Values) { v.Set("edition_id", "ed_nope") },
		"chapter zero":   func(v url.Values) { v.Set("chapter_idx", "0") },
		"chapter past":   func(v url.Values) { v.Set("chapter_idx", "99") },
		"sid garbage":    func(v url.Values) { v.Set("sid", "not-a-sid") },
		"sid wrong ch":   func(v url.Values) { v.Set("chapter_idx", "3") },
		"sid not there":  func(v url.Values) { v.Set("sid", "c2.p99.s0") },
		"off too large":  func(v url.Values) { v.Set("off", "99999") },
		"off negative":   func(v url.Values) { v.Set("off", "-1") },
		"quote too long": func(v url.Values) { v.Set("quote", strings.Repeat("x", 65)) },
		"char_pos":       func(v url.Values) { v.Set("char_pos", "-4") },
		"no device":      func(v url.Values) { v.Del("device_id") },
		"no time":        func(v url.Values) { v.Set("client_updated_at", "0") },
		"active_ms":      func(v url.Values) { v.Set("active_ms", "x") },
	} {
		if rec := post(l.owner, path, form(mod)); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d, want 422", name, rec.Code)
		}
	}
	// Who may use it.
	for name, ck := range map[string]*http.Cookie{"member without can_read": l.noRead, "guest without can_read": l.guestNoR} {
		if rec := post(ck, path, form(nil)); rec.Code != http.StatusNotFound {
			t.Errorf("POST as %s: %d, want 404", name, rec.Code)
		}
		if rec := l.get(ck, path, false); rec.Code != http.StatusNotFound {
			t.Errorf("GET as %s: %d, want 404", name, rec.Code)
		}
	}
	if rec := post(l.guest, path, form(nil)); rec.Code != http.StatusOK {
		t.Errorf("guest with can_read: %d", rec.Code)
	}
	if rec := l.a.do(http.MethodPost, path, form(nil), nil, l.owner); rec.Code != http.StatusForbidden {
		t.Errorf("no CSRF token: %d, want 403", rec.Code)
	}
	if rec := l.get(nil, path, false); rec.Code == http.StatusOK {
		t.Error("anonymous read a place")
	}
}

func TestReaderSpeaker(t *testing.T) {
	l := rsSeed(t, textEPUB(t, "Speaker", 2))
	ctx := context.Background()
	if _, err := l.a.st.Writer().Exec(`UPDATE audio_files SET duration_ms = ?, container = 'mp3', codec = 'mp3' WHERE book_id = ?`, sweepHourMs, l.book.ID); err != nil {
		t.Fatal(err)
	}
	// The audiobook has its own saved place; the reading place is somewhere else and must not leak into the link.
	if _, err := l.a.srv.progress.Save(ctx, progress.Update{UserID: l.ownerID, BookID: l.book.ID, PositionMs: 120_000, DurationMs: sweepHourMs,
		ClientUpdatedAt: time.Now().UnixMilli(), DeviceID: "rsdevice05"}); err != nil {
		t.Fatal(err)
	}
	sid := ebook.SentenceID(l.doc.Chapters[2].Blocks[0].ID, 0)
	if _, _, err := l.a.srv.progress.SaveReading(ctx, progress.ReadingSave{UserID: l.ownerID, BookID: l.book.ID, EditionID: l.ed.ID, ChapterIdx: 3,
		SID: sid, CharPos: 50, PercentBP: 5000, ClientUpdatedAt: time.Now().UnixMilli(), DeviceID: "rsdevice06"}); err != nil {
		t.Fatal(err)
	}
	body := l.get(l.owner, l.page(""), false).Body.String()
	bar := body[strings.Index(body, `<header class="ts-readerbar"`):strings.Index(body, `</header>`)]
	if !strings.Contains(bar, `aria-label="Play the audiobook"`) || !strings.Contains(bar, `data-ts-play="`+l.book.ID+`"`) {
		t.Fatalf("the speaker is missing from the bar: %s", bar)
	}
	if !strings.Contains(bar, `data-ts-start-ms="120000"`) || !strings.Contains(bar, "#t=") {
		t.Errorf("the speaker does not start at the audiobook's own place: %s", bar)
	}
	if strings.Contains(bar, "data-ts-from-start") || strings.Count(body, "data-ts-play=") != 1 {
		t.Error("one resume control only, and it resumes")
	}
	// Reading and listening are separate: the speaker's place did not move when the reading place was saved.
	p, err := l.a.srv.progress.Get(ctx, l.ownerID, l.book.ID)
	if err != nil || p.PositionMs != 120_000 {
		t.Errorf("listening place %d (%v)", p.PositionMs, err)
	}
	// Listening is allowed by any grant that reaches the book, so a reader gets the speaker too; a book that is
	// missing from disk (the audio cannot play) loses it.
	if rec := l.get(l.reader, l.page(""), false); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Play the audiobook") {
		t.Errorf("a reader with a grant should get the speaker (status %d)", rec.Code)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE books SET missing_since = 1 WHERE id = ?`, l.book.ID); err != nil {
		t.Fatal(err)
	}
	if rec := l.get(l.owner, l.page(""), false); rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "Play the audiobook") {
		t.Error("the speaker shows for a book whose audio is missing")
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE books SET missing_since = NULL WHERE id = ?`, l.book.ID); err != nil {
		t.Fatal(err)
	}
	// A book with no audio files has no speaker.
	if _, err := l.a.st.Writer().Exec(`DELETE FROM audio_files WHERE book_id = ?`, l.book.ID); err != nil {
		t.Fatal(err)
	}
	if rec := l.get(l.owner, l.page(""), false); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Play the audiobook") {
		t.Errorf("a text-only book shows the speaker (status %d)", rec.Code)
	}
}
