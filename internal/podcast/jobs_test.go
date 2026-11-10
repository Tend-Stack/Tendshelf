package podcast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/podcast/podtest"
)

type rig struct {
	*env
	svc *Service
	pub *podtest.Publisher
	dir string
}

// newRig builds a Service against a fake publisher. h answers every publisher request.
func newRig(t *testing.T, h http.HandlerFunc) *rig {
	t.Helper()
	e := newEnv(t)
	pub := podtest.New(t, h)
	runner, err := jobs.New(e.st, jobs.Options{}) // never started: the tests call the handlers
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	svc := New(e.st, pub.Client, runner, dir, func() time.Time { return *e.clk }, nil)
	if err := svc.Register(); err != nil {
		t.Fatal(err)
	}
	return &rig{env: e, svc: svc, pub: pub, dir: dir}
}

func (r *rig) enable(t *testing.T, mod func(*Settings)) {
	t.Helper()
	s := Defaults()
	s.Enabled = true
	if mod != nil {
		mod(&s)
	}
	if err := SaveSettings(context.Background(), r.st, "us_a", s, *r.clk); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) run(h jobs.Handler, payload any) (any, error) {
	b, _ := json.Marshal(payload)
	return h(context.Background(), &jobs.Task{ID: "jb_t", Attempt: 1, Payload: b})
}

func jobCode(err error) string {
	var je *jobs.Error
	if errors.As(err, &je) {
		return je.Code
	}
	if err == nil {
		return ""
	}
	return "other:" + err.Error()
}

func rssXML(n int, extra string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel><title>Pod</title>
<itunes:author>Ann</itunes:author><description>About</description><itunes:image href="http://pics.test/art.png"/>` + extra)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<item><title>Ep %d</title><guid>g%d</guid><pubDate>Mon, 0%d Jan 2024 10:00:00 +0000</pubDate>
<enclosure url="http://audio.test/%d.mp3" type="audio/mpeg" length="100"/><itunes:duration>10:00</itunes:duration></item>`, i, i, i, i)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func TestRefreshJob(t *testing.T) {
	var cond atomic.Int64
	var feedHits atomic.Int64
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		switch {
		case q.URL.Path == "/feed.xml":
			feedHits.Add(1)
			if q.Header.Get("If-None-Match") == `"v1"` {
				cond.Add(1)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprint(w, rssXML(3, ""))
		case q.URL.Path == "/old.xml":
			http.Redirect(w, q, "/feed.xml", http.StatusMovedPermanently)
		case q.URL.Path == "/art.png":
			var b bytes.Buffer
			img := image.NewRGBA(image.Rect(0, 0, 4, 4))
			img.Set(1, 1, color.White)
			png.Encode(&b, img)
			w.Write(b.Bytes())
		case q.URL.Path == "/html":
			fmt.Fprint(w, "<html>not a feed</html>")
		default:
			http.NotFound(w, q)
		}
	})
	r.pub.Resolve["pics.test"] = "93.184.216.34"
	r.enable(t, nil)
	ctx := context.Background()
	show := r.follow(t, "us_a", r.pub.URL("pub.test", "/feed.xml"))

	res, err := r.run(r.svc.handleRefresh, showPayload{show})
	if err != nil {
		t.Fatal(err)
	}
	if m := res.(map[string]any); m["added"] != 3 {
		t.Fatalf("result %v", res)
	}
	sh, _ := r.s.ShowByID(ctx, show)
	if sh.Title != "Pod" || sh.ETag != `"v1"` || sh.OKAt == 0 || sh.NextRefreshAt != 0 {
		t.Fatalf("show %+v", sh)
	}
	if eps, _, _ := r.s.Episodes(ctx, "us_a", show, EpisodeQuery{}); len(eps) != 3 || eps[0].DurationMs != 600_000 {
		t.Fatalf("episodes %+v", eps)
	}
	if _, ok, _ := r.svc.Runner.ActiveFor(ctx, KindArt, "show_id", show); !ok {
		t.Error("no art job queued for the new picture")
	}

	// Conditional GET: the second refresh sends the ETag and takes the 304.
	if res, err = r.run(r.svc.handleRefresh, showPayload{show}); err != nil || cond.Load() != 1 || res.(map[string]any)["not_modified"] != true {
		t.Fatalf("second refresh: %v %v cond=%d", res, err, cond.Load())
	}
	if sh, _ = r.s.ShowByID(ctx, show); sh.ETag != `"v1"` {
		t.Errorf("etag lost on 304: %q", sh.ETag)
	}

	// A permanent redirect moves the stored address.
	old := r.follow(t, "us_b", r.pub.URL("pub.test", "/old.xml"))
	if _, err = r.run(r.svc.handleRefresh, showPayload{old}); err != nil {
		t.Fatalf("moved refresh: %v", err)
	}
	// Its new address is already another show's, so it keeps the old one.
	if sh, _ = r.s.ShowByID(ctx, old); !strings.HasSuffix(sh.FeedURL, "/old.xml") {
		t.Errorf("moved onto an existing show's address: %q", sh.FeedURL)
	}

	// Failures are stored as a code with back-off, and the job fails in plain words.
	nf := r.follow(t, "us_a", r.pub.URL("pub.test", "/missing.xml"))
	_, err = r.run(r.svc.handleRefresh, showPayload{nf})
	var je *jobs.Error
	if !errors.As(err, &je) || je.Code != "not_found" || je.Text == "" || strings.Contains(je.Text, "pub.test") {
		t.Fatalf("404: %v", err)
	}
	if sh, _ = r.s.ShowByID(ctx, nf); sh.FailCode != "not_found" || sh.FailCount != 1 || sh.NextRefreshAt <= r.clk.UnixMilli() {
		t.Errorf("failure not stored: %+v", sh)
	}
	bad := r.follow(t, "us_a", r.pub.URL("pub.test", "/html"))
	if _, err = r.run(r.svc.handleRefresh, showPayload{bad}); jobCode(err) != "not_a_feed" {
		t.Errorf("html: %v", err)
	}
	// A private address is refused at dial time; the job fails with "refused" and nothing was fetched.
	r.pub.Resolve["intranet.test"] = "10.0.0.5"
	hits := r.pub.Hits.Load()
	priv := r.follow(t, "us_a", r.pub.URL("intranet.test", "/feed.xml"))
	if _, err = r.run(r.svc.handleRefresh, showPayload{priv}); jobCode(err) != "refused" || r.pub.Hits.Load() != hits {
		t.Errorf("private address: %v hits %d->%d", err, hits, r.pub.Hits.Load())
	}
	// A show that was pruned while the job waited is not an error.
	if _, err = r.run(r.svc.handleRefresh, showPayload{"ps_gone"}); err != nil {
		t.Errorf("pruned show: %v", err)
	}
	if _, err = r.run(r.svc.handleRefresh, map[string]string{}); jobCode(err) != "bad_payload" {
		t.Errorf("no id: %v", err)
	}
}

func TestOffIsOff(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) { fmt.Fprint(w, rssXML(1, "")) })
	ctx := context.Background()
	show := r.follow(t, "us_a", r.pub.URL("pub.test", "/feed.xml"))
	ep := "pe_x"
	for name, run := range map[string]func() error{
		"refresh": func() error { _, err := r.run(r.svc.handleRefresh, showPayload{show}); return err },
		"art":     func() error { _, err := r.run(r.svc.handleArt, artPayload{ShowID: show}); return err },
		"cache":   func() error { _, err := r.run(r.svc.handleCache, map[string]string{"episode_id": ep}); return err },
		"import": func() error {
			_, err := r.run(r.svc.handleImport, importPayload{UserID: "us_a", URLs: []string{"https://x.example/f"}})
			return err
		},
		"prune": func() error { _, err := r.run(r.svc.handlePrune, nil); return err },
	} {
		if err := run(); jobCode(err) != "podcasts_off" {
			t.Errorf("%s with podcasts off: %v", name, err)
		}
	}
	if r.pub.Hits.Load() != 0 {
		t.Errorf("a job fetched while off: %d requests", r.pub.Hits.Load())
	}
	// The scheduler sleeps, and opening Podcasts queues nothing.
	r.svc.Tick(ctx, time.Time{})
	if n := r.svc.RefreshStale(ctx, "us_a"); n != 0 {
		t.Errorf("stale refresh while off: %d", n)
	}
	if _, ok, _ := r.svc.Runner.ActiveFor(ctx, KindRefresh, "show_id", show); ok {
		t.Error("a refresh was queued while off")
	}
	if _, ok, _ := r.svc.Runner.ActiveFor(ctx, KindPrune, "kind", "prune"); ok {
		t.Error("a prune was queued while off")
	}
}

func TestSchedulerOnlyWithAnInterval(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {})
	ctx := context.Background()
	show := r.follow(t, "us_a", "https://a.example/f.xml")
	unfollowed := r.follow(t, "us_b", "https://b.example/f.xml")
	r.s.Unfollow(ctx, "us_b", unfollowed)

	queued := func(id string) bool { _, ok, _ := r.svc.Runner.ActiveFor(ctx, KindRefresh, "show_id", id); return ok }
	r.enable(t, nil) // on, but the owner chose no interval
	r.svc.Tick(ctx, time.Time{})
	if queued(show) {
		t.Fatal("refresh queued with the interval off")
	}
	r.enable(t, func(s *Settings) { s.Refresh = Refresh6h })
	r.svc.Tick(ctx, time.Time{})
	if !queued(show) || queued(unfollowed) {
		t.Fatalf("scheduled refresh: followed=%v unfollowed=%v", queued(show), queued(unfollowed))
	}
	// Not due again until its next_refresh_at.
	r.st.Writer().Exec(`UPDATE podcast_shows SET next_refresh_at = ? WHERE id = ?`, r.clk.Add(time.Hour).UnixMilli(), show)
	if ids, _ := r.s.DueShows(ctx, 10); len(ids) != 0 {
		t.Errorf("due before its time: %v", ids)
	}
	r.advance(2 * time.Hour)
	if ids, _ := r.s.DueShows(ctx, 10); len(ids) != 1 {
		t.Errorf("due after its time: %v", ids)
	}
	// Prune is queued once a day.
	last := r.svc.Tick(ctx, time.Time{})
	if last.IsZero() {
		t.Fatal("no prune queued")
	}
	if again := r.svc.Tick(ctx, last); !again.Equal(last) {
		t.Error("prune queued twice within a day")
	}
}

func TestRefreshNowAndStale(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {})
	ctx := context.Background()
	r.enable(t, nil)
	show := r.follow(t, "us_a", "https://a.example/f.xml")
	id, err := r.svc.RefreshNow(ctx, "us_a", show)
	if err != nil || id == "" {
		t.Fatalf("first refresh: %v", err)
	}
	if again, err := r.svc.RefreshNow(ctx, "us_a", show); err != nil || again != id {
		t.Fatalf("a refresh already queued is shared: %q %v", again, err)
	}
	if _, err := r.svc.RefreshNow(ctx, "us_b", show); !errors.Is(err, ErrNotFound) {
		t.Errorf("stranger refreshing: %v", err)
	}
	r.st.Writer().Exec(`UPDATE podcast_shows SET fetched_at = ?`, r.clk.UnixMilli()-60_000)
	r.st.Writer().Exec(`UPDATE jobs SET state = 'succeeded'`)
	if _, err := r.svc.RefreshNow(ctx, "us_a", show); !errors.Is(err, ErrTooSoon) {
		t.Errorf("refresh within five minutes: %v", err)
	}
	if n := r.svc.RefreshStale(ctx, "us_a"); n != 0 {
		t.Errorf("fresh show counted stale: %d", n)
	}
	r.advance(2 * time.Hour)
	if n := r.svc.RefreshStale(ctx, "us_a"); n != 1 {
		t.Errorf("stale show: %d", n)
	}
}

func TestArtJob(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Path == "/art.png" {
			w.Write(b.Bytes())
			return
		}
		fmt.Fprint(w, "<html>not a picture</html>")
	})
	r.enable(t, nil)
	ctx := context.Background()
	show := r.follow(t, "us_a", "https://a.example/f.xml")
	r.s.ApplyFeed(ctx, show, feedOf(1, 0), FetchMeta{})
	r.exec(t, `UPDATE podcast_shows SET art_url = ? WHERE id = ?`, r.pub.URL("pics.test", "/art.png"), show)
	var thumbs []int
	r.svc.Thumb = func(_ context.Context, src, dst string, box int) error {
		thumbs = append(thumbs, box)
		return os.WriteFile(dst, []byte("jpg"), 0o600)
	}
	if _, err := r.run(r.svc.handleArt, artPayload{ShowID: show}); err != nil {
		t.Fatal(err)
	}
	sh, _ := r.s.ShowByID(ctx, show)
	if len(sh.ArtSHA) != 64 || !strings.HasPrefix(sh.ArtPath, "cache/podcast-art/") || len(thumbs) != 3 {
		t.Fatalf("art: %+v thumbs %v", sh, thumbs)
	}
	if _, err := os.Stat(filepath.Join(r.dir, sh.ArtPath)); err != nil {
		t.Errorf("art file: %v", err)
	}
	// A page where a picture was promised is refused and stores nothing.
	r.exec(t, `UPDATE podcast_shows SET art_url = ?, art_path = '', art_sha = '' WHERE id = ?`, r.pub.URL("pics.test", "/page"), show)
	if _, err := r.run(r.svc.handleArt, artPayload{ShowID: show}); jobCode(err) != "bad_answer" {
		t.Errorf("html picture: %v", err)
	}
	if _, err := r.run(r.svc.handleArt, artPayload{ShowID: show, StationID: "rs_x"}); jobCode(err) != "bad_payload" {
		t.Errorf("both ids: %v", err)
	}
}

func TestCacheAndEvict(t *testing.T) {
	audio := bytes.Repeat([]byte("a"), 1000)
	var mode atomic.Int64
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		if mode.Load() == 1 {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>sign in</html>")
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(audio)
	})
	r.enable(t, nil)
	ctx := context.Background()
	show := r.follow(t, "us_a", "https://a.example/f.xml")
	f := feedOf(3, 0)
	for i := range f.Episodes {
		f.Episodes[i].AudioURL = r.pub.URL("aud.test", fmt.Sprintf("/%d.mp3", i))
	}
	r.s.ApplyFeed(ctx, show, f, FetchMeta{})
	ids := map[string]string{}
	rows, _ := r.st.Reader().Query(`SELECT guid_key, id FROM podcast_episodes`)
	for rows.Next() {
		var k, id string
		rows.Scan(&k, &id)
		ids[k] = id
	}
	rows.Close()

	for i, k := range []string{"k0001", "k0002", "k0003"} {
		r.advance(time.Minute)
		if _, err := r.run(r.svc.handleCache, map[string]string{"episode_id": ids[k]}); err != nil {
			t.Fatalf("cache %d: %v", i, err)
		}
	}
	ep, _ := r.s.Episode(ctx, "us_a", ids["k0001"])
	file := r.svc.CacheFile(ep.CachePath)
	if file == "" || !strings.HasPrefix(filepath.ToSlash(ep.CachePath), "cache/podcasts/"+show+"/") || !strings.HasSuffix(ep.CachePath, ".mp3") || ep.CacheBytes != 1000 {
		t.Fatalf("cached episode: %+v (%q)", ep, file)
	}
	if got, _ := os.ReadFile(file); !bytes.Equal(got, audio) {
		t.Error("cached bytes differ")
	}
	if n := r.svc.CacheUsage(ctx); n != 3000 {
		t.Errorf("usage = %d", n)
	}
	// Over the limit: the oldest unplayed goes first unless a played one is available.
	r.s.SetPlayed(ctx, "us_a", ids["k0003"], true)
	removed, err := r.svc.Evict(ctx, 2500)
	if err != nil || removed != 1 {
		t.Fatalf("evict: %d %v", removed, err)
	}
	if ep, _ = r.s.Episode(ctx, "us_a", ids["k0003"]); ep.CachePath != "" {
		t.Error("the played episode should go first")
	}
	if _, err := os.Stat(file); err != nil {
		t.Error("an unplayed episode was removed before the played one")
	}
	// A show nobody follows goes before a followed one.
	other := r.follow(t, "us_b", "https://other.example/f.xml")
	r.s.ApplyFeed(ctx, other, feedOf(1, 0), FetchMeta{})
	var oe string
	r.st.Reader().QueryRow(`SELECT id FROM podcast_episodes WHERE show_id = ?`, other).Scan(&oe)
	r.exec(t, `UPDATE podcast_episodes SET audio_url = ? WHERE id = ?`, r.pub.URL("aud.test", "/o.mp3"), oe)
	r.svc.PutCache(ctx, oe, 1<<30)
	r.s.Unfollow(ctx, "us_b", other)
	if removed, err = r.svc.Evict(ctx, 2000); err != nil || removed != 1 {
		t.Fatalf("evict unfollowed: %d %v", removed, err)
	}
	if e2, _ := r.s.Episode(ctx, "us_b", oe); e2.CachePath != "" {
		t.Error("the unfollowed show's file should go first")
	}
	// A limit of zero keeps nothing; the cache job refuses to store at zero.
	r.svc.ClearCache(ctx)
	if n := r.svc.CacheUsage(ctx); n != 0 {
		t.Errorf("usage after clear = %d", n)
	}
	files, _ := filepath.Glob(filepath.Join(r.svc.CacheDir(), "*", "*"))
	if len(files) != 0 {
		t.Errorf("files left: %v", files)
	}
	r.enable(t, func(s *Settings) { s.CacheBytes = 0 })
	if _, err := r.run(r.svc.handleCache, map[string]string{"episode_id": ids["k0001"]}); jobCode(err) != "cache_off" {
		t.Errorf("cache at zero: %v", err)
	}
	// An HTML answer is refused and leaves no file behind.
	r.enable(t, nil)
	mode.Store(1)
	if _, err := r.run(r.svc.handleCache, map[string]string{"episode_id": ids["k0002"]}); jobCode(err) != "bad_answer" {
		t.Errorf("html audio: %v", err)
	}
	files, _ = filepath.Glob(filepath.Join(r.svc.CacheDir(), "*", "*"))
	if len(files) != 0 {
		t.Errorf("files after a refused download: %v", files)
	}
	// A stored path outside the cache folder is never opened or removed.
	if r.svc.CacheFile("../../etc/passwd") != "" || r.svc.CacheFile("cache/podcasts/../../x") != "" || r.svc.CacheFile("cache/podcasts") != "" {
		t.Error("CacheFile accepted a path outside the cache")
	}
}

func TestImportAndPrune(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {})
	r.enable(t, nil)
	ctx := context.Background()
	res, err := r.run(r.svc.handleImport, importPayload{UserID: "us_a", URLs: []string{
		"https://a.example/f.xml", "ftp://bad.example/f", "https://a.example/f.xml#again", "https://b.example/f.xml"}})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]int)
	if m["followed"] != 2 || m["skipped"] != 1 {
		t.Errorf("import result %v", m)
	}
	shows, _ := r.s.Shows(ctx, "us_a")
	if len(shows) != 2 {
		t.Fatalf("shows %d", len(shows))
	}
	for _, sh := range shows {
		if _, ok, _ := r.svc.Runner.ActiveFor(ctx, KindRefresh, "show_id", sh.ID); !ok {
			t.Errorf("no refresh queued for %s", sh.FeedURL)
		}
	}
	// Prune removes a show nobody keeps, with its folder.
	gone := shows[0].ID
	r.s.Unfollow(ctx, "us_a", gone)
	folder := filepath.Join(r.svc.CacheDir(), gone)
	os.MkdirAll(folder, 0o700)
	os.WriteFile(filepath.Join(folder, "x.mp3"), []byte("x"), 0o600)
	art := filepath.Join(r.svc.ArtDir(), "stray.png")
	os.MkdirAll(r.svc.ArtDir(), 0o700)
	os.WriteFile(art, []byte("x"), 0o600)
	if _, err := r.run(r.svc.handlePrune, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.ShowByID(ctx, gone); !errors.Is(err, ErrNotFound) {
		t.Errorf("orphan show kept: %v", err)
	}
	if _, err := os.Stat(folder); err == nil {
		t.Error("orphan cache folder kept")
	}
	if _, err := os.Stat(art); err == nil {
		t.Error("stray picture kept")
	}
}

func TestStreamSlots(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {})
	var rel []func()
	for i := 0; i < MaxStreamsPerPerson; i++ {
		f, ok := r.svc.AcquireStream("us_a")
		if !ok {
			t.Fatalf("slot %d refused", i)
		}
		rel = append(rel, f)
	}
	if _, ok := r.svc.AcquireStream("us_a"); ok {
		t.Error("a fourth stream for one person")
	}
	rel[0]()
	rel[0]() // releasing twice frees one slot only
	if f, ok := r.svc.AcquireStream("us_a"); !ok {
		t.Error("slot not freed")
	} else {
		rel = append(rel, f)
	}
	if _, ok := r.svc.AcquireStream("us_a"); ok {
		t.Error("double release freed two slots")
	}
	for _, f := range rel[1:] {
		f()
	}
	var held []func()
	for i := 0; i < MaxStreams; i++ {
		f, ok := r.svc.AcquireStream(fmt.Sprintf("us_%d", i))
		if !ok {
			t.Fatalf("server slot %d refused", i)
		}
		held = append(held, f)
	}
	if _, ok := r.svc.AcquireStream("us_other"); ok {
		t.Error("a 17th stream on the server")
	}
	for _, f := range held {
		f()
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if s := LoadSettings(ctx, e.st); s.Enabled || s.Refresh != RefreshOff || s.CacheBytes != DefaultCacheBytes || s.ServerAuto || s.RadioEnabled {
		t.Fatalf("defaults %+v", s)
	}
	want := Settings{Enabled: true, Refresh: Refresh6h, CacheBytes: 5 << 30, ServerAuto: true, RadioEnabled: true}
	if err := SaveSettings(ctx, e.st, "us_a", want, *e.clk); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(ctx, e.st); got != want {
		t.Errorf("got %+v", got)
	}
	for _, bad := range []Settings{{Refresh: "2h"}, {Refresh: RefreshOff, CacheBytes: -1}, {Refresh: RefreshOff, CacheBytes: MaxCacheBytes + 1}} {
		if err := SaveSettings(ctx, e.st, "us_a", bad, *e.clk); !errors.Is(err, ErrBadSetting) {
			t.Errorf("%+v accepted", bad)
		}
	}
	if got := LoadSettings(ctx, e.st); got != want {
		t.Error("a refused save changed the settings")
	}
	var n int
	e.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'podcast.settings'`).Scan(&n)
	if n != 1 {
		t.Errorf("audit rows = %d", n)
	}
}
