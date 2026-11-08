package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

const (
	feedNSI = "http://www.itunes.com/dtds/podcast-1.0.dtd"
	feedNSP = "https://podcastindex.org/namespace/1.0"
)

// feedLab is an app with books for podcast feeds (decision 0031):
//
//	lib1: multi (3 files, 4 chapters), single (1 file, cover), protected (.aax file), saga: sa, refused, textonly
//	lib2: sb      series "Saga": multi (1), sa (2), sb (3), refused (4), textonly (5)
//	people: m_dl (both libraries, download), m_part (lib1 only), m_nodl (lib1, listen only), g_dl (guest, lib1)
type feedLab struct {
	a                                             *testApp
	owner                                         *http.Cookie
	ownerID                                       string
	lib1, lib2                                    library.Library
	multi, single, protected, sa, sb, refused, tx library.Book
	seriesID                                      string
	ids                                           map[string]string
	files                                         map[string][]library.AudioFile // by book id
	body                                          map[string][]byte              // by file id
	clock                                         *time.Time
}

var proxyDefault = func(r *http.Request) {
	r.RemoteAddr = "10.1.1.1:5000"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-For", "198.51.100.4")
	r.Host = "podcasts.example"
}

func newFeedLab(t *testing.T) *feedLab {
	t.Helper()
	px, err := auth.ParseProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	a := newTestApp(t, func(o *Options) {
		o.TrustedProxies = px
		o.Now = func() time.Time { return clock }
	})
	l := &feedLab{a: a, ids: map[string]string{}, files: map[string][]library.AudioFile{}, body: map[string][]byte{}, clock: &clock}
	l.owner = a.owner(t)
	l.ownerID = sweepUserID(t, a, l.owner)
	l.ids["owner"] = l.ownerID
	var f1, f2 library.Folder
	l.lib1, f1 = librarytest.NewLibrary(t, a.lib, "Feeds one", adminAudioDir(t, true))
	l.lib2, f2 = librarytest.NewLibrary(t, a.lib, "Feeds two", adminAudioDir(t, true))
	add := func(f library.Folder, s librarytest.BookSpec) library.Book {
		b := librarytest.AddBook(t, a.lib, f, s)
		fs, err := a.lib.AudioFiles(context.Background(), b.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i, af := range fs {
			body := []byte(strings.Repeat(fmt.Sprintf("%s-%d|", af.ID, i), 40))
			if err := os.MkdirAll(filepath.Dir(af.Path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(af.Path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			l.body[af.ID] = body
			if _, err := a.st.Writer().Exec(`UPDATE audio_files SET size = ?, duration_ms = 1000000, offset_ms = ? WHERE id = ?`,
				len(body), int64(i)*1000000, af.ID); err != nil {
				t.Fatal(err)
			}
		}
		l.files[b.ID], _ = a.lib.AudioFiles(context.Background(), b.ID)
		return b
	}
	l.multi = add(f1, librarytest.BookSpec{Title: "Multi Book", Authors: []string{"Ann Author", "Bo Writer"}, Description: "About <multi> & more", Files: 3, Series: "Saga", Sequence: "1"})
	l.single = add(f1, librarytest.BookSpec{Title: "Single Tale", Authors: []string{"Cy Solo"}, Description: "A single file", Files: 1})
	l.protected = add(f1, librarytest.BookSpec{Title: "Protected Tale", Files: 1})
	l.sa = add(f1, librarytest.BookSpec{Title: "Saga Two", Files: 1, Series: "Saga", Sequence: "2"})
	l.sb = add(f2, librarytest.BookSpec{Title: "Saga Three", Files: 1, Series: "Saga", Sequence: "3"})
	l.refused = add(f1, librarytest.BookSpec{Title: "Saga Four", Files: 1, Series: "Saga", Sequence: "4", Refused: library.RefusedDRM})
	l.tx = add(f1, librarytest.BookSpec{Title: "Saga Five", Files: 1, Series: "Saga", Sequence: "5"})
	l.exec(t, `UPDATE audio_files SET missing_since = 1 WHERE book_id = ?`, l.tx.ID)
	series, err := a.lib.BookSeries(context.Background(), l.multi.ID)
	if err != nil || len(series) != 1 {
		t.Fatal(series, err)
	}
	l.seriesID = series[0].SeriesID
	// The multi-file book has chapters on the book's timeline; the last file starts at an untitled chapter.
	for i, c := range []struct {
		s, e  int64
		title string
	}{{0, 500000, "Opening"}, {500000, 1000000, "Middle"}, {1000000, 2000000, "Second"}, {2000000, 3000000, ""}} {
		l.exec(t, `INSERT INTO chapters(book_id, idx, start_ms, end_ms, title, source) VALUES (?, ?, ?, ?, ?, 'file')`, l.multi.ID, i, c.s, c.e, c.title)
	}
	// A copy-protected file in a book that is not refused as a whole.
	pf := l.files[l.protected.ID][0]
	aax := strings.TrimSuffix(pf.Path, ".mp3") + ".aax"
	if err := os.Rename(pf.Path, aax); err != nil {
		t.Fatal(err)
	}
	l.exec(t, `UPDATE audio_files SET path = ? WHERE id = ?`, aax, pf.ID)
	l.files[l.protected.ID], _ = a.lib.AudioFiles(context.Background(), l.protected.ID)
	// A cover for the single book.
	sha := fmt.Sprintf("%064x", 0xfeed10)
	cover := library.Cover{BookID: l.single.ID, Source: library.CoverFolder, Mime: "image/png", Width: 8, Height: 8, SHA256: sha, Path: sweepWriteCover(t, a.dir, sha)}
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
		if err := tx.PutCover(&cover); err != nil {
			return err
		}
		return tx.SetBookCover(l.single.ID, cover.ID)
	}); err != nil {
		t.Fatal(err)
	}
	l.single, _ = a.lib.Book(context.Background(), l.single.ID)

	person := func(name string, role users.Role, dl int, libs ...library.Library) {
		ck := a.as(t, role)
		id := sweepUserID(t, a, ck)
		l.ids[name] = id
		for i, lb := range libs {
			l.exec(t, `INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at)
				VALUES (?, ?, 'library', ?, ?, 0, 1, 1)`, fmt.Sprintf("gr_%s_%d", id, i), id, lb.ID, dl)
		}
	}
	person("m_dl", users.RoleMember, 1, l.lib1, l.lib2)
	person("m_part", users.RoleMember, 1, l.lib1)
	person("m_nodl", users.RoleMember, 0, l.lib1)
	person("g_dl", users.RoleGuest, 1, l.lib1)
	return l
}

func (l *feedLab) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := l.a.st.Writer().Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// mk makes a feed for a lab person and returns its token and row.
func (l *feedLab) mk(t *testing.T, who, book, series string) (string, share.Feed) {
	t.Helper()
	var tok string
	var f share.Feed
	err := l.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, f, err = l.a.srv.feeds.Create(context.Background(), tx, l.ids[who], "", book, series)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok, f
}

func (l *feedLab) get(path string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	return l.a.do(http.MethodGet, path, nil, func(r *http.Request) {
		proxyDefault(r)
		for _, m := range mod {
			m(r)
		}
	})
}

type feedItemXML struct {
	Title string `xml:"title"`
	GUID  struct {
		Perma string `xml:"isPermaLink,attr"`
		Value string `xml:",chardata"`
	} `xml:"guid"`
	PubDate   string `xml:"pubDate"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length string `xml:"length,attr"`
		Type   string `xml:"type,attr"`
	} `xml:"enclosure"`
	Duration string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
	Episode  string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd episode"`
	Image    struct {
		Href string `xml:"href,attr"`
	} `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
	Explicit    string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd explicit"`
	Description string `xml:"description"`
	Chapters    struct {
		URL  string `xml:"url,attr"`
		Type string `xml:"type,attr"`
	} `xml:"https://podcastindex.org/namespace/1.0 chapters"`
}

type feedXML struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel struct {
		Title       string `xml:"title"`
		Link        string `xml:"link"`
		Description string `xml:"description"`
		Language    string `xml:"language"`
		Author      string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd author"`
		Image       struct {
			Href string `xml:"href,attr"`
		} `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
		Block    string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd block"`
		Explicit string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd explicit"`
		Type     string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd type"`
		Items    []feedItemXML `xml:"item"`
	} `xml:"channel"`
}

func (l *feedLab) feedDoc(t *testing.T, tok string, mod ...func(*http.Request)) feedXML {
	t.Helper()
	rec := l.get("/feeds/"+tok+"/feed.xml", mod...)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed.xml = %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/rss+xml; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
	var doc feedXML
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("feed does not parse: %v\n%s", err, rec.Body.String())
	}
	return doc
}

func feedTitles(doc feedXML) []string {
	var out []string
	for _, it := range doc.Channel.Items {
		out = append(out, it.Title)
	}
	return out
}

func eq(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

func TestFeedXMLBook(t *testing.T) {
	l := newFeedLab(t)
	tok, _ := l.mk(t, "m_dl", l.multi.ID, "")
	rec := l.get("/feeds/" + tok + "/feed.xml")
	if !strings.Contains(rec.Body.String(), `xmlns:itunes="`+feedNSI+`"`) || !strings.Contains(rec.Body.String(), `xmlns:podcast="`+feedNSP+`"`) {
		t.Errorf("namespaces missing:\n%s", rec.Body.String())
	}
	doc := l.feedDoc(t, tok)
	ch := doc.Channel
	if doc.Version != "2.0" || ch.Title != "Multi Book" || ch.Description != "About <multi> & more" || ch.Author != "Ann Author, Bo Writer" ||
		ch.Block != "yes" || ch.Explicit != "false" || ch.Type != "serial" || ch.Link != "https://podcasts.example/" {
		t.Errorf("channel = %+v", ch)
	}
	if want := []string{"Opening", "Second", "Part 3 of 3"}; !eq(feedTitles(doc), want) {
		t.Fatalf("titles = %v, want %v", feedTitles(doc), want)
	}
	root := "https://podcasts.example/feeds/" + tok + "/"
	var prev time.Time
	for i, it := range ch.Items {
		f := l.files[l.multi.ID][i]
		if it.Enclosure.URL != root+"audio/"+f.ID+".mp3" || it.Enclosure.Length != strconv.Itoa(len(l.body[f.ID])) || it.Enclosure.Type != "audio/mpeg" {
			t.Errorf("enclosure %d = %+v", i, it.Enclosure)
		}
		if it.GUID.Value != "urn:tendshelf:audio:"+f.ID || it.GUID.Perma != "false" || it.Episode != strconv.Itoa(i+1) {
			t.Errorf("guid/episode %d = %+v %s", i, it.GUID, it.Episode)
		}
		pd, err := time.Parse(time.RFC1123Z, it.PubDate)
		if err != nil || !pd.After(prev) || !pd.Before(*l.clock) {
			t.Errorf("pubDate %d = %q (%v), previous %v", i, it.PubDate, err, prev)
		}
		prev = pd
		if it.Duration != "00:16:40" {
			t.Errorf("duration = %q", it.Duration)
		}
	}
	// Chapters are linked only for a file with two or more of them.
	if got := ch.Items[0].Chapters.URL; got != root+"chapters/"+l.files[l.multi.ID][0].ID+".json" || ch.Items[0].Chapters.Type != "application/json+chapters" {
		t.Errorf("chapters link = %+v", ch.Items[0].Chapters)
	}
	if ch.Items[1].Chapters.URL != "" || ch.Items[2].Chapters.URL != "" {
		t.Error("a file with one chapter links chapters")
	}
	// A single-file book is one episode titled like the book, with its cover.
	tok2, _ := l.mk(t, "m_dl", l.single.ID, "")
	d2 := l.feedDoc(t, tok2)
	if !eq(feedTitles(d2), []string{"Single Tale"}) || !strings.HasPrefix(d2.Channel.Image.Href, "https://podcasts.example/feeds/"+tok2+"/cover/"+l.single.ID+".png") {
		t.Errorf("single = %v image %q", feedTitles(d2), d2.Channel.Image.Href)
	}
	// Without a trusted proxy the address is plain http and takes the Host header.
	d3 := l.feedDoc(t, tok2, func(r *http.Request) {
		r.RemoteAddr = "192.0.2.9:1"
		r.Header.Del("X-Forwarded-Proto")
		r.Host = "lan.test:8095"
	})
	if !strings.HasPrefix(d3.Channel.Items[0].Enclosure.URL, "http://lan.test:8095/feeds/") {
		t.Errorf("untrusted url = %q", d3.Channel.Items[0].Enclosure.URL)
	}
	// HEAD answers the headers only.
	head := l.a.do(http.MethodHead, "/feeds/"+tok+"/feed.xml", nil, proxyDefault)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Errorf("HEAD = %d, %d bytes", head.Code, head.Body.Len())
	}
}

func TestFeedXMLSeriesFiltersByGrant(t *testing.T) {
	l := newFeedLab(t)
	all := []string{"Multi Book · Opening", "Multi Book · Second", "Multi Book · Part 3 of 3", "Saga Two", "Saga Three"}
	cases := []struct {
		who  string
		want []string // nil means 404
	}{
		{"owner", all},
		{"m_dl", all},
		{"m_part", all[:4]},
		{"g_dl", all[:4]},
		{"m_nodl", nil},
	}
	for _, c := range cases {
		tok, _ := l.mk(t, c.who, "", l.seriesID)
		rec := l.get("/feeds/" + tok + "/feed.xml")
		if c.want == nil {
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: status %d", c.who, rec.Code)
			}
			continue
		}
		doc := l.feedDoc(t, tok)
		if !eq(feedTitles(doc), c.want) {
			t.Errorf("%s: titles %v, want %v", c.who, feedTitles(doc), c.want)
		}
		if doc.Channel.Title != "Saga" || doc.Channel.Author != "Ann Author, Bo Writer" {
			t.Errorf("%s: channel %q by %q", c.who, doc.Channel.Title, doc.Channel.Author)
		}
	}
	// Removing the grant empties the person's series feed on the next request.
	tok, _ := l.mk(t, "m_part", "", l.seriesID)
	l.exec(t, `DELETE FROM grants WHERE user_id = ?`, l.ids["m_part"])
	if rec := l.get("/feeds/" + tok + "/feed.xml"); rec.Code != http.StatusNotFound {
		t.Errorf("after grant removal: %d", rec.Code)
	}
}

func TestFeedCaps(t *testing.T) {
	l := newFeedLab(t)
	_, f1 := librarytest.NewLibrary(t, l.a.lib, "Many", adminAudioDir(t, true))
	for i := 0; i < feedMaxBooks+1; i++ {
		librarytest.AddBook(t, l.a.lib, f1, librarytest.BookSpec{Title: fmt.Sprintf("Many %03d", i), Files: 1, Series: "Many", Sequence: strconv.Itoa(i + 1)})
	}
	big := librarytest.AddBook(t, l.a.lib, f1, librarytest.BookSpec{Title: "Big", Files: feedMaxFiles + 1})
	var sid string
	if err := l.a.st.Reader().QueryRow(`SELECT id FROM series WHERE name = 'Many'`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	tok, _ := l.mk(t, "owner", "", sid)
	if n := len(l.feedDoc(t, tok).Channel.Items); n != feedMaxBooks {
		t.Errorf("series items = %d, want %d", n, feedMaxBooks)
	}
	tok, _ = l.mk(t, "owner", big.ID, "")
	if n := len(l.feedDoc(t, tok).Channel.Items); n != feedMaxFiles {
		t.Errorf("book items = %d, want %d", n, feedMaxFiles)
	}
}

// TestFeedPodcastAppExpectations reads a series feed the way a podcast app does: every enclosure has an address,
// a length and a type, guids are unique, durations are HH:MM:SS, and each enclosure address really serves that length.
func TestFeedPodcastAppExpectations(t *testing.T) {
	l := newFeedLab(t)
	tok, _ := l.mk(t, "m_dl", "", l.seriesID)
	doc := l.feedDoc(t, tok)
	if len(doc.Channel.Items) == 0 {
		t.Fatal("no items")
	}
	clock := regexp.MustCompile(`^\d{2,}:[0-5]\d:[0-5]\d$`)
	guids := map[string]bool{}
	for i, it := range doc.Channel.Items {
		e := it.Enclosure
		n, err := strconv.Atoi(e.Length)
		if e.URL == "" || err != nil || n <= 0 || e.Type == "" {
			t.Errorf("item %d enclosure incomplete: %+v", i, e)
		}
		if it.GUID.Value == "" || guids[it.GUID.Value] {
			t.Errorf("item %d guid %q empty or repeated", i, it.GUID.Value)
		}
		guids[it.GUID.Value] = true
		if !clock.MatchString(it.Duration) {
			t.Errorf("item %d duration %q", i, it.Duration)
		}
		if _, err := time.Parse(time.RFC1123Z, it.PubDate); err != nil {
			t.Errorf("item %d pubDate %q", i, it.PubDate)
		}
		if it.Title == "" {
			t.Errorf("item %d has no title", i)
		}
		u, err := url.Parse(e.URL)
		if err != nil || u.Scheme != "https" || u.Host != "podcasts.example" {
			t.Fatalf("item %d url %q", i, e.URL)
		}
		rec := l.get(u.Path)
		if rec.Code != http.StatusOK || rec.Body.Len() != n || rec.Header().Get("Content-Type") != e.Type {
			t.Errorf("item %d enclosure fetch = %d, %d bytes (want %d), type %q", i, rec.Code, rec.Body.Len(), n, rec.Header().Get("Content-Type"))
		}
	}
}

func TestFeedAudioRange(t *testing.T) {
	l := newFeedLab(t)
	tok, _ := l.mk(t, "m_part", l.multi.ID, "")
	f := l.files[l.multi.ID][1]
	path := "/feeds/" + tok + "/audio/" + f.ID + ".mp3"
	rec := l.get(path, func(r *http.Request) { r.Header.Set("Range", "bytes=5-14") })
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), l.body[f.ID][5:15]) {
		t.Fatalf("range = %d %q", rec.Code, rec.Body.String())
	}
	if cr := rec.Header().Get("Content-Range"); cr != fmt.Sprintf("bytes 5-14/%d", len(l.body[f.ID])) {
		t.Errorf("content-range = %q", cr)
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("cache-control = %q", rec.Header().Get("Cache-Control"))
	}
	full := l.get(path)
	if full.Code != http.StatusOK || !bytes.Equal(full.Body.Bytes(), l.body[f.ID]) {
		t.Errorf("full = %d", full.Code)
	}
	// The extension is for the app's sake; the id decides.
	if rec := l.get("/feeds/" + tok + "/audio/" + f.ID + ".whatever"); rec.Code != http.StatusOK {
		t.Errorf("other extension = %d", rec.Code)
	}
	head := l.a.do(http.MethodHead, path, nil, proxyDefault)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(len(l.body[f.ID])) {
		t.Errorf("HEAD = %d, len %q", head.Code, head.Header().Get("Content-Length"))
	}
	// A file of another book is not this feed's.
	other := l.files[l.single.ID][0]
	if rec := l.get("/feeds/" + tok + "/audio/" + other.ID + ".mp3"); rec.Code != http.StatusNotFound {
		t.Errorf("file of another book = %d", rec.Code)
	}
	// A series feed serves the series' books the person may download, and nothing else.
	stok, _ := l.mk(t, "m_part", "", l.seriesID)
	for name, want := range map[string]int{
		"sa": http.StatusOK, "sb": http.StatusNotFound, "refused": http.StatusNotFound, "tx": http.StatusNotFound, "single": http.StatusNotFound,
	} {
		b := map[string]library.Book{"sa": l.sa, "sb": l.sb, "refused": l.refused, "tx": l.tx, "single": l.single}[name]
		if rec := l.get("/feeds/" + stok + "/audio/" + l.files[b.ID][0].ID + ".mp3"); rec.Code != want {
			t.Errorf("series feed, file of %s = %d, want %d", name, rec.Code, want)
		}
	}
	// A copy-protected file is never served, even from its own book's feed.
	ptok, _ := l.mk(t, "owner", l.protected.ID, "")
	pf := l.files[l.protected.ID][0]
	if rec := l.get("/feeds/" + ptok + "/audio/" + pf.ID + ".aax"); rec.Code != http.StatusNotFound {
		t.Errorf("copy-protected = %d", rec.Code)
	}
	if rec := l.get("/feeds/" + ptok + "/feed.xml"); rec.Code != http.StatusNotFound {
		t.Errorf("feed of a copy-protected book = %d", rec.Code)
	}
	// A missing file: gone from disk, then marked missing.
	stok2, _ := l.mk(t, "owner", l.single.ID, "")
	sf := l.files[l.single.ID][0]
	if err := os.Remove(sf.Path); err != nil {
		t.Fatal(err)
	}
	if rec := l.get("/feeds/" + stok2 + "/audio/" + sf.ID + ".mp3"); rec.Code != http.StatusNotFound {
		t.Errorf("file gone from disk = %d", rec.Code)
	}
	l.exec(t, `UPDATE audio_files SET missing_since = 5 WHERE id = ?`, sf.ID)
	if rec := l.get("/feeds/" + stok2 + "/audio/" + sf.ID + ".mp3"); rec.Code != http.StatusNotFound {
		t.Errorf("file marked missing = %d", rec.Code)
	}
}

func TestFeedCoverAndChapters(t *testing.T) {
	l := newFeedLab(t)
	tok, _ := l.mk(t, "m_dl", "", l.seriesID)
	if rec := l.get("/feeds/" + tok + "/cover/" + l.multi.ID + ".jpg"); rec.Code != http.StatusNotFound {
		t.Errorf("book without cover = %d", rec.Code)
	}
	if rec := l.get("/feeds/" + tok + "/cover/" + l.single.ID + ".png"); rec.Code != http.StatusNotFound {
		t.Errorf("cover of a book outside the series = %d", rec.Code)
	}
	stok, _ := l.mk(t, "m_dl", l.single.ID, "")
	rec := l.get("/feeds/" + stok + "/cover/" + l.single.ID + ".png")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG")) {
		t.Errorf("cover = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := l.get("/feeds/" + stok + "/cover/" + l.multi.ID + ".png"); rec.Code != http.StatusNotFound {
		t.Errorf("cover of another book = %d", rec.Code)
	}
	btok, _ := l.mk(t, "m_dl", l.multi.ID, "")
	files := l.files[l.multi.ID]
	rec = l.get("/feeds/" + btok + "/chapters/" + files[0].ID + ".json")
	var doc struct {
		Version  string `json:"version"`
		Chapters []struct {
			StartTime float64 `json:"startTime"`
			Title     string  `json:"title"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json+chapters" {
		t.Fatalf("chapters = %d %q %v", rec.Code, rec.Body.String(), err)
	}
	if doc.Version != "1.2.0" || len(doc.Chapters) != 2 || doc.Chapters[0].Title != "Opening" || doc.Chapters[1].StartTime != 500 {
		t.Errorf("file 1 chapters = %+v", doc)
	}
	rec = l.get("/feeds/" + btok + "/chapters/" + files[1].ID + ".json")
	if !strings.Contains(rec.Body.String(), `"startTime":0.000`) || !strings.Contains(rec.Body.String(), `"Second"`) {
		t.Errorf("file 2 chapters = %s", rec.Body.String())
	}
	if rec := l.get("/feeds/" + btok + "/chapters/" + l.files[l.single.ID][0].ID + ".json"); rec.Code != http.StatusNotFound {
		t.Errorf("chapters of another book's file = %d", rec.Code)
	}
}

func TestFeedFailuresLookAlike(t *testing.T) {
	l := newFeedLab(t)
	unknown, _ := feedUnknownToken()
	cases := []struct {
		name    string
		path    func() string
		counted bool
	}{
		{"malformed", func() string { return "/feeds/not-a-token/feed.xml" }, true},
		{"unknown", func() string { return "/feeds/" + unknown + "/feed.xml" }, true},
		{"revoked", func() string {
			tok, f := l.mk(t, "m_dl", l.single.ID, "")
			l.exec(t, `UPDATE podcast_feeds SET revoked_at = 5 WHERE id = ?`, f.ID)
			return "/feeds/" + tok + "/feed.xml"
		}, false},
		{"owner disabled", func() string {
			tok, _ := l.mk(t, "m_part", l.single.ID, "")
			l.exec(t, `UPDATE users SET disabled = 1 WHERE id = ?`, l.ids["m_part"])
			return "/feeds/" + tok + "/feed.xml"
		}, false},
		{"no download right", func() string {
			tok, _ := l.mk(t, "m_nodl", l.single.ID, "")
			return "/feeds/" + tok + "/feed.xml"
		}, false},
		{"grant removed", func() string {
			tok, _ := l.mk(t, "g_dl", l.single.ID, "")
			l.exec(t, `DELETE FROM grants WHERE user_id = ?`, l.ids["g_dl"])
			return "/feeds/" + tok + "/feed.xml"
		}, false},
		{"book deleted", func() string {
			tok, _ := l.mk(t, "owner", l.sa.ID, "")
			l.exec(t, `DELETE FROM books WHERE id = ?`, l.sa.ID)
			return "/feeds/" + tok + "/feed.xml"
		}, false},
		{"unknown route", func() string { tok, _ := l.mk(t, "owner", l.single.ID, ""); return "/feeds/" + tok + "/other" }, false},
	}
	var status int
	var body string
	var hdr string
	for i, c := range cases {
		rec := l.get(c.path())
		if i == 0 {
			status, body, hdr = rec.Code, rec.Body.String(), rec.Header().Get("Referrer-Policy")
		}
		if rec.Code != http.StatusNotFound || rec.Body.String() != "Not found.\n" || rec.Code != status || rec.Body.String() != body ||
			!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || rec.Header().Get("Referrer-Policy") != hdr {
			t.Errorf("%s: %d %q (%q)", c.name, rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
		}
	}
	// Only the two guesses wrote an audit row, each with the address and a reason, never a token.
	rows, err := l.a.st.Reader().Query(`SELECT action, coalesce(user_id, ''), target, detail_json FROM audit_log WHERE action LIKE 'feed.%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var a, u, tg, d string
		if err := rows.Scan(&a, &u, &tg, &d); err != nil {
			t.Fatal(err)
		}
		if u != "" || tg != "" || (!strings.Contains(d, `"reason":"malformed"`) && !strings.Contains(d, `"reason":"unknown"`)) || strings.Contains(d, unknown) {
			t.Errorf("audit %s %q %q %s", a, u, tg, d)
		}
		got = append(got, a)
	}
	if !eq(got, []string{"feed.failed", "feed.failed"}) {
		t.Errorf("audit rows = %v", got)
	}
}

func feedUnknownToken() (string, error) {
	return strings.Repeat("A", 43), nil
}

func TestFeedLimiter(t *testing.T) {
	l := newFeedLab(t)
	good, _ := l.mk(t, "owner", l.single.ID, "")
	same := func(r *http.Request) { r.Header.Set("X-Forwarded-For", "198.51.100.77") }
	for i := 0; i < keyFailMax; i++ {
		if rec := l.get("/feeds/"+strings.Repeat("B", 43)+"/feed.xml", same); rec.Code != http.StatusNotFound {
			t.Fatalf("guess %d = %d", i, rec.Code)
		}
	}
	// Limited before the database is read: even a valid token from this address waits, and no audit row is added.
	var before int
	l.a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log`).Scan(&before)
	rec := l.get("/feeds/"+good+"/feed.xml", same)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || rec.Body.String() != "Too many tries. Wait a little and try again.\n" {
		t.Errorf("limited = %d %q retry %q", rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"))
	}
	if rec := l.get("/feeds/"+strings.Repeat("C", 43)+"/feed.xml", same); rec.Code != http.StatusTooManyRequests {
		t.Errorf("guess while limited = %d", rec.Code)
	}
	var after int
	l.a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log`).Scan(&after)
	if after != before {
		t.Errorf("limited requests wrote %d audit rows", after-before)
	}
	// Another address is not affected.
	if rec := l.get("/feeds/" + good + "/feed.xml"); rec.Code != http.StatusOK {
		t.Errorf("other address = %d", rec.Code)
	}
	// An app still polling a revoked feed is never limited.
	dead, f := l.mk(t, "owner", l.single.ID, "")
	l.exec(t, `UPDATE podcast_feeds SET revoked_at = 5 WHERE id = ?`, f.ID)
	other := func(r *http.Request) { r.Header.Set("X-Forwarded-For", "198.51.100.88") }
	for i := 0; i < 20; i++ {
		if rec := l.get("/feeds/"+dead+"/feed.xml", other); rec.Code != http.StatusNotFound {
			t.Fatalf("revoked poll %d = %d", i, rec.Code)
		}
	}
}

func TestFeedPerFeedLimit(t *testing.T) {
	l := newFeedLab(t)
	tok, _ := l.mk(t, "owner", l.single.ID, "")
	var last *httptest.ResponseRecorder
	for i := 0; i <= apiRateMax; i++ {
		last = l.get("/feeds/" + tok + "/feed.xml")
		if last.Code != http.StatusOK {
			break
		}
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("Retry-After") == "" ||
		last.Body.String() != "Too many requests from this app. Wait a little and try again.\n" {
		t.Errorf("per-feed limit = %d %q", last.Code, last.Body.String())
	}
	tok2, _ := l.mk(t, "owner", l.single.ID, "")
	if rec := l.get("/feeds/" + tok2 + "/feed.xml"); rec.Code != http.StatusOK {
		t.Errorf("another feed = %d", rec.Code)
	}
}

func TestFeedTokenOpensNothingElse(t *testing.T) {
	l := newFeedLab(t)
	tok, _ := l.mk(t, "owner", l.single.ID, "")
	fileID := l.files[l.single.ID][0].ID
	paths := []string{"/", "/books/" + l.single.ID, "/stream/" + fileID, "/me/devices", "/hx/player/" + l.single.ID, "/api/v1/books",
		"/opds", "/opds/all", "/covers/" + l.single.ID, "/admin", "/hx/jobs/x"}
	ways := map[string]func(*http.Request){
		"cookie":  func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "ts_session", Value: tok}) },
		"bearer":  func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) },
		"basic":   func(r *http.Request) { r.SetBasicAuth("owner", tok) },
		"query":   func(r *http.Request) { r.URL.RawQuery = "token=" + tok },
		"header":  func(r *http.Request) { r.Header.Set("X-Api-Key", tok) },
		"cookie2": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "ts_link", Value: tok}) },
	}
	for _, p := range paths {
		for name, mod := range ways {
			rec := l.get(p, mod)
			if rec.Code == http.StatusOK {
				t.Errorf("%s with token as %s = 200", p, name)
			}
		}
	}
	// Methods other than GET and HEAD on a feed path never succeed.
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := l.a.do(m, "/feeds/"+tok+"/feed.xml", url.Values{"x": {"y"}}, proxyDefault)
		if rec.Code == http.StatusOK {
			t.Errorf("%s feed.xml = 200", m)
		}
	}
	// A real session on a feed path with a bad token still gets the plain 404.
	rec := l.get("/feeds/"+strings.Repeat("D", 43)+"/feed.xml", func(r *http.Request) { r.AddCookie(l.owner) })
	if rec.Code != http.StatusNotFound || rec.Body.String() != "Not found.\n" {
		t.Errorf("session cookie on a bad token = %d %q", rec.Code, rec.Body.String())
	}
	// The feed does not read the session: a good token with someone else's cookie is the token's owner's feed.
	other := l.a.as(t, users.RoleMember)
	doc := l.feedDoc(t, tok, func(r *http.Request) { r.AddCookie(other) })
	if !eq(feedTitles(doc), []string{"Single Tale"}) {
		t.Errorf("titles = %v", feedTitles(doc))
	}
}

func TestFeedNoCookieSet(t *testing.T) {
	l := newFeedLab(t)
	tok, _ := l.mk(t, "owner", l.multi.ID, "")
	f := l.files[l.multi.ID][0].ID
	for _, p := range []string{"/feeds/" + tok + "/feed.xml", "/feeds/" + tok + "/audio/" + f + ".mp3", "/feeds/" + tok + "/chapters/" + f + ".json",
		"/feeds/" + tok + "/cover/" + l.multi.ID + ".jpg", "/feeds/" + tok + "/nothing", "/feeds/", "/feeds/bad/feed.xml"} {
		for _, mod := range []func(*http.Request){nil, func(r *http.Request) { r.AddCookie(l.owner) }} {
			var m []func(*http.Request)
			if mod != nil {
				m = append(m, mod)
			}
			if rec := l.get(p, m...); len(rec.Result().Cookies()) != 0 || rec.Header().Get("Set-Cookie") != "" {
				t.Errorf("%s set a cookie: %v", p, rec.Header()["Set-Cookie"])
			}
		}
	}
}

func TestFeedUncleanPathsDoNotRedirect(t *testing.T) {
	l := newFeedLab(t)
	tok, _ := l.mk(t, "owner", l.single.ID, "")
	for _, p := range []string{"/feeds//" + tok + "/feed.xml", "/feeds/" + tok + "//feed.xml", "/feeds/" + tok + "/../" + tok + "/feed.xml", "/feeds/" + tok + "/./feed.xml", "/feeds/" + tok} {
		rec := l.get(p)
		if rec.Header().Get("Location") != "" || strings.Contains(rec.Body.String(), tok) || rec.Code == http.StatusOK {
			t.Errorf("%s = %d, location %q", p, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestFeedTouchOncePerHour(t *testing.T) {
	l := newFeedLab(t)
	tok, f := l.mk(t, "owner", l.single.ID, "")
	used := func() int64 {
		var n sql.NullInt64
		if err := l.a.st.Reader().QueryRow(`SELECT last_used_at FROM podcast_feeds WHERE id = ?`, f.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n.Int64
	}
	if used() != 0 {
		t.Fatal("used before any request")
	}
	l.get("/feeds/" + tok + "/feed.xml")
	first := used()
	if first != l.clock.UnixMilli() {
		t.Fatalf("first use = %d", first)
	}
	*l.clock = l.clock.Add(30 * time.Minute)
	l.get("/feeds/" + tok + "/feed.xml")
	if used() != first {
		t.Error("written again inside the hour")
	}
	*l.clock = l.clock.Add(31 * time.Minute)
	l.get("/feeds/" + tok + "/feed.xml")
	if used() != l.clock.UnixMilli() {
		t.Error("not written after the hour")
	}
}

func TestFeedsNotInServiceWorker(t *testing.T) {
	src, err := os.ReadFile("static/js/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "feeds") {
		t.Error("the service worker names /feeds/")
	}
}

func TestFeedTokenNotLogged(t *testing.T) {
	l := newFeedLab(t)
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	tok, _ := l.mk(t, "owner", l.single.ID, "")
	f := l.files[l.single.ID][0]
	if err := os.Remove(f.Path); err != nil { // the open failure is logged
		t.Fatal(err)
	}
	for _, p := range []string{"/feeds/" + tok + "/feed.xml", "/feeds/" + tok + "/audio/" + f.ID + ".mp3", "/feeds/" + tok + "/x", "/feeds/" + strings.Repeat("E", 43) + "/feed.xml"} {
		l.get(p)
	}
	if strings.Contains(logs.String(), tok) {
		t.Errorf("token in the log:\n%s", logs.String())
	}
	if got := logPath(httptest.NewRequest(http.MethodGet, "/feeds/"+tok+"/feed.xml", nil)); strings.Contains(got, tok) || !strings.HasPrefix(got, "/feeds/") {
		t.Errorf("logPath = %q", got)
	}
	// Nor in any audit row.
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE detail_json LIKE '%' || ? || '%' OR target LIKE '%' || ? || '%'`, tok, tok).Scan(&n); err != nil || n != 0 {
		t.Errorf("audit rows holding the token: %d, %v", n, err)
	}
}
