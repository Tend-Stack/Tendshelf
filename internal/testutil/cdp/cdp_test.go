package cdp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestClientSmoke drives a real browser; it runs only when TENDSHELF_TEST_BROWSER names one.
func TestClientSmoke(t *testing.T) {
	chrome := os.Getenv("TENDSHELF_TEST_BROWSER")
	if chrome == "" {
		t.Skip("TENDSHELF_TEST_BROWSER not set")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><meta name=viewport content="width=device-width"><title>t</title><p id=a>hello</p>`))
	}))
	defer srv.Close()
	b := Start(t, chrome)
	tab := b.NewTab(390, 600, true, true)
	tab.Navigate(srv.URL)
	var got struct {
		Text    string `json:"text"`
		Width   int    `json:"w"`
		Reduced bool   `json:"reduced"`
	}
	tab.Eval(`({text: document.getElementById('a').textContent, w: innerWidth, reduced: matchMedia('(prefers-reduced-motion: reduce)').matches})`, &got)
	if got.Text != "hello" || got.Width != 390 || !got.Reduced {
		t.Fatalf("got %+v", got)
	}
	// Call reaches any DevTools method; a throttle of 1 is "no throttling".
	if err := tab.Call("Emulation.setCPUThrottlingRate", map[string]any{"rate": 1}, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if err := tab.Call("No.suchMethod", nil, nil); err == nil {
		t.Fatal("Call accepted an unknown method")
	}
	shot := filepath.Join(t.TempDir(), "s.png")
	tab.Screenshot(shot)
	if fi, err := os.Stat(shot); err != nil || fi.Size() < 100 {
		t.Fatalf("screenshot: %v", err)
	}
}

// TestScreenshotSettles checks what Screenshot does to make a capture repeatable: a stylesheet still
// loading is waited for, one that failed is fetched again, a Go temp folder and a share link on screen get fixed text, the page keeps its
// scroll offset, nothing it added stays behind, and two captures of the same page are the same bytes.
func TestScreenshotSettles(t *testing.T) {
	chrome := os.Getenv("TENDSHELF_TEST_BROWSER")
	if chrome == "" {
		t.Skip("TENDSHELF_TEST_BROWSER not set")
	}
	// Each of these fails its first fetch, as stylesheet fetches during a long browser run now and
	// then did: one the page links (Navigate loads the page again) and one added after load (the
	// capture fetches it again).
	flaky := map[string]*atomic.Int32{"/flaky.css": {}, "/late-flaky.css": {}}
	rules := map[string]string{"/flaky.css": `#b { color: rgb(4, 5, 6); }`, "/late-flaky.css": `#a { background-color: rgb(7, 8, 9); }`}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n := flaky[r.URL.Path]; n != nil {
			if n.Add(1) == 1 {
				http.Error(w, "busy", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write([]byte(rules[r.URL.Path]))
			return
		}
		if r.URL.Path == "/slow.css" {
			time.Sleep(400 * time.Millisecond)
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write([]byte(`#a { color: rgb(1, 2, 3); }`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><meta name=viewport content="width=device-width"><title>t</title><link rel=stylesheet href=/flaky.css>
<p id=a>/tmp/TestVisualThing3582997436/002/Lantern Book</p>
<p id=b>http://127.0.0.1:37733/invite/abcDEF123_-</p>
<input id=c value="/tmp/TestOther12/001">
<div id=s style="overflow-x: auto; width: 100px; white-space: nowrap">a row wider than its box, scrolled sideways a little</div>
<div style="height: 3000px"></div>`))
	}))
	defer srv.Close()
	b := Start(t, chrome)
	tab := b.NewTab(390, 600, true, true)
	tab.Navigate(srv.URL)
	// A stylesheet added after load, as an htmx swap puts a page's own stylesheet back.
	tab.Eval(`(() => { const l = document.createElement("link"); l.rel = "stylesheet"; l.href = "/slow.css"; document.head.append(l);
		const f = document.createElement("link"); f.rel = "stylesheet"; f.href = "/late-flaky.css"; document.head.append(f);
		document.getElementById("s").scrollLeft = 50; scrollTo(0, 500); })()`, nil)
	dir := t.TempDir()
	tab.Screenshot(filepath.Join(dir, "1.png"))
	var got struct {
		A, B, C string
		Color   string
		ColorB  string
		BackA   string
		Y       float64
		Sheets  int
	}
	tab.Eval(`({a: document.getElementById("a").textContent, b: document.getElementById("b").textContent, c: document.getElementById("c").value,
		color: getComputedStyle(document.getElementById("a")).color, colorB: getComputedStyle(document.getElementById("b")).color, backA: getComputedStyle(document.getElementById("a")).backgroundColor, y: scrollY, sheets: document.adoptedStyleSheets.length})`, &got)
	want := struct {
		A, B, C string
		Color   string
		ColorB  string
		BackA   string
		Y       float64
		Sheets  int
	}{"/tmp/TestVisualThing1000000000/002/Lantern Book", "http://127.0.0.1:40000/invite/xxxxxxxxxxx", "/tmp/TestOther1000000000/001", "rgb(1, 2, 3)", "rgb(4, 5, 6)", "rgb(7, 8, 9)", 500, 0}
	if got != want {
		t.Fatalf("after Screenshot:\n got %+v\nwant %+v", got, want)
	}
	// The scrolling box is still scrolled and scrollable after the capture.
	var row struct {
		Left     float64
		Overflow string
		Marks    int
	}
	tab.Eval(`({left: document.getElementById("s").scrollLeft, overflow: getComputedStyle(document.getElementById("s")).overflowX,
		marks: document.querySelectorAll("[data-ts-capture-still]").length})`, &row)
	if row.Left != 50 || row.Overflow != "auto" || row.Marks != 0 {
		t.Fatalf("scrolling box after Screenshot: %+v", row)
	}
	tab.Screenshot(filepath.Join(dir, "2.png"))
	one, err1 := os.ReadFile(filepath.Join(dir, "1.png"))
	two, err2 := os.ReadFile(filepath.Join(dir, "2.png"))
	if err1 != nil || err2 != nil || !bytes.Equal(one, two) {
		t.Fatalf("two captures of the same page differ (%v, %v)", err1, err2)
	}
}
