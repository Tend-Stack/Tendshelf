package rss

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const feedURL = "https://example.org/dir/feed.xml"

func wrap(items string) []byte {
	return []byte(`<?xml version="1.0"?><rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel><title>T</title>` + items + `</channel></rss>`)
}

func item(extra string) string {
	return `<item><title>E</title>` + extra + `<enclosure url="a.mp3" type="audio/mpeg"/></item>`
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestParseITunesFull(t *testing.T) {
	b, err := os.ReadFile("testdata/itunes.xml")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(b, feedURL)
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "The Test Show" || f.Author != "Test Author" || !f.Explicit || f.Language != "en-us" ||
		f.Link != "https://example.org/show" || f.ImageURL != "https://example.org/art/show.jpg" ||
		f.NewFeedURL != "https://example.org/new.xml" || f.Description != "A <b>show</b> about tests." {
		t.Fatalf("channel %+v", f)
	}
	if len(f.Episodes) != 2 {
		t.Fatalf("episodes %d", len(f.Episodes))
	}
	e := f.Episodes[0]
	want := Item{
		GUID: "ep-2", GUIDKey: sum("ep-2"), Title: "Episode 2", Link: "https://example.org/ep2",
		NotesHTML: `<p>Full <a href="https://example.org/x" rel="nofollow noopener noreferrer ugc">notes</a>.</p>`,
		AudioURL:  "https://example.org/audio/ep2.mp3", AudioType: "audio/mpeg", AudioBytes: 12345,
		DurationMs: (3600 + 2*60 + 3) * 1000, PublishedAt: time.Date(2023, 1, 3, 10, 0, 0, 0, time.UTC).UnixMilli(),
		Season: 2, Number: 7, Kind: "bonus", ChaptersURL: "https://example.org/chapters/ep2.json", ImageURL: "https://example.org/ep2.jpg",
	}
	if e != want {
		t.Errorf("episode 0\n got %+v\nwant %+v", e, want)
	}
	e = f.Episodes[1]
	if e.Kind != "trailer" || e.DurationMs != 754000 || e.AudioURL != "https://cdn.example.net/ep1.m4a" ||
		e.PublishedAt != time.Date(2023, 1, 2, 9, 0, 0, 0, time.UTC).UnixMilli() {
		t.Errorf("episode 1 %+v", e)
	}
}

func TestParseAtom(t *testing.T) {
	b, _ := os.ReadFile("testdata/atom.xml")
	f, err := Parse(b, feedURL)
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Atom Show" || f.Author != "Ann Atom" || f.Link != "https://example.org/" || f.Language != "de" ||
		f.ImageURL != "https://example.org/logo.png" || f.Description != "<p>Plain subtitle</p>" {
		t.Fatalf("feed %+v", f)
	}
	if len(f.Episodes) != 1 {
		t.Fatalf("episodes %+v", f.Episodes)
	}
	e := f.Episodes[0]
	if e.Title != "First & best" || e.GUID != "urn:uuid:1" || e.AudioURL != "https://example.org/dir/media/e1.mp3" ||
		e.AudioBytes != 500 || e.Link != "https://example.org/e1" || e.NotesHTML != "<p>Summary one</p>" ||
		e.PublishedAt != time.Date(2023, 5, 6, 7, 8, 9, 0, time.UTC).UnixMilli() {
		t.Errorf("entry %+v", e)
	}
	// No xmlns at all still reads as Atom.
	f, err = Parse([]byte(`<feed><title>Bare</title><entry><title>x</title><id>1</id><link rel="enclosure" href="http://e.example/a.mp3" type="audio/mpeg"/></entry></feed>`), feedURL)
	if err != nil || f.Title != "Bare" || len(f.Episodes) != 1 {
		t.Errorf("bare atom: %v %+v", err, f)
	}
}

func TestParseRules(t *testing.T) {
	cases := []struct {
		name  string
		body  []byte
		check func(t *testing.T, f Feed)
	}{
		{"minimal", wrap(item(`<guid>g1</guid>`)), func(t *testing.T, f Feed) {
			e := f.Episodes[0]
			if len(f.Episodes) != 1 || e.AudioURL != "https://example.org/dir/a.mp3" || e.Kind != "full" || e.PublishedAt != 0 || e.DurationMs != 0 {
				t.Errorf("%+v", f.Episodes)
			}
		}},
		{"no guid uses audio url", wrap(item(``)), func(t *testing.T, f Feed) {
			if e := f.Episodes[0]; e.GUID != "" || e.GUIDKey != sum("https://example.org/dir/a.mp3") {
				t.Errorf("%+v", e)
			}
		}},
		{"guid trimmed before hashing", wrap(item(`<guid>  g1
</guid>`)), func(t *testing.T, f Feed) {
			if f.Episodes[0].GUIDKey != sum("g1") {
				t.Errorf("%+v", f.Episodes[0])
			}
		}},
		{"duplicate guid keeps first", wrap(
			`<item><title>first</title><guid>d</guid><enclosure url="1.mp3" type="audio/mpeg"/></item>` +
				`<item><title>second</title><guid>d</guid><enclosure url="2.mp3" type="audio/mpeg"/></item>`), func(t *testing.T, f Feed) {
			if len(f.Episodes) != 1 || f.Episodes[0].Title != "first" {
				t.Errorf("%+v", f.Episodes)
			}
		}},
		{"relative and absolute enclosure", wrap(
			`<item><guid>a</guid><enclosure url="../up.mp3" type="audio/mpeg"/></item>` +
				`<item><guid>b</guid><enclosure url="//cdn.example.net/x.mp3" type="audio/mpeg"/></item>`), func(t *testing.T, f Feed) {
			if f.Episodes[0].AudioURL != "https://example.org/up.mp3" || f.Episodes[1].AudioURL != "https://cdn.example.net/x.mp3" {
				t.Errorf("%+v", f.Episodes)
			}
		}},
		{"no audio dropped, video kept, non-http dropped", wrap(
			`<item><guid>a</guid><title>text only</title></item>` +
				`<item><guid>b</guid><enclosure url="v.mp4" type="video/mp4"/></item>` +
				`<item><guid>c</guid><enclosure url="file:///etc/passwd" type="audio/mpeg"/></item>` +
				`<item><guid>d</guid><enclosure url="pic.jpg" type="image/jpeg"/></item>` +
				`<item><guid>e</guid><enclosure url="v.mp4" type="video/mp4"/><enclosure url="a.mp3" type="audio/mpeg"/></item>` +
				`<item><guid>f</guid><enclosure url="noext" /></item>` +
				`<item><guid>g</guid><enclosure url="x.opus" /></item>`), func(t *testing.T, f Feed) {
			var got []string
			for _, e := range f.Episodes {
				got = append(got, e.GUID+":"+e.AudioType)
			}
			if strings.Join(got, ",") != "b:video/mp4,e:audio/mpeg,g:" {
				t.Errorf("%v", got)
			}
		}},
		{"malformed and odd dates", wrap(
			`<item><guid>a</guid><pubDate>not a date</pubDate><enclosure url="a.mp3" type="audio/mpeg"/></item>` +
				`<item><guid>b</guid><pubDate>Mon, 2 Jan 2006 15:04:05 -0700</pubDate><enclosure url="a.mp3" type="audio/mpeg"/></item>` +
				`<item><guid>c</guid><pubDate>02 Jan 06 15:04 MST</pubDate><enclosure url="a.mp3" type="audio/mpeg"/></item>` +
				`<item><guid>d</guid><pubDate>2006-01-02T15:04:05+02:00</pubDate><enclosure url="a.mp3" type="audio/mpeg"/></item>` +
				`<item><guid>e</guid><pubDate>Thu, 01 Jan 1970 00:00:00 +0000</pubDate><enclosure url="a.mp3" type="audio/mpeg"/></item>` +
				`<item><guid>f</guid><pubDate></pubDate><enclosure url="a.mp3" type="audio/mpeg"/></item>`), func(t *testing.T, f Feed) {
			d := func(h int, off int) int64 {
				return time.Date(2006, 1, 2, h, 4, 5, 0, time.FixedZone("", off)).UnixMilli()
			}
			ps := []int64{}
			for _, e := range f.Episodes {
				ps = append(ps, e.PublishedAt)
			}
			want := []int64{0, d(15, -7*3600), time.Date(2006, 1, 2, 15, 4, 0, 0, time.UTC).UnixMilli(), d(15, 2*3600), 0, 0}
			// 02 Jan 06 15:04 MST: an unknown zone name reads as UTC.
			if len(ps) != 6 || ps[0] != want[0] || ps[1] != want[1] || ps[3] != want[3] || ps[4] != 0 || ps[5] != 0 || ps[2] == 0 {
				t.Errorf("%v want %v", ps, want)
			}
		}},
		{"durations", wrap(
			item(`<guid>a</guid><itunes:duration>90</itunes:duration>`) +
				item(`<guid>b</guid><itunes:duration>12:30</itunes:duration>`) +
				item(`<guid>c</guid><itunes:duration>01:02:03</itunes:duration>`) +
				item(`<guid>d</guid><itunes:duration>bad</itunes:duration>`) +
				item(`<guid>e</guid><itunes:duration>1:2:3:4</itunes:duration>`) +
				item(`<guid>f</guid><itunes:duration>12.5</itunes:duration>`)), func(t *testing.T, f Feed) {
			var got []int64
			for _, e := range f.Episodes {
				got = append(got, e.DurationMs)
			}
			if fmt.Sprint(got) != "[90000 750000 3723000 0 0 12500]" {
				t.Errorf("%v", got)
			}
		}},
		{"entities in titles", wrap(`<item><guid>a</guid><title>Caf&eacute; &amp; &copy; &#169; &nbsp;x</title><enclosure url="a.mp3" type="audio/mpeg"/></item>`), func(t *testing.T, f Feed) {
			if got := f.Episodes[0].Title; got != "Café & © © x" {
				t.Errorf("%q", got)
			}
		}},
		{"scripts and handlers in notes", wrap(item(
			`<guid>a</guid><description><![CDATA[<p onclick="evil()">Hi<script>alert(1)</script> <a href="javascript:alert(1)">bad</a> <a href="https://ok.example/?a=1&b=2" onclick="x">good</a> <img src=x onerror=y><iframe src=z></iframe></p>]]></description>`)), func(t *testing.T, f Feed) {
			n := f.Episodes[0].NotesHTML
			for _, bad := range []string{"script", "onclick", "javascript", "onerror", "<img", "iframe", "alert"} {
				if strings.Contains(n, bad) {
					t.Errorf("%q survives in %s", bad, n)
				}
			}
			if !strings.Contains(n, `<a href="https://ok.example/?a=1&amp;b=2" rel="nofollow noopener noreferrer ugc">good</a>`) || !strings.Contains(n, "bad") {
				t.Errorf("%s", n)
			}
		}},
		{"long text cut", wrap(`<item><guid>a</guid><title>` + strings.Repeat("é", 400) + `</title><enclosure url="a.mp3" type="audio/mpeg"/></item>`), func(t *testing.T, f Feed) {
			if n := len([]rune(f.Episodes[0].Title)); n != 300 {
				t.Errorf("title runes %d", n)
			}
		}},
		{"unknown episode type and giant season", wrap(item(`<guid>a</guid><itunes:episodeType>weird</itunes:episodeType><itunes:season>99999999999</itunes:season>`)), func(t *testing.T, f Feed) {
			if e := f.Episodes[0]; e.Kind != "full" || e.Season != 0 {
				t.Errorf("%+v", e)
			}
		}},
		{"chapters of another type ignored", wrap(item(`<guid>a</guid><podcast:chapters xmlns:podcast="https://podcastindex.org/namespace/1.0" url="c.xml" type="text/xml"/>`)), func(t *testing.T, f Feed) {
			if f.Episodes[0].ChaptersURL != "" {
				t.Errorf("%+v", f.Episodes[0])
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := Parse(c.body, feedURL)
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, f)
		})
	}
}

func TestParseWindows1252(t *testing.T) {
	body := []byte("<?xml version=\"1.0\" encoding=\"windows-1252\"?><rss><channel><title>Caf\xe9 \x93quoted\x94</title>" +
		"<item><title>\xe9t\xe9</title><enclosure url=\"http://e.example/a.mp3\" type=\"audio/mpeg\"/></item></channel></rss>")
	f, err := Parse(body, feedURL)
	if err != nil || f.Title != "Café “quoted”" || f.Episodes[0].Title != "été" {
		t.Fatalf("%v %+v", err, f)
	}
}

func TestParseCutsToNewest3000(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 3500; i++ {
		fmt.Fprintf(&b, `<item><guid>g%d</guid><pubDate>%s</pubDate><enclosure url="a%d.mp3" type="audio/mpeg"/></item>`,
			i, time.Unix(int64(1_600_000_000+i*3600), 0).UTC().Format(time.RFC1123Z), i)
	}
	f, err := Parse(wrap(b.String()), feedURL)
	if err != nil || len(f.Episodes) != 3000 {
		t.Fatalf("%v %d", err, len(f.Episodes))
	}
	if f.Episodes[0].GUID != "g3499" || f.Episodes[2999].GUID != "g500" {
		t.Errorf("%s ... %s", f.Episodes[0].GUID, f.Episodes[2999].GUID)
	}
}

func TestParseNotAFeed(t *testing.T) {
	for name, body := range map[string]string{
		"empty": "", "spaces": "   \n", "html": "<html><body>hi</body></html>", "json": `{"a":1}`,
		"no channel": "<rss></rss>", "text": "hello",
	} {
		if _, err := Parse([]byte(body), feedURL); !errors.Is(err, ErrNotFeed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseTruncatedKeepsWhatWasRead(t *testing.T) {
	body := `<rss><channel><title>T</title><item><title>one</title><enclosure url="http://e.example/a.mp3" type="audio/mpeg"/></item><item><title>tw`
	f, err := Parse([]byte(body), feedURL)
	if err != nil || f.Title != "T" || len(f.Episodes) < 1 {
		t.Fatalf("%v %+v", err, f)
	}
}

func TestParseHostileDepth(t *testing.T) {
	body := "<rss><channel>" + strings.Repeat("<a>", 100000)
	if _, err := Parse([]byte(body), feedURL); err != nil {
		// A broken feed may be refused or kept; it must not hang or crash.
		t.Log(err)
	}
}
