package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// id3World is a podcast world whose chapters are read through the real ffprobe wrapper (the fake binary).
type id3World struct {
	*podWorld
	probes atomic.Int64
}

func newID3World(t *testing.T) *id3World {
	t.Helper()
	w := &id3World{podWorld: newPodWorld(t)}
	tools := mediatest.FakeTools(t)
	w.fx.svc.ProbeChapters = func(ctx context.Context, path string) ([]podcast.Chapter, error) {
		w.probes.Add(1)
		p, err := tools.Probe(ctx, path)
		if err != nil {
			return nil, err
		}
		var out []podcast.Chapter
		for _, c := range p.Chapters {
			out = append(out, podcast.Chapter{StartMs: c.StartMs, Title: c.Title})
		}
		return out, nil
	}
	return w
}

// keep makes episode i look the way a cached one without a publisher chapters file does: a file in the
// cache folder that the fake ffprobe describes, no chapters_url, nothing stored.
func (w *id3World) keep(t *testing.T, i int, f mediatest.FakeFile) {
	t.Helper()
	rel := filepath.ToSlash(filepath.Join("cache", "podcasts", w.show, w.eps[i]+".mp3"))
	abs := filepath.Join(w.fx.svc.DataDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	mediatest.WriteFake(t, abs, f)
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_episodes SET chapters_url = '', chapters_json = NULL, cache_path = ?, cache_bytes = 100 WHERE id = ?`, rel, w.eps[i]); err != nil {
		t.Fatal(err)
	}
}

func (w *id3World) unkeep(t *testing.T, i int) {
	t.Helper()
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_episodes SET chapters_url = '', chapters_json = NULL, cache_path = NULL WHERE id = ?`, w.eps[i]); err != nil {
		t.Fatal(err)
	}
}

func TestPodcastID3ChaptersRoute(t *testing.T) {
	w := newID3World(t)
	w.keep(t, 0, mediatest.FakeFile{Container: "mp3", Codec: "mp3", Chapters: []mediatest.FakeChapter{
		mediatest.Chapter(0, 60_000, "Intro"), mediatest.Chapter(60_000, 120_000, " Interview "),
	}})
	w.unkeep(t, 1) // not kept, no chapters_url: nothing to read
	ep, other := w.eps[0], w.eps[1]

	rec := w.get("/podcasts/chapters/"+ep, w.ben)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `"Intro"`) || !strings.Contains(body, `"Interview"`) || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("chapters: %d %q", rec.Code, body)
	}
	// Stored now: a second ask does not probe again, and the player fragment carries the chapters.
	if rec = w.get("/podcasts/chapters/"+ep, w.ben); rec.Code != 200 || w.probes.Load() != 1 {
		t.Errorf("second ask: %d, probes %d", rec.Code, w.probes.Load())
	}
	if frag := w.get("/hx/player/episode/"+ep, w.ben).Body.String(); !strings.Contains(frag, `>Interview</li>`) || strings.Contains(frag, "data-chapters-href") {
		t.Errorf("fragment: %q", frag)
	}
	// A stranger who does not follow the show still gets 404, stored chapters or not.
	if rec = w.get("/podcasts/chapters/"+ep, w.cy); rec.Code != http.StatusNotFound {
		t.Errorf("stranger: %d", rec.Code)
	}
	// An uncached episode without a chapters file is a plain 404 and is never probed.
	if rec = w.get("/podcasts/chapters/"+other, w.ben); rec.Code != http.StatusNotFound || w.probes.Load() != 1 {
		t.Errorf("uncached: %d, probes %d", rec.Code, w.probes.Load())
	}
	if frag := w.get("/hx/player/episode/"+other, w.ben).Body.String(); strings.Contains(frag, "data-chapters-href") {
		t.Errorf("uncached fragment offers chapters: %q", frag)
	}
}

func TestPodcastID3EmptyLookIsRemembered(t *testing.T) {
	w := newID3World(t)
	w.keep(t, 2, mediatest.FakeFile{Container: "mp3", Codec: "mp3", Chapters: []mediatest.FakeChapter{mediatest.Chapter(0, 1000, "Only one")}})
	ep := w.eps[2]
	// Before the first look the link is offered, so the player asks once.
	if frag := w.get("/hx/player/episode/"+ep, w.ben).Body.String(); !strings.Contains(frag, `data-chapters-href="/podcasts/chapters/`+ep+`"`) {
		t.Errorf("cached fragment does not offer chapters: %q", frag)
	}
	for i := 0; i < 3; i++ {
		if rec := w.get("/podcasts/chapters/"+ep, w.ben); rec.Code != http.StatusNotFound {
			t.Fatalf("ask %d: %d", i, rec.Code)
		}
	}
	if n := w.probes.Load(); n != 1 {
		t.Errorf("probes = %d, want 1", n)
	}
	if frag := w.get("/hx/player/episode/"+ep, w.ben).Body.String(); strings.Contains(frag, "data-chapters-href") {
		t.Errorf("fragment still offers chapters after an empty look: %q", frag)
	}
}

func TestPodcastID3APIDocument(t *testing.T) {
	w := newID3World(t)
	w.keep(t, 0, mediatest.FakeFile{Container: "mp3", Codec: "mp3"})
	w.unkeep(t, 1)
	tok := w.bearer(t, w.benID)
	get := func(id string) string {
		rec := deviceBearerReq(w.a, "GET", "/api/v1/podcasts/episodes/"+id, tok, nil)
		if rec.Code != 200 {
			t.Fatalf("episode %s: %d %q", id, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	want := `"chaptersUrl":"/api/v1/podcasts/chapters/` + w.eps[0] + `"`
	if doc := get(w.eps[0]); !strings.Contains(doc, want) {
		t.Errorf("cached episode document lacks the chapters link: %q", doc)
	}
	if doc := get(w.eps[1]); strings.Contains(doc, "chaptersUrl") {
		t.Errorf("uncached episode document offers chapters: %q", doc)
	}
}
