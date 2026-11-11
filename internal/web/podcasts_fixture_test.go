package web

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/podcast/podtest"
	"github.com/Tend-Stack/tendshelf/internal/radio"
)

// podFix is a fake publisher and a fake OndaCast on one httptest server, reached through the real guarded
// client (podtest). No test here touches the network.
type podFix struct {
	pub   *podtest.Publisher
	svc   *podcast.Service
	audio []byte

	ignoreRange atomic.Bool            // the publisher answers 200 with the whole file to a Range request
	ondaDown    atomic.Bool            // OndaCast answers 503
	htmlAudio   atomic.Bool            // the publisher answers a web page instead of audio
	audioCT     atomic.Pointer[string] // when set, audio answers with this Content-Type
	chapterFail atomic.Bool            // the chapters file answers 500
	chapterHits atomic.Int64
	audioHits   atomic.Int64
	ondaHits    atomic.Int64
	hold        chan struct{} // when set, audio answers wait here until closed

	mu          sync.Mutex
	audioReqs   []http.Header // headers of the requests that reached the audio handler
	audioStatus int           // when non-zero, the audio handler answers this status
	searchImage string        // the picture address the fake OndaCast gives its podcast hit; "" means the default art
}

const podFixEpisodes = 3

func newPodFix(t *testing.T) *podFix {
	t.Helper()
	f := &podFix{audio: bytes.Repeat([]byte("0123456789abcdef"), 4096)} // 64 KiB
	f.pub = podtest.New(t, f.serve)
	return f
}

func (f *podFix) audioURL(i int) string { return f.pub.URL("audio.test", fmt.Sprintf("/ep%d.mp3", i)) }

func (f *podFix) feedURL() string { return f.pub.URL("pub.test", "/feed.xml") }

// otherFeedURL is a second show on the same fake publisher.
func (f *podFix) otherFeedURL() string { return f.pub.URL("pub.test", "/other.xml") }

func (f *podFix) rss(title string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0"?><rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>%s</title>
<itunes:author>Ann Author</itunes:author><description>Notes about %s</description><itunes:image href="%s"/>`, title, title, f.pub.URL("pics.test", "/art.png"))
	for i := 1; i <= podFixEpisodes; i++ {
		fmt.Fprintf(&b, `<item><title>%s episode %d</title><guid>%s-g%d</guid><pubDate>Mon, 0%d Jan 2024 10:00:00 +0000</pubDate>
<enclosure url="%s" type="audio/mpeg" length="%d"/><itunes:duration>10:00</itunes:duration><podcast:chapters url="%s" type="application/json+chapters"/></item>`,
			title, i, title, i, i, f.audioURL(i), len(f.audio), f.pub.URL("chap.test", "/chapters.json"))
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func (f *podFix) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Host, "ondacast.test") {
		f.ondaHits.Add(1)
		if f.ondaDown.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		switch {
		case r.URL.Path == "/api/v2/search":
			f.mu.Lock()
			img := f.searchImage
			f.mu.Unlock()
			if img == "" {
				img = f.pub.URL("pics.test", "/art.png")
			}
			fmt.Fprintf(w, `{"success":true,"data":[{"id":1,"type":"podcast","title":"Found Show","subtitle":"Sub","slug":"found-show","image_url":%q,"rank":1}]}`, img)
		case r.URL.Path == "/api/v2/podcasts/found-show":
			fmt.Fprintf(w, `{"success":true,"data":{"feed_url":%q}}`, f.feedURL())
		case r.URL.Path == "/api/v2/podcasts/other-show":
			fmt.Fprintf(w, `{"success":true,"data":{"feed_url":%q}}`, f.otherFeedURL())
		default:
			http.NotFound(w, r)
		}
		return
	}
	switch {
	case r.URL.Path == "/feed.xml":
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, f.rss("Main"))
	case r.URL.Path == "/other.xml":
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, f.rss("Other"))
	case strings.HasSuffix(r.URL.Path, ".mp3"):
		f.audioHits.Add(1)
		f.mu.Lock()
		f.audioReqs = append(f.audioReqs, r.Header.Clone())
		status := f.audioStatus
		f.mu.Unlock()
		if f.hold != nil {
			select {
			case <-f.hold:
			case <-r.Context().Done():
				return
			}
		}
		switch {
		case status != 0:
			w.WriteHeader(status)
		case f.audioCT.Load() != nil:
			w.Header().Set("Content-Type", *f.audioCT.Load())
			http.ServeContent(w, r, "ep.mp3", time.Time{}, bytes.NewReader(f.audio))
		case f.htmlAudio.Load():
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>please sign in</html>")
		case f.ignoreRange.Load():
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Write(f.audio)
		default:
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Header().Set("Set-Cookie", "tracker=1")
			http.ServeContent(w, r, "ep.mp3", time.Time{}, bytes.NewReader(f.audio))
		}
	case r.URL.Path == "/chapters.json":
		f.chapterHits.Add(1)
		if f.chapterFail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"1.2.0","chapters":[{"startTime":0,"title":"Intro"}]}`)
	case r.URL.Path == "/big.png": // over the 5 MiB picture cap
		var b bytes.Buffer
		png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)))
		w.Write(b.Bytes())
		w.Write(make([]byte, 6<<20))
	case r.URL.Path == "/art.png":
		var b bytes.Buffer
		png.Encode(&b, solidImage(96))
		w.Write(b.Bytes())
	default:
		http.NotFound(w, r)
	}
}

// mod builds the podcast service on the app's store and runner and hands it to the handler.
func (f *podFix) mod(t *testing.T) func(*Options) {
	return func(o *Options) {
		f.svc = podcast.New(o.Store, f.pub.Client, o.Jobs, o.DataDir, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err := f.svc.Register(); err != nil {
			t.Fatal(err)
		}
		o.Podcasts = f.svc
		o.Radio = radio.New(o.Store, f.pub.Client, f.svc, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}
}

// enable turns podcasts on in the app's settings.
func (f *podFix) enable(t *testing.T, a *testApp, mod func(*podcast.Settings)) {
	t.Helper()
	s := podcast.Defaults()
	s.Enabled = true
	if mod != nil {
		mod(&s)
	}
	if err := podcast.SaveSettings(context.Background(), a.st, "", s, time.Now()); err != nil {
		t.Fatal(err)
	}
}
