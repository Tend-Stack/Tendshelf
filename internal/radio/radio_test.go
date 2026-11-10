package radio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
	"github.com/Tend-Stack/tendshelf/internal/podcast/podtest"
)

func block(text string) []byte {
	n := (len(text) + 15) / 16
	b := make([]byte, 1+n*16)
	b[0] = byte(n)
	copy(b[1:], text)
	return b
}

// stream builds audio in chunks of metaint bytes, each followed by the given block (nil means a zero-length one).
func stream(metaint int, blocks ...[]byte) (all []byte, audio []byte) {
	for i, bl := range blocks {
		chunk := bytes.Repeat([]byte{byte('a' + i)}, metaint)
		audio = append(audio, chunk...)
		all = append(all, chunk...)
		if bl == nil {
			bl = []byte{0}
		}
		all = append(all, bl...)
	}
	return
}

func TestSplitter(t *testing.T) {
	tests := []struct {
		name   string
		blocks [][]byte
		titles []string
	}{
		{"title then none", [][]byte{block("StreamTitle='Ann - Song';"), nil}, []string{"Ann - Song"}},
		{"apostrophe inside", [][]byte{block("StreamTitle='Rock 'n' Roll';")}, []string{"Rock 'n' Roll"}},
		{"junk block changes nothing", [][]byte{block("hello world"), block("StreamUrl='x';")}, nil},
		{"empty title clears", [][]byte{block("StreamTitle='A';"), block("StreamTitle='';")}, []string{"A", ""}},
		{"no closing quote", [][]byte{block("StreamTitle='Cut off")}, []string{"Cut off"}},
		{"latin1", [][]byte{block("StreamTitle='Caf\xe9 del Mar';")}, []string{"Café del Mar"}},
		{"oversized is cut", [][]byte{block("StreamTitle='" + strings.Repeat("é", 400) + "';")}, []string{strings.Repeat("é", MaxTitleRunes)}},
		{"controls dropped", [][]byte{block("StreamTitle='a\x01\x1b[0m\tb';")}, []string{"a[0m b"}},
		{"largest block", [][]byte{append([]byte{255}, append([]byte("StreamTitle='Big';"), make([]byte, 255*16-18)...)...)}, []string{"Big"}},
	}
	for _, tc := range tests {
		for _, readSize := range []int{1, 7, 4096} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, readSize), func(t *testing.T) {
				all, audio := stream(32, tc.blocks...)
				var got []string
				sp := &Splitter{R: bytes.NewReader(all), Metaint: 32, OnTitle: func(s string) { got = append(got, s) }}
				var out bytes.Buffer
				buf := make([]byte, readSize)
				for {
					n, err := sp.Read(buf)
					out.Write(buf[:n])
					if err != nil {
						if err != io.EOF {
							t.Fatalf("err %v", err)
						}
						break
					}
				}
				if !bytes.Equal(out.Bytes(), audio) {
					t.Errorf("audio differs: got %d bytes, want %d", out.Len(), len(audio))
				}
				if strings.Join(got, "|") != strings.Join(tc.titles, "|") {
					t.Errorf("titles %q, want %q", got, tc.titles)
				}
			})
		}
	}
}

func TestSplitterBoundaries(t *testing.T) {
	// A stream that ends inside a block is an error, not audio; metaint 0 passes bytes through untouched.
	all, _ := stream(8, block("StreamTitle='X';"))
	sp := &Splitter{R: bytes.NewReader(all[:12]), Metaint: 8}
	if _, err := io.ReadAll(sp); err == nil || !errors.Is(err, io.ErrUnexpectedEOF) && err != io.EOF {
		t.Errorf("truncated block: %v", err)
	}
	raw := []byte("no metadata here StreamTitle='x';")
	got, _ := io.ReadAll(&Splitter{R: bytes.NewReader(raw)})
	if !bytes.Equal(got, raw) {
		t.Errorf("metaint 0 changed the stream")
	}
}

func TestCleanTitleLimit(t *testing.T) {
	if n := len([]rune(CleanTitle(strings.Repeat("a b ", 200)))); n > MaxTitleRunes {
		t.Errorf("%d runes", n)
	}
	if got := CleanTitle("  a \n\n b  "); got != "a b" {
		t.Errorf("%q", got)
	}
}

func TestFirstEntry(t *testing.T) {
	tests := []struct {
		name, in, want string
		hls            bool
	}{
		{"pls", "[playlist]\nNumberOfEntries=2\nFile1=http://a.example:8000/live\nTitle1=X\nFile2=http://b.example/x\n", "http://a.example:8000/live", false},
		{"m3u", "#EXTM3U\n#EXTINF:-1,Name\nhttps://a.example/live.mp3\n", "https://a.example/live.mp3", false},
		{"relative only", "#EXTM3U\nlive.mp3\n/x\n", "", false},
		{"non http skipped", "file:///etc/passwd\nftp://a.example/x\nhttp://ok.example/s\n", "http://ok.example/s", false},
		{"hls", "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhttps://a.example/low.m3u8\n", "", true},
		{"bom", "\ufeff#EXTM3U\nhttp://a.example/s\n", "http://a.example/s", false},
		{"empty", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, hls := FirstEntry(tc.in)
			if got != tc.want || hls != tc.hls {
				t.Errorf("got %q hls=%v", got, hls)
			}
		})
	}
}

func TestStreamType(t *testing.T) {
	for ct, want := range map[string]bool{
		"audio/mpeg": true, "AUDIO/MPEG; charset=x": true, "audio/aac": true, "audio/aacp": true, "audio/ogg": true, "audio/opus": true, "audio/flac": true,
		"text/html": false, "image/svg+xml": false, "audio/x-mpegurl": false, "video/mp4": false, "application/vnd.apple.mpegurl": false,
		"audio/mp4": false, "": false, "application/octet-stream": false, "audio/mpeg\x00": false,
	} {
		if _, _, ok := StreamType(ct); ok != want {
			t.Errorf("%q: ok=%v, want %v", ct, ok, want)
		}
	}
}

type fixture struct {
	pub *podtest.Publisher
	svc *Service
	st  *db.Store
	h   func(w http.ResponseWriter, r *http.Request)
}

func newFixture(t *testing.T, h http.HandlerFunc) *fixture {
	t.Helper()
	st, err := db.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, u := range []string{"us_a", "us_b"} {
		if _, err := st.Writer().Exec(`INSERT INTO users(id, username, role, created_at) VALUES (?, ?, 'member', 1)`, u, u); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{st: st}
	f.pub = podtest.New(t, h)
	f.svc = New(st, f.pub.Client, nil, nil, nil)
	return f
}

func audioHandler(ct string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(200)
		io.WriteString(w, "audio")
	}
}

func TestProbe(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.mp3":
			audioHandler("audio/mpeg")(w, r)
		case "/html":
			audioHandler("text/html")(w, r)
		case "/svg":
			audioHandler("image/svg+xml")(w, r)
		case "/radio.pls":
			w.Header().Set("Content-Type", "audio/x-scpls")
			fmt.Fprintf(w, "[playlist]\nFile1=http://s.test:%s/live.mp3\n", r.Host[strings.LastIndex(r.Host, ":")+1:])
		case "/loop.m3u":
			w.Header().Set("Content-Type", "audio/x-mpegurl")
			fmt.Fprintf(w, "#EXTM3U\nhttp://s.test:%s/loop.m3u\n", r.Host[strings.LastIndex(r.Host, ":")+1:])
		case "/hls":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\nseg.ts\n")
		case "/hlsplain.m3u":
			w.Header().Set("Content-Type", "audio/x-mpegurl")
			io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhttp://s.test/low.m3u8\n")
		case "/empty.m3u":
			w.Header().Set("Content-Type", "audio/x-mpegurl")
			io.WriteString(w, "#EXTM3U\nrelative.mp3\n")
		case "/gone":
			http.NotFound(w, r)
		case "/redir":
			http.Redirect(w, r, "http://127.0.0.1:9/x", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	})
	f.pub.Resolve["private.test"] = "127.0.0.1"
	f.pub.Resolve["meta.test"] = "169.254.169.254"
	// The redirect target is a literal loopback address; the guarded client refuses it at dial time.
	cases := []struct {
		name, url string
		wantErr   error
		wantCode  string
		wantURL   string
		wantCodec string
	}{
		{"mp3", f.pub.URL("s.test", "/live.mp3"), nil, "", f.pub.URL("s.test", "/live.mp3"), "mp3"},
		{"pls entry rechecked", f.pub.URL("s.test", "/radio.pls"), nil, "", f.pub.URL("s.test", "/live.mp3"), "mp3"},
		{"html refused", f.pub.URL("s.test", "/html"), ErrNotAudio, "", "", ""},
		{"svg refused", f.pub.URL("s.test", "/svg"), ErrNotAudio, "", "", ""},
		{"m3u8 path is probed as HLS", f.pub.URL("s.test", "/a.m3u8"), nil, "not_found", "", ""},
		{"hls content type without a target duration", f.pub.URL("s.test", "/hls"), ErrHLS, "", "", ""},
		{"playlist without entry", f.pub.URL("s.test", "/empty.m3u"), ErrNoEntry, "", "", ""},
		{"playlist loop stops", f.pub.URL("s.test", "/loop.m3u"), ErrNoEntry, "", "", ""},
		{"private dns refused", f.pub.URL("private.test", "/live.mp3"), nil, "refused", "", ""},
		{"metadata address refused", f.pub.URL("meta.test", "/live.mp3"), nil, "refused", "", ""},
		{"redirect to private refused", f.pub.URL("s.test", "/redir"), nil, "refused", "", ""},
		{"literal loopback refused", "http://127.0.0.1:8095/live.mp3", nil, "refused", "", ""},
		{"not found", f.pub.URL("s.test", "/gone"), nil, "not_found", "", ""},
		{"ftp refused", "ftp://s.test/live.mp3", ErrBadURL, "", "", ""},
		{"user info refused", "http://u:p@s.test/live.mp3", ErrBadURL, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := f.svc.Probe(ctx, tc.url)
			switch {
			case tc.wantCode != "":
				var pe *podnet.Error
				if !errors.As(err, &pe) || pe.Code != tc.wantCode {
					t.Fatalf("err = %v, want code %s", err, tc.wantCode)
				}
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if p.StreamURL != tc.wantURL || p.Codec != tc.wantCodec {
					t.Errorf("got %+v", p)
				}
			}
		})
	}
}

func TestHLSText(t *testing.T) {
	if ErrHLS.Text != "Tendshelf can't play this kind of stream yet." {
		t.Errorf("%q", ErrHLS.Text)
	}
}

func TestStore(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, audioHandler("audio/mpeg"))
	s := f.svc.Store
	in := Input{StreamURL: "http://Radio.Example:80/live?x=1#frag", Name: " Cool \n FM ", Slug: "cool-fm", Genre: "Pop"}
	a, created, err := s.Save(ctx, "us_a", in)
	if err != nil || !created {
		t.Fatalf("save: %v %v", created, err)
	}
	if a.Name != "Cool FM" || a.StreamURL != "http://radio.example/live?x=1" || a.Slug != "cool-fm" || !a.Favourite {
		t.Errorf("%+v", a)
	}
	// Same stream address: one row, a second person shares it, a repeat does nothing.
	b, created, err := s.Save(ctx, "us_b", Input{StreamURL: "http://radio.example/live?x=1"})
	if err != nil || !created || b.ID != a.ID {
		t.Fatalf("second person: %+v %v %v", b, created, err)
	}
	if _, created, _ := s.Save(ctx, "us_a", in); created {
		t.Error("repeat created")
	}
	var n int
	f.st.Reader().QueryRow(`SELECT count(*) FROM radio_stations`).Scan(&n)
	if n != 1 {
		t.Errorf("stations = %d", n)
	}
	// Without an OndaCast slug at all a station still saves and loads.
	c, _, err := s.Save(ctx, "us_a", Input{StreamURL: "https://other.example/s"})
	if err != nil || c.Slug != "" || c.Name != "Radio station" {
		t.Fatalf("%+v %v", c, err)
	}
	favs, _ := s.Favourites(ctx, "us_a")
	if len(favs) != 2 || favs[0].ID != a.ID || favs[1].ID != c.ID {
		t.Errorf("order %+v", favs)
	}
	// A stranger can not load a station they neither saved nor played.
	if _, err := s.Get(ctx, "us_b", c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("stranger: %v", err)
	}
	if err := s.NotePlay(ctx, "us_b", c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.NotePlay(ctx, "us_b", c.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, "us_b", c.ID); err != nil || got.Favourite {
		t.Errorf("played: %+v %v", got, err)
	}
	rec, _ := s.Recent(ctx, "us_b", 5)
	if len(rec) != 1 || rec[0].ID != c.ID || rec[0].LastPlayed == 0 {
		t.Errorf("recent %+v", rec)
	}
	var plays int
	f.st.Reader().QueryRow(`SELECT plays FROM radio_plays WHERE user_id='us_b'`).Scan(&plays)
	if plays != 2 {
		t.Errorf("plays = %d", plays)
	}
	// Remove: the favourite goes, the play keeps it reachable; removing again is not found.
	if err := s.Remove(ctx, "us_a", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, "us_a", a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove twice: %v", err)
	}
	if _, err := s.Get(ctx, "us_a", a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after remove: %v", err)
	}
	// The audit log names ids, never addresses.
	rows, _ := f.st.Reader().Query(`SELECT action, target, detail_json FROM audit_log WHERE action LIKE 'radio.%'`)
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var a, tg, d string
		rows.Scan(&a, &tg, &d)
		seen++
		if strings.Contains(tg+d, "example") || strings.Contains(tg+d, "http") {
			t.Errorf("audit leaks an address: %s %s %s", a, tg, d)
		}
	}
	if seen == 0 {
		t.Error("no audit entries")
	}
}

func TestStoreLimit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, audioHandler("audio/mpeg"))
	for i := 0; i < MaxFavourites; i++ {
		if _, _, err := f.svc.Save(ctx, "us_a", Input{StreamURL: fmt.Sprintf("http://s%d.example/live", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := f.svc.Save(ctx, "us_a", Input{StreamURL: "http://one-more.example/live"}); !errors.Is(err, ErrTooMany) {
		t.Errorf("101st: %v", err)
	}
	// Saving one already kept is still fine, and another person is unaffected.
	if _, _, err := f.svc.Save(ctx, "us_a", Input{StreamURL: "http://s3.example/live"}); err != nil {
		t.Error(err)
	}
	if _, _, err := f.svc.Save(ctx, "us_b", Input{StreamURL: "http://one-more.example/live"}); err != nil {
		t.Error(err)
	}
}

func TestAcquireLimits(t *testing.T) {
	f := newFixture(t, audioHandler("audio/mpeg"))
	rel, err := f.svc.Acquire("us_a", "rs_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Acquire("us_a", "rs_2"); !errors.Is(err, ErrPersonBusy) {
		t.Errorf("second live for one person: %v", err)
	}
	if _, err := f.svc.Acquire("us_b", "rs_1"); err != nil {
		t.Errorf("other person: %v", err)
	}
	f.svc.SetTitle("rs_1", "Ann - Song")
	if f.svc.NowPlaying("rs_1") != "Ann - Song" {
		t.Error("title lost")
	}
	rel()
	rel() // twice is harmless
	if _, err := f.svc.Acquire("us_a", "rs_2"); err != nil {
		t.Errorf("after release: %v", err)
	}
	// us_b still listens to rs_1, so its title stays; once they stop it is forgotten.
	if f.svc.NowPlaying("rs_1") == "" {
		t.Error("title dropped while a listener remains")
	}
}

func TestTitleCap(t *testing.T) {
	f := newFixture(t, audioHandler("audio/mpeg"))
	for i := 0; i < maxTitles+50; i++ {
		f.svc.SetTitle(fmt.Sprintf("rs_%d", i), "t")
	}
	if len(f.svc.titles) > maxTitles {
		t.Errorf("titles = %d", len(f.svc.titles))
	}
	_ = time.Second
}

func TestResaveAndArtSHA(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, audioHandler("audio/mpeg"))
	st, _, err := f.svc.Save(ctx, "us_a", Input{StreamURL: "http://r.example/live"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.Resave(ctx, "us_b", st.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a stranger resaved: %v", err)
	}
	if err := f.svc.Remove(ctx, "us_a", st.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.Resave(ctx, "us_a", st.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("never played, yet resaved: %v", err)
	}
	if err := f.svc.NotePlay(ctx, "us_a", st.ID); err != nil {
		t.Fatal(err)
	}
	got, created, err := f.svc.Resave(ctx, "us_a", st.ID)
	if err != nil || !created || !got.Favourite {
		t.Fatalf("resave: %+v %v %v", got, created, err)
	}
	if _, created, err := f.svc.Resave(ctx, "us_a", st.ID); err != nil || created {
		t.Errorf("second resave: %v %v", created, err)
	}
	sha := strings.Repeat("ab", 32)
	for _, c := range []struct{ path, want string }{
		{"cache/podcast-art/" + sha + ".png", sha}, {"", ""}, {"cache/podcast-art/short.png", ""},
	} {
		if got := (Station{ArtPath: c.path}).ArtSHA(); got != c.want {
			t.Errorf("ArtSHA(%q) = %q", c.path, got)
		}
	}
}
