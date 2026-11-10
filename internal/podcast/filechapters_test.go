package podcast

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPodcastID3Mapping(t *testing.T) {
	long := strings.Repeat("x", 300)
	for _, c := range []struct {
		name        string
		chapters    []Chapter
		probeErr    error
		chaptersURL string
		stored      string
		wantCalls   int
		wantTitles  []string // nil = nothing stored
	}{
		{name: "three chapters, trimmed and capped", chapters: []Chapter{{StartMs: 60_000, Title: " Second "}, {StartMs: 0, Title: "First"}, {StartMs: 120_000, Title: long}},
			wantCalls: 1, wantTitles: []string{"First", "Second", long[:200]}},
		{name: "repeated starts are dropped", chapters: []Chapter{{StartMs: 0, Title: "A"}, {StartMs: 0, Title: "B"}, {StartMs: 5000, Title: "C"}},
			wantCalls: 1, wantTitles: []string{"A", "C"}},
		{name: "one chapter stores nothing", chapters: []Chapter{{StartMs: 0, Title: "Only"}}, wantCalls: 1},
		{name: "no chapters stores nothing", wantCalls: 1},
		{name: "probe error leaves the cache job fine", probeErr: errors.New("ffprobe: boom"), wantCalls: 1},
		{name: "publisher file named means no probe", chapters: []Chapter{{StartMs: 0, Title: "A"}, {StartMs: 1, Title: "B"}}, chaptersURL: "https://pub.test/c.json", wantCalls: 0},
		{name: "stored chapters mean no probe", chapters: []Chapter{{StartMs: 0, Title: "A"}, {StartMs: 1, Title: "B"}}, stored: `{"chapters":[{"startTime":0,"title":"P"}]}`, wantCalls: 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
				w.Header().Set("Content-Type", "audio/mpeg")
				w.Write([]byte("audio-bytes"))
			})
			r.enable(t, nil)
			ctx := context.Background()
			calls := 0
			var gotPath string
			r.svc.ProbeChapters = func(_ context.Context, p string) ([]Chapter, error) {
				calls++
				gotPath = p
				return c.chapters, c.probeErr
			}
			show := r.follow(t, "us_a", "https://a.example/f.xml")
			f := feedOf(1, 0)
			f.Episodes[0].AudioURL = r.pub.URL("aud.test", "/1.mp3")
			f.Episodes[0].ChaptersURL = c.chaptersURL
			r.s.ApplyFeed(ctx, show, f, FetchMeta{})
			var id string
			if err := r.st.Reader().QueryRow(`SELECT id FROM podcast_episodes`).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if c.stored != "" {
				if err := r.s.SetChapters(ctx, id, []byte(c.stored)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.run(r.svc.handleCache, map[string]string{"episode_id": id}); err != nil {
				t.Fatalf("cache job failed: %v", err)
			}
			ep, _ := r.s.Episode(ctx, "us_a", id)
			if ep.CachePath == "" {
				t.Fatal("episode not cached")
			}
			if calls != c.wantCalls {
				t.Fatalf("probe calls = %d, want %d", calls, c.wantCalls)
			}
			if calls == 1 && gotPath != r.svc.CacheFile(ep.CachePath) {
				t.Errorf("probed %q, want the kept file", gotPath)
			}
			if c.stored != "" {
				if ep.ChaptersJSON != c.stored {
					t.Errorf("stored chapters changed: %q", ep.ChaptersJSON)
				}
				return
			}
			var titles []string
			for _, ch := range ParseChapters([]byte(ep.ChaptersJSON)) {
				titles = append(titles, ch.Title)
			}
			if fmt.Sprint(titles) != fmt.Sprint(c.wantTitles) && !(len(titles) == 0 && c.wantTitles == nil) {
				t.Errorf("titles = %q, want %q", titles, c.wantTitles)
			}
			// A failed or empty look is remembered, so the route does not probe the file on every ask.
			if c.wantTitles == nil && calls == 1 {
				if !r.svc.ChapterFetchBlocked(id) || r.svc.FileChaptersPossible(ep) || r.svc.ReadFileChapters(ctx, ep) != nil || calls != 1 {
					t.Errorf("an empty look was not remembered (calls %d)", calls)
				}
			}
		})
	}
}

func TestPodcastID3FeatureOff(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) { w.Write([]byte("x")) })
	if r.svc.FileChaptersPossible(Episode{ID: "pe_1", CachePath: "cache/podcasts/a/b.mp3"}) {
		t.Error("possible without a reader")
	}
	if r.svc.ReadFileChapters(context.Background(), Episode{ID: "pe_1"}) != nil {
		t.Error("read without a reader")
	}
}
