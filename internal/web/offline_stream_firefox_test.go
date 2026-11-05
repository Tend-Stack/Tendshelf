//go:build dev

package web

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
	"github.com/Tend-Stack/tendshelf/internal/testutil/webdriver"
)

// The Firefox hang (operator, 2026-11-05): over https with the worker running, audio plays for a
// while and then the browser stops answering. This drives a real headless Firefox with a throwaway
// profile against a localhost server (a secure context, so the worker runs), plays a long file, seeks
// repeatedly and times ordinary requests afterwards, with the worker on and off, for a stored and a
// not stored book. Opt-in: TENDSHELF_TEST_FIREFOX=1 (or a binary) (the geckodriver is found on PATH
// or at /snap/bin/geckodriver). Run by:
// TENDSHELF_TEST_FIREFOX=1 go test -tags dev -v -run OfflineStreamFirefox ./internal/web/

const firefoxSeekJS = `
const done = arguments[arguments.length - 1];
const MEDIA = %MEDIA%, OTHER = %OTHER%, ASSET = %ASSET%;
(async () => {
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const timed = async (url, opts) => {
    const t0 = performance.now();
    const c = new AbortController();
    const timer = setTimeout(() => c.abort(), 8000);
    try {
      const res = await fetch(url, { ...opts, signal: c.signal });
      await res.arrayBuffer();
      return Math.round(performance.now() - t0);
    } catch (e) {
      return -1;
    } finally {
      clearTimeout(timer);
    }
  };
  const audio = new Audio(MEDIA);
  audio.muted = true;
  const out = { seeks: 0 };
  try {
    await audio.play();
  } catch (e) {
    out.playError = String(e);
  }
  await sleep(1000);
  out.started = audio.currentTime;
  for (let i = 0; i < 50; i++) {
    audio.currentTime = ((i * 37) % 90) * 5;
    out.seeks++;
    await sleep(120);
  }
  out.sameOrigin = [];
  for (let i = 0; i < 4; i++) out.sameOrigin.push(await timed("/static/" + ASSET, {cache: "no-store"}));
  out.otherOrigin = await timed(OTHER, { mode: "no-cors", cache: "no-store" });
  out.position = audio.currentTime;
  audio.pause();
  audio.removeAttribute("src");
  audio.load();
  done(out);
})();`

func firefoxEnv(t *testing.T) (*offlineEnv, *httptest.Server) {
	t.Helper()
	e := offlineApp(t)
	// One long file: 450 s of 16 kB/s PCM is about 7 MB.
	af := e.files[0]
	if err := wav.Write(af.Path, 450_000, 440); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(af.Path)
	if err != nil {
		t.Fatal(err)
	}
	streamExec(t, e.a, `UPDATE audio_files SET duration_ms=450000, size=?, mtime_ns=? WHERE id=?`, fi.Size(), fi.ModTime().UnixNano(), af.ID)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(other.Close)
	return e, other
}

func TestOfflineStreamFirefox(t *testing.T) {
	ff := os.Getenv("TENDSHELF_TEST_FIREFOX")
	if ff == "" {
		t.Skip("set TENDSHELF_TEST_FIREFOX to 1 (or a Firefox binary) to run this")
	}
	if ff == "1" || ff == "auto" {
		ff = "" // let geckodriver find Firefox (the snap build is not started by its path)
	}
	driver, err := exec.LookPath("geckodriver")
	if err != nil {
		driver = "/snap/bin/geckodriver"
	}
	for _, c := range []struct {
		name   string
		worker bool
		stored bool
		h2     bool // behind a TLS reverse proxy speaking HTTP/2, as tailscale serve does
	}{
		{"worker off", false, false, false},
		{"worker on, not stored", true, false, false},
		{"worker on, stored", true, true, false},
		{"h2, worker off", false, false, true},
		{"h2, worker on, not stored", true, false, true},
		{"h2, worker on, stored", true, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, other := firefoxEnv(t)
			prefs := map[string]any{"dom.serviceWorkers.enabled": c.worker, "media.autoplay.default": 0, "media.autoplay.blocking_policy": 0}
			base := e.URL
			if c.h2 {
				target, _ := url.Parse(e.URL)
				front := httptest.NewUnstartedServer(httputil.NewSingleHostReverseProxy(target))
				front.EnableHTTP2 = true
				front.StartTLS()
				t.Cleanup(front.Close)
				base = front.URL
			}
			s := webdriver.StartFirefox(t, driver, ff, prefs)
			s.Navigate(base + "/libraries")
			if c.worker {
				s.ExecAsync(`const done = arguments[arguments.length-1]; navigator.serviceWorker.ready.then(() => done(true));`, nil)
				s.Navigate(base + "/libraries") // now controlled
				var controlled bool
				s.Eval(`Boolean(navigator.serviceWorker.controller)`, &controlled)
				if !controlled && c.h2 {
					t.Skip("Firefox does not run a worker behind a self-signed certificate")
				}
				if !controlled {
					t.Fatal("the worker does not control the page")
				}
			}
			if c.stored {
				seed := offlineSeedJS + `;
				const done = arguments[arguments.length - 1];
				window.__seed(` + strconv.Quote(e.book.ID) + `).then(done);`
				s.ExecAsync(seed, nil)
			}
			var asset string
			s.Eval(`document.querySelector('link[rel=stylesheet]').href.split("/static/")[1]`, &asset)
			var out struct {
				Seeks       int
				Started     float64
				Position    float64
				SameOrigin  []int
				OtherOrigin int
				PlayError   string
			}
			js := strings.NewReplacer("%MEDIA%", strconv.Quote("/stream/"+e.files[0].ID), "%OTHER%", strconv.Quote(other.URL), "%ASSET%", strconv.Quote(asset)).Replace(firefoxSeekJS)
			s.ExecAsync(js, &out)
			t.Logf("%s: %+v", c.name, out)
		})
	}
}
