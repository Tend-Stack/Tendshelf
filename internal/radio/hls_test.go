package radio

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/radio/hls/hlstest"
)

// hlsStations is a fake origin with one HLS station per behaviour the add-time check must tell apart.
func hlsFixture(t *testing.T) *fixture {
	t.Helper()
	stations := map[string]*hlstest.Station{
		"/ok/":     {Prefix: "/ok/", Title: "Ann - Song"},
		"/mp3/":    {Prefix: "/mp3/", MP3: true},
		"/enc/":    {Prefix: "/enc/", Key: "AES-128"},
		"/sample/": {Prefix: "/sample/", Key: "SAMPLE-AES"},
		"/video/":  {Prefix: "/video/", Video: true},
		"/ac3/":    {Prefix: "/ac3/", Codecs: "ac-3"},
		"/rec/":    {Prefix: "/rec/", EndList: true},
	}
	return newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		for p, st := range stations {
			if strings.HasPrefix(r.URL.Path, p) {
				st.Handler()(w, r)
				return
			}
		}
		if r.URL.Path == "/notplaylist.m3u8" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>sign in</html>"))
			return
		}
		http.NotFound(w, r)
	})
}

// TestRadioHLSProbe: an HLS address is opened like a listener would; good ones come back with format
// "hls" and their codec, and the unplayable ones are refused in plain words.
func TestRadioHLSProbe(t *testing.T) {
	f := hlsFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name, path string
		wantCodec  string
		wantErr    *Refusal
	}{
		{"master playlist, AAC", "/ok/master.m3u8", "aac", nil},
		{"media playlist directly", "/ok/live.m3u8", "aac", nil},
		{"MP3 segments", "/mp3/master.m3u8", "mp3", nil},
		{"AES-128 is locked", "/enc/master.m3u8", "", ErrHLSLocked},
		{"SAMPLE-AES is locked", "/sample/live.m3u8", "", ErrHLSLocked},
		{"video only", "/video/master.m3u8", "", ErrHLSVideo},
		{"unsupported sound format", "/ac3/master.m3u8", "", ErrHLSCodec},
		{"a recording", "/rec/live.m3u8", "", ErrHLSRecording},
		{"not a playlist", "/notplaylist.m3u8", "", ErrHLS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url := f.pub.URL("s.test", tc.path)
			p, err := f.svc.Probe(ctx, url)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr.Text)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.Format != "hls" || p.Codec != tc.wantCodec || p.StreamURL != url {
				t.Errorf("probed = %+v", p)
			}
		})
	}
}

func TestRadioHLSRefusalWords(t *testing.T) {
	for r, want := range map[*Refusal]string{
		ErrHLSLocked:    "This station's stream is locked, so Tendshelf will not play it.",
		ErrHLSVideo:     "That stream sends video, and Tendshelf plays radio as sound only.",
		ErrHLSCodec:     "That station uses a sound format Tendshelf can't play.",
		ErrHLSRecording: "That address is a recording, not a live station.",
	} {
		if r.Text != want {
			t.Errorf("%q, want %q", r.Text, want)
		}
	}
}

// TestRadioHLSStore: the format goes in through Save and comes out of Get, Favourites, Recent and Kept; a
// station saved without one is "icy".
func TestRadioHLSStore(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	hlsSt, _, err := f.svc.Save(ctx, "us_a", Input{StreamURL: "https://a.example/live.m3u8", Name: "H", Format: "hls", Codec: "aac"})
	if err != nil {
		t.Fatal(err)
	}
	icy, _, err := f.svc.Save(ctx, "us_a", Input{StreamURL: "https://a.example/live", Name: "I"})
	if err != nil {
		t.Fatal(err)
	}
	if hlsSt.Format != "hls" || icy.Format != "icy" {
		t.Fatalf("formats = %q, %q", hlsSt.Format, icy.Format)
	}
	got, err := f.svc.Get(ctx, "us_a", hlsSt.ID)
	if err != nil || got.Format != "hls" {
		t.Errorf("Get = %q, %v", got.Format, err)
	}
	favs, _ := f.svc.Favourites(ctx, "us_a")
	if len(favs) != 2 || favs[0].Format != "hls" || favs[1].Format != "icy" {
		t.Errorf("favourites = %+v", favs)
	}
	if k, ok := f.svc.Kept(ctx, "us_a", "", "https://a.example/live.m3u8"); !ok || k.Format != "hls" {
		t.Errorf("Kept = %q, %v", k.Format, ok)
	}
	if err := f.svc.NotePlay(ctx, "us_a", hlsSt.ID); err != nil {
		t.Fatal(err)
	}
	rec, _ := f.svc.Recent(ctx, "us_a", 10)
	if len(rec) != 1 || rec[0].Format != "hls" {
		t.Errorf("recent = %+v", rec)
	}
	// An unknown format is stored as icy: the column only ever holds the two values.
	odd, _, err := f.svc.Save(ctx, "us_a", Input{StreamURL: "https://a.example/odd", Format: "dash"})
	if err != nil || odd.Format != "icy" {
		t.Errorf("odd format = %q, %v", odd.Format, err)
	}
}

// TestRadioPlaySlugRemember: Remember keeps the station out of the favourites yet reachable by its person
// alone, and a later Save of it makes it a favourite without a second row.
func TestRadioPlaySlugRemember(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	in := Input{StreamURL: "https://a.example/pop", Name: "Pop", Slug: "pop"}
	st, err := f.svc.Remember(ctx, "us_a", in)
	if err != nil {
		t.Fatal(err)
	}
	if st.Favourite {
		t.Error("remembered station is a favourite")
	}
	if n, _ := f.svc.FavouriteCount(ctx, "us_a"); n != 0 {
		t.Errorf("favourites = %d", n)
	}
	if _, err := f.svc.Get(ctx, "us_b", st.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("other person's Get = %v", err)
	}
	again, err := f.svc.Remember(ctx, "us_a", in)
	if err != nil || again.ID != st.ID {
		t.Errorf("second Remember = %v, %v", again.ID, err)
	}
	rec, _ := f.svc.Recent(ctx, "us_a", 10)
	if len(rec) != 1 {
		t.Errorf("recent = %d", len(rec))
	}
	saved, created, err := f.svc.Save(ctx, "us_a", in)
	if err != nil || !created || saved.ID != st.ID || !saved.Favourite {
		t.Errorf("Save after Remember = %+v %v %v", saved, created, err)
	}
}
