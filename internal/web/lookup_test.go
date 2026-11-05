package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// Look-up and the word list (M5 G1). Helpers are lk*.

// lkDict writes a TSV dictionary into the lab's dictionaries folder.
func lkDict(t *testing.T, l *rdrLab, name, body string) {
	t.Helper()
	dir := filepath.Join(l.a.dir, "dictionaries")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".tsv"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func lkLookup(l *rdrLab, ck *http.Cookie, q string) *struct {
	Code int
	Body string
} {
	rec := l.get(ck, "/hx/books/"+l.book.ID+"/read/lookup?q="+url.QueryEscape(q), true)
	return &struct {
		Code int
		Body string
	}{rec.Code, rec.Body.String()}
}

func lkSave(t *testing.T, l *rdrLab, ck *http.Cookie, word, quote string) *struct {
	Code int
	Body string
} {
	t.Helper()
	s0, _ := l.sidIn(1)
	rec := l.hx(t, http.MethodPost, "/hx/books/"+l.book.ID+"/words", url.Values{"edition_id": {l.ed.ID}, "word": {word}, "quote": {quote}, "sid": {s0}, "off": {"4"}}, ck)
	return &struct {
		Code int
		Body string
	}{rec.Code, rec.Body.String()}
}

func TestDefineRoute(t *testing.T) {
	l := rdrSeed(t)
	// No dictionary yet: a plain sentence, and the hint only for the administrator.
	owner := lkLookup(l, l.owner, "lantern")
	if owner.Code != http.StatusOK || !strings.Contains(owner.Body, lookupNoDictionary) || !strings.Contains(owner.Body, lookupAdminHint) {
		t.Errorf("no dictionary, owner: %d %s", owner.Code, owner.Body)
	}
	if reader := lkLookup(l, l.reader, "lantern"); !strings.Contains(reader.Body, lookupNoDictionary) || strings.Contains(reader.Body, lookupAdminHint) {
		t.Errorf("no dictionary, reader: %s", reader.Body)
	}
	// With a dictionary: the entry as escaped text, even when the file holds a script.
	lkDict(t, l, "Plain words", "lantern\tA lamp with <script>alert(1)</script> a case &amp; a handle\nserene\tcalm and quiet\n")
	got := lkLookup(l, l.reader, "Lantern")
	if got.Code != http.StatusOK || !strings.Contains(got.Body, "<dialog") || !strings.Contains(got.Body, "Plain words") || !strings.Contains(got.Body, "A lamp with &lt;script&gt;alert(1)&lt;/script&gt; a case &amp;amp; a handle") {
		t.Errorf("entry: %d %s", got.Code, got.Body)
	}
	if strings.Contains(got.Body, "<script") || strings.Contains(got.Body, lookupAdminHint) {
		t.Errorf("a script or the admin hint came through: %s", got.Body)
	}
	// A word that is not in it.
	if none := lkLookup(l, l.owner, "zzzxq"); !strings.Contains(none.Body, lookupNone) || strings.Contains(none.Body, lookupAdminHint) {
		t.Errorf("not found: %s", none.Body)
	}
	// A script in the query is escaped everywhere it is shown.
	evil := lkLookup(l, l.owner, "<script>alert(1)</script>")
	if evil.Code != http.StatusOK || strings.Contains(evil.Body, "<script>") || !strings.Contains(evil.Body, "&lt;script&gt;") {
		t.Errorf("script in q: %d %s", evil.Code, evil.Body)
	}
	// 422 with a toast fragment, not a page: empty, 65 runes, no letters.
	for name, q := range map[string]string{"empty": "", "spaces": "   ", "too long": strings.Repeat("é", 65), "no letters": "***"} {
		bad := lkLookup(l, l.owner, q)
		if bad.Code != http.StatusUnprocessableEntity || !strings.Contains(bad.Body, "ts-toast") || strings.Contains(bad.Body, "<html") {
			t.Errorf("%s: %d %s", name, bad.Code, bad.Body)
		}
	}
	if ok := lkLookup(l, l.owner, strings.Repeat("é", 64)); ok.Code != http.StatusOK {
		t.Errorf("64 runes: %d", ok.Code)
	}
	// English forms only for a book in English.
	if _, err := l.a.st.Writer().Exec(`UPDATE editions SET language = 'fr' WHERE id = ?`, l.ed.ID); err != nil {
		t.Fatal(err)
	}
	if fr := lkLookup(l, l.owner, "serenes"); !strings.Contains(fr.Body, lookupNone) {
		t.Errorf("French book: -s was stripped: %s", fr.Body)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE editions SET language = 'en' WHERE id = ?`, l.ed.ID); err != nil {
		t.Fatal(err)
	}
	if en := lkLookup(l, l.owner, "serenes"); !strings.Contains(en.Body, "calm and quiet") {
		t.Errorf("English book: %s", en.Body)
	}
	// Rights: no can_read is 404, and so is a visitor with no session.
	for name, ck := range map[string]*http.Cookie{"member without can_read": l.noRead, "guest without can_read": l.guestNoR} {
		if c := lkLookup(l, ck, "lantern").Code; c != http.StatusNotFound {
			t.Errorf("%s: %d", name, c)
		}
		if c := lkSave(t, l, ck, "lantern", "q").Code; c != http.StatusNotFound {
			t.Errorf("%s saves: %d", name, c)
		}
	}
	if rec := l.a.do(http.MethodGet, "/hx/books/"+l.book.ID+"/read/lookup?q=lantern", nil, nil); rec.Code == http.StatusOK {
		t.Error("anonymous visitor got a definition")
	}
	// The page carries the hooks: the button (online only), the script address and the hidden form.
	page := l.get(l.owner, l.readPath("ch=2"), false).Body.String()
	for _, want := range []string{`data-ts-mark="lookup"`, `data-lookup-src="/static/js/reader-lookup`, `id="reader-word-form"`, `hx-post="/hx/books/` + l.book.ID + `/words"`} {
		if !strings.Contains(page, want) {
			t.Errorf("reader page lacks %q", want)
		}
	}
}

func TestDefineSearchLink(t *testing.T) {
	l := rdrSeed(t)
	got := lkLookup(l, l.owner, "Zoë & co").Body
	m := regexp.MustCompile(`<a href="(https://[^"]*)"([^>]*)>`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("no web search link: %s", got)
	}
	if m[1] != "https://duckduckgo.com/?q=Zo%C3%AB+%26+co" {
		t.Errorf("href = %q, want only the selected word, percent-encoded", m[1])
	}
	for _, want := range []string{`target="_blank"`, `rel="noopener noreferrer"`} {
		if !strings.Contains(m[2], want) {
			t.Errorf("link lacks %s: %s", want, m[2])
		}
	}
	if n := strings.Count(got, "https://"); n != 1 {
		t.Errorf("%d absolute addresses in the popup, want only the one link", n)
	}
	for _, leak := range []string{l.book.ID, l.ed.ID, l.ownerID} {
		if strings.Contains(m[1], leak) {
			t.Errorf("the search link carries %q", leak)
		}
	}
	if !strings.Contains(got, "This opens outside Tendshelf.") {
		t.Error("the popup does not say the link leaves Tendshelf")
	}
}

// lkFailing is a transport that fails the test: nothing here may reach the network.
type lkFailing struct{ t *testing.T }

func (f lkFailing) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("outbound request to %s", r.URL)
	return nil, errors.New("no network in this test")
}

func TestDefineNoFetch(t *testing.T) {
	l := rdrSeed(t)
	lkDict(t, l, "d", "lantern\ta lamp\n")
	old, oldClient := http.DefaultTransport, http.DefaultClient.Transport
	http.DefaultTransport, http.DefaultClient.Transport = lkFailing{t}, lkFailing{t}
	defer func() { http.DefaultTransport, http.DefaultClient.Transport = old, oldClient }()
	for _, q := range []string{"lantern", "missing"} {
		if c := lkLookup(l, l.owner, q).Code; c != http.StatusOK {
			t.Errorf("%s: %d", q, c)
		}
	}
	if c := lkSave(t, l, l.owner, "lantern", "a lantern").Code; c != http.StatusOK {
		t.Errorf("save: %d", c)
	}
	if c := l.get(l.owner, "/me/words", false).Code; c != http.StatusOK {
		t.Errorf("word list: %d", c)
	}
}

func TestWordListPage(t *testing.T) {
	l := rdrSeed(t)
	ctx := context.Background()
	rec := lkSave(t, l, l.owner, "  Lantern ", "A  lantern\nin the window.")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body, lookupSaved) {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if again := lkSave(t, l, l.owner, "LANTERN", "other"); again.Code != http.StatusOK {
		t.Errorf("save twice: %d", again.Code)
	}
	if n, _ := l.a.srv.progress.CountWords(ctx, l.ownerID); n != 1 {
		t.Fatalf("%d words stored, want 1 (once per person, book and word)", n)
	}
	for name, form := range map[string]url.Values{"empty": {"word": {" "}}, "long": {"word": {strings.Repeat("w", 65)}}} {
		form.Set("quote", "x")
		if c := l.hx(t, http.MethodPost, "/hx/books/"+l.book.ID+"/words", form, l.owner).Code; c != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, c)
		}
	}
	// The page: the word, the sentence, the book, a way back to the page.
	page := l.get(l.owner, "/me/words", false)
	body := page.Body.String()
	s0, _ := l.sidIn(1)
	for _, want := range []string{">Lantern<", "A lantern in the window.", "Textword Lantern", "at=" + s0 + "&amp;ch=2", "/me/words.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("word list lacks %q", want)
		}
	}
	// Another person: empty, cannot delete it, cannot see its export.
	other := l.get(l.reader, "/me/words", false).Body.String()
	if strings.Contains(other, "Lantern") || !strings.Contains(other, wordsEmptyTitle) {
		t.Errorf("another person's page: %s", other)
	}
	var id string
	var created int64
	if err := l.a.st.Reader().QueryRow(`SELECT id, created_at FROM word_list WHERE user_id = ?`, l.ownerID).Scan(&id, &created); err != nil {
		t.Fatal(err)
	}
	if rec := l.a.do(http.MethodPost, "/me/words/"+id+"/delete", url.Values{"_csrf": {l.a.tokenFor(t, l.reader)}}, nil, l.reader); rec.Code != http.StatusNotFound {
		t.Errorf("another person deletes: %d", rec.Code)
	}
	if rec := l.a.do(http.MethodPost, "/me/words/"+id+"/delete", nil, nil, l.owner); rec.Code == http.StatusSeeOther {
		t.Error("a delete without the CSRF token went through")
	}
	if n, _ := l.a.srv.progress.CountWords(ctx, l.ownerID); n != 1 {
		t.Fatal("the word was removed by a request that should have been refused")
	}
	// Export bytes.
	day := time.UnixMilli(created).UTC().Format("2006-01-02")
	md := l.get(l.owner, "/me/words.md", false)
	if want := "# Your word list\n\n## Textword Lantern\n\n- **Lantern** - A lantern in the window. (" + day + ")\n"; md.Body.String() != want {
		t.Errorf("markdown = %q, want %q", md.Body.String(), want)
	}
	if h := md.Header(); !strings.HasPrefix(h.Get("Content-Disposition"), "attachment;") || h.Get("Cache-Control") != "no-store" || !strings.HasPrefix(h.Get("Content-Type"), "text/markdown") {
		t.Errorf("markdown headers: %v", h)
	}
	js := l.get(l.owner, "/me/words.json", false)
	var out struct{ Words []wordExport }
	if err := json.Unmarshal(js.Body.Bytes(), &out); err != nil || len(out.Words) != 1 || out.Words[0].Word != "Lantern" || out.Words[0].Book != "Textword Lantern" || out.Words[0].Saved != day {
		t.Errorf("json: %v %s", err, js.Body.String())
	}
	if h := js.Header(); h.Get("Content-Type") != "application/json" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment;") || h.Get("Cache-Control") != "no-store" {
		t.Errorf("json headers: %v", h)
	}
	if o := l.get(l.reader, "/me/words.json", false).Body.String(); strings.Contains(o, "Lantern") {
		t.Errorf("another person's export: %s", o)
	}
	// The owner removes it with the form.
	if rec := l.a.do(http.MethodPost, "/me/words/"+id+"/delete", url.Values{"_csrf": {l.a.tokenFor(t, l.owner)}}, nil, l.owner); rec.Code != http.StatusSeeOther {
		t.Errorf("delete: %d", rec.Code)
	}
	if n, _ := l.a.srv.progress.CountWords(ctx, l.ownerID); n != 0 {
		t.Error("the word is still there")
	}
}

// TestWordListRevoked: a book the person can no longer read does not show its title, in the page or the export;
// the word stays stored.
func TestWordListRevoked(t *testing.T) {
	l := rdrSeed(t)
	if c := lkSave(t, l, l.reader, "ember", "an ember").Code; c != http.StatusOK {
		t.Fatalf("save: %d", c)
	}
	if body := l.get(l.reader, "/me/words", false).Body.String(); !strings.Contains(body, "Textword Lantern") {
		t.Fatalf("before: %s", body)
	}
	if _, err := l.a.st.Writer().Exec(`DELETE FROM grants WHERE user_id = (SELECT user_id FROM word_list LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/me/words", "/me/words.md", "/me/words.json"} {
		if body := l.get(l.reader, path, false).Body.String(); strings.Contains(body, "Textword Lantern") || strings.Contains(body, "ember") {
			t.Errorf("%s shows a revoked book: %s", path, body)
		}
	}
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM word_list`).Scan(&n); err != nil || n != 1 {
		t.Errorf("stored words = %d, %v; want the word kept", n, err)
	}
}

func TestWordListLimit(t *testing.T) {
	l := rdrSeed(t)
	old := progress.MaxWordsPerPerson
	progress.MaxWordsPerPerson = 1
	defer func() { progress.MaxWordsPerPerson = old }()
	if c := lkSave(t, l, l.owner, "one", "q").Code; c != http.StatusOK {
		t.Fatalf("first: %d", c)
	}
	if full := lkSave(t, l, l.owner, "two", "q"); full.Code != http.StatusConflict || !strings.Contains(full.Body, lookupFull) {
		t.Errorf("full: %d %s", full.Code, full.Body)
	}
}

// TestWordListMarksUntouched: the toolbar's colours still highlight, Copy only copies, and only the Look up
// button reaches the new module. The browser test covers the clicks; this pins the page and the script.
func TestWordListMarksUntouched(t *testing.T) {
	l := rdrSeed(t)
	page := l.get(l.owner, l.readPath("ch=2"), false).Body.String()
	if n := strings.Count(page, `data-ts-mark="highlight"`); n != 4 {
		t.Errorf("%d colour buttons, want 4", n)
	}
	for _, mark := range []string{"note", "copy", "lookup"} {
		if n := strings.Count(page, `data-ts-mark="`+mark+`"`); n != 1 {
			t.Errorf("%d %s buttons, want 1", n, mark)
		}
	}
	js, err := os.ReadFile("static/js/reader-marks.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	i := strings.Index(src, "function act(")
	if i < 0 {
		t.Fatal("no act()")
	}
	act := src[i : i+strings.Index(src[i:], "\n  }\n")]
	for _, want := range []string{`kind === "copy") navigator.clipboard.writeText(marked.text)`, `kind === "highlight") save(marked)`, `kind === "lookup") import(`} {
		if !strings.Contains(act, want) {
			t.Errorf("act() lacks %q:\n%s", want, act)
		}
	}
	if strings.Contains(act, `"copy") save`) || strings.Contains(act, `else save(`) {
		t.Errorf("a path other than the colours saves a highlight:\n%s", act)
	}
	// Loading the page, a look-up and a copy write no highlight and no place.
	lkLookup(l, l.owner, "lantern")
	hls, _ := l.a.srv.progress.Highlights(context.Background(), l.ownerID, l.book.ID)
	if len(hls) != 0 {
		t.Errorf("%d highlights written without a colour click", len(hls))
	}
	if n := lkCount(t, l.a, "reading_positions"); n != 0 {
		t.Errorf("a look-up wrote %d reading places", n)
	}
}

func lkCount(t *testing.T, a *testApp, table string) int {
	t.Helper()
	var n int
	if err := a.st.Reader().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
