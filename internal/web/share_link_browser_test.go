//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// requestLog records every request the signed-out browser makes, so a test can prove that a link
// page never reaches a session route.
type requestLog struct {
	mu    sync.Mutex
	paths []string
}

func (l *requestLog) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		l.paths = append(l.paths, r.Method+" "+r.URL.Path)
		l.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

// outside lists the requests that are neither under the link nor static files.
func (l *requestLog) outside(token string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, p := range l.paths {
		path := p[strings.Index(p, " ")+1:]
		if strings.HasPrefix(path, "/s/"+token) || strings.HasPrefix(path, "/static/") || path == "/favicon.ico" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (l *requestLog) count(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, p := range l.paths {
		if strings.Contains(p, substr) {
			n++
		}
	}
	return n
}

// TestShareLinkBrowser walks the whole life of a share link in Chromium at 390 and 1440: the owner
// makes a link on the book's Share page; a browser with no account opens it, plays, seeks, reloads
// and resumes from the place kept in localStorage, and never reaches a session route; a password
// link refuses a wrong password and opens with the right one; the owner stops the link and the open
// tab is refused at its next request.
func TestShareLinkBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			e := linkNewEnv(t)
			owner := httptest.NewServer(e.a.signedIn(t))
			t.Cleanup(owner.Close)
			log := &requestLog{}
			public := httptest.NewServer(log.wrap(e.a.h)) // no cookie is ever added here
			t.Cleanup(public.Close)
			mobile := w < 768

			// The owner makes a link for 7 days (the default) and reads its address from the panel.
			otab := b.NewTab(w, 900, mobile, false)
			otab.Navigate(owner.URL + "/books/" + e.book.ID + "/share")
			otab.WaitFor(`document.querySelector("#link-label") !== null`, 10*time.Second)
			inviteFill(otab, "#link-label", "Tia Marta")
			playerClickSel(t, otab, `form.ts-share__linkform button[type=submit]`)
			otab.WaitFor(`document.querySelector("#share-link") !== null`, 10*time.Second)
			var address string
			otab.Eval(`document.querySelector("#share-link").value`, &address)
			path := address[strings.Index(address, "/s/"):]
			token := strings.TrimPrefix(path, "/s/")
			if len(token) != 43 {
				t.Fatalf("link address %q", address)
			}
			var over bool
			otab.Eval(`document.documentElement.scrollWidth > innerWidth`, &over)
			if over {
				t.Error("the Share page scrolls sideways with the link panel")
			}

			// A browser with no account opens it and plays.
			ptab := b.NewTab(w, 900, mobile, false)
			ptab.Navigate(public.URL + path)
			ptab.WaitFor(`window.tsPlayer !== undefined && window.tsPlayer.state().bookId !== ""`, 10*time.Second)
			var shown struct {
				Title, Label string
				Over         bool
				Small        []string
				Bookmarks    int
			}
			ptab.Eval(`({
				Title: document.querySelector("h1").textContent,
				Label: document.querySelector("#link-play [data-ts-play-label]").textContent.trim(),
				Over: document.documentElement.scrollWidth > innerWidth,
				Small: [...document.querySelectorAll("a.ts-btn, button")].filter(el => el.offsetParent && el.getBoundingClientRect().height < 43.5).map(el => el.className + ":" + Math.round(el.getBoundingClientRect().height)),
				Bookmarks: [...document.querySelectorAll("[data-ts-bookmark]")].filter(el => el.offsetParent).length,
			})`, &shown)
			if shown.Title != "Linkword Lantern" || shown.Label != "Play" || shown.Over || len(shown.Small) > 0 || shown.Bookmarks != 0 {
				t.Errorf("link page: %+v", shown)
			}
			// One Play per screen: the bar stays out of the way until the book plays.
			ptab.WaitFor(`document.getElementById("player").classList.contains("is-suppressed")`, 5*time.Second)
			playerClickSel(t, ptab, `#link-play [data-ts-play]`)
			ptab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 400`, 10*time.Second)
			if log.count("GET /s/"+token+"/stream/"+e.files[0].ID) == 0 {
				t.Error("the first file was not streamed through the link")
			}

			// Seek into the second file; the place is written to this browser only.
			ptab.Eval(`window.tsPlayer.seek(4500, true)`, nil)
			ptab.WaitFor(`window.tsPlayer.state().fileIdx === 1 && window.tsPlayer.state().positionMs >= 4500`, 10*time.Second)
			ptab.Eval(`window.tsPlayer.pause()`, nil)
			key := "ts.link.place." + e.book.ID
			var saved struct {
				PositionMs int64 `json:"positionMs"`
			}
			ptab.WaitFor(`localStorage.getItem(`+cdp.Quote(key)+`) !== null && JSON.parse(localStorage.getItem(`+cdp.Quote(key)+`)).positionMs >= 4500`, 10*time.Second)
			ptab.Eval(`JSON.parse(localStorage.getItem(`+cdp.Quote(key)+`))`, &saved)

			// Reload: the button says Resume and continues within 2 s of the kept place.
			ptab.Navigate(public.URL + path)
			ptab.WaitFor(`window.tsPlayer !== undefined && window.tsPlayer.state().bookId !== ""`, 10*time.Second)
			ptab.WaitFor(`document.querySelector("#link-play [data-ts-play-label]").textContent.trim() === "Resume"`, 10*time.Second)
			playerClickSel(t, ptab, `#link-play [data-ts-play]`)
			ptab.WaitFor(`window.tsPlayer.state().playing`, 10*time.Second)
			st := playerStateOf(ptab)
			if d := st.PositionMs - saved.PositionMs; d < -500 || d > 2000 {
				t.Errorf("resumed at %d ms, saved %d ms", st.PositionMs, saved.PositionMs)
			}
			ptab.Eval(`window.tsPlayer.pause()`, nil)
			if w >= 1280 {
				// the desktop bar's waveform asks for peaks through the link
				time.Sleep(300 * time.Millisecond)
			}

			// Nothing but the link and static files was requested: no /progress, /hx/, /me, bookmarks.
			if out := log.outside(token); len(out) > 0 {
				t.Errorf("requests outside the link: %v", out)
			}
			if log.count("/progress") > 0 || log.count("/hx/") > 0 || log.count("/bookmarks") > 0 || log.count("/me/") > 0 {
				t.Errorf("a session route was requested")
			}

			// A password link: wrong, then right.
			_, pwTok := e.make(e.book.ID, 7, "a long quiet password")
			ltab := b.NewTab(w, 900, mobile, false)
			ltab.Navigate(public.URL + "/s/" + pwTok)
			ltab.WaitFor(`document.querySelector("#link-password") !== null`, 10*time.Second)
			var locked struct {
				HasBook, Over bool
				Small         []string
			}
			ltab.Eval(`({HasBook: document.body.innerText.includes("Linkword"), Over: document.documentElement.scrollWidth > innerWidth,
				Small: [...document.querySelectorAll("input, button")].filter(el => el.type !== "hidden" && el.getBoundingClientRect().height < 43.5).map(el => el.id || el.className)})`, &locked)
			if locked.HasBook || locked.Over || len(locked.Small) > 0 {
				t.Errorf("locked page: %+v", locked)
			}
			inviteFill(ltab, "#link-password", "not the password")
			playerClickSel(t, ltab, `form button[type=submit]`)
			ltab.WaitFor(`document.querySelector("#link-error") !== null`, 10*time.Second)
			var msg string
			ltab.Eval(`document.querySelector("#link-error").textContent`, &msg)
			if !strings.Contains(msg, "isn") || !strings.Contains(msg, "right") {
				t.Errorf("wrong password message %q", msg)
			}
			inviteFill(ltab, "#link-password", "a long quiet password")
			playerClickSel(t, ltab, `form button[type=submit]`)
			ltab.WaitFor(`document.querySelector("#link-play [data-ts-play]") !== null && window.tsPlayer !== undefined`, 10*time.Second)

			// The owner stops the first link; the open tab is refused at its next request.
			otab.Navigate(owner.URL + "/books/" + e.book.ID + "/share")
			otab.WaitFor(`document.querySelector('button[aria-label="Stop the link for Tia Marta"]') !== null`, 10*time.Second)
			playerClickSel(t, otab, `button[aria-label="Stop the link for Tia Marta"]`)
			otab.WaitFor(`document.querySelector('button[aria-label="Stop the link for Tia Marta"]') === null`, 10*time.Second)
			var status int
			ptab.Eval(`fetch(location.pathname + "/stream/`+e.files[0].ID+`", {headers: {Range: "bytes=0-99"}}).then(r => r.status)`, &status)
			if status != http.StatusNotFound {
				t.Errorf("stream after stop: %d, want 404", status)
			}
			ptab.Navigate(public.URL + path)
			ptab.WaitFor(`document.body.innerText.includes("This link doesn")`, 10*time.Second)
			var gone struct {
				Book, Player bool
			}
			ptab.Eval(`({Book: document.body.innerText.includes("Linkword"), Player: document.getElementById("player") !== null})`, &gone)
			if gone.Book || gone.Player {
				t.Errorf("stopped link page shows the book or the player: %+v", gone)
			}
		})
	}
}
