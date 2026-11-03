//go:build dev

package web

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// TestBulkLookupKeepsPlaying plays a book, opens Libraries & scanning by a boosted link, starts "Look
// up all books" on a fake network, and checks that the count line moves, that the pause line shows
// once the day's request cap is used up, that Stop works, and that through all of it the same
// <audio> element keeps playing and the page is never reloaded. Needs TENDSHELF_TEST_BROWSER.
func TestBulkLookupKeepsPlaying(t *testing.T) {
	b := chromium(t)
	for _, vp := range []struct {
		name   string
		w, h   int
		mobile bool
	}{{"desktop-1440", 1440, 900, false}, {"mobile-390", 390, 844, true}} {
		t.Run(vp.name, func(t *testing.T) {
			sn := &slowNet{delay: 100 * time.Millisecond}
			e := playerAppMods(t, nil, slowLookupMod(t, sn))
			if err := sn.runner.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sn.runner.Stop(context.Background()) })
			for i := 1; i <= 3; i++ {
				librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{
					Title: fmt.Sprintf("Bulk Book %d", i), Authors: []string{"Ann Author"}, DurationMs: 60_000, Files: 1})
			}
			// A bulk lookup takes books whose details have been read.
			streamExec(t, e.a, `INSERT INTO book_sidecars(book_id, signature, checked_at) SELECT id, 'test', 1 FROM books`)
			e.store(t, 100_000, "lookupdevice01", "Other device", 0, false)
			tab := b.NewTab(vp.w, vp.h, vp.mobile, false)
			tab.Navigate(e.URL + "/libraries")
			tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
			bmkeyPlay(t, tab, "[data-ts-toggle]")
			tab.Eval(`window.__tsMarker = 7; document.querySelector("audio").__tsSame = 1; window.__tsLoads = 0;
			  window.addEventListener("load", () => { window.__tsLoads++; })`, nil)
			var before float64
			tab.Eval(`document.querySelector("audio").currentTime`, &before)

			kept := func(stage string) {
				t.Helper()
				var r struct {
					Marker, Same, Loads float64
					Playing             bool
				}
				tab.Eval(`({Marker: window.__tsMarker || 0, Same: document.querySelector("audio").__tsSame || 0, Loads: window.__tsLoads,
				  Playing: window.tsPlayer.state().playing})`, &r)
				if r.Marker != 7 || r.Same != 1 || r.Loads != 0 {
					t.Errorf("the page was reloaded by %s: %+v", stage, r)
				}
				if !r.Playing {
					t.Errorf("the book stopped at %s", stage)
				}
			}
			tab.Eval(`(() => { const a = document.createElement("a"); a.href = "/admin/libraries"; document.querySelector("#main").append(a); htmx.process(a); a.click(); })()`, nil)
			tab.WaitFor(`document.querySelector("form[action$='/lookups'] button[type=submit]") !== null`, 10*time.Second)
			kept("opening the Libraries page")

			tab.Eval(`document.querySelector("form[action$='/lookups'] button[type=submit]").click()`, nil)
			tab.WaitFor(`document.querySelector(".ts-lookups progress") !== null`, 10*time.Second)
			kept("starting the lookup")

			// The count line moves while books are looked up (about five seconds apart).
			seen := map[string]bool{}
			deadline := time.Now().Add(40 * time.Second)
			for time.Now().Before(deadline) && len(seen) < 3 {
				var line string
				tab.Eval(`(document.querySelector(".ts-lookups .ts-scan__title") || {textContent: ""}).textContent`, &line)
				if strings.HasPrefix(line, "Looking up ") {
					seen[line] = true
				}
				time.Sleep(200 * time.Millisecond)
			}
			if len(seen) < 3 {
				var st, prog, errc string
				_ = e.a.st.Reader().QueryRow(`SELECT state, COALESCE(progress_json,''), COALESCE(error_code,'') FROM jobs WHERE kind='metadata.online_bulk'`).Scan(&st, &prog, &errc)
				t.Errorf("the count line did not move: %v (job %s %s %s)", seen, st, prog, errc)
			}
			kept("watching the progress")

			// Use up today's requests: the job pauses with a plain line and a time.
			day := time.Now().UnixMilli() / int64(24*time.Hour/time.Millisecond)
			streamExec(t, e.a, `INSERT INTO online_usage(day, provider, requests) VALUES (?, 'audnexus', 100000)
			  ON CONFLICT(day, provider) DO UPDATE SET requests = 100000`, day)
			tab.WaitFor(`(document.querySelector(".ts-lookups [role=status]") || {textContent: ""}).textContent.includes("Paused for today")`, 30*time.Second)
			var line string
			tab.Eval(`document.querySelector(".ts-lookups [role=status]").textContent`, &line)
			if !strings.Contains(line, "Continues at ") {
				t.Errorf("pause line: %q", line)
			}
			kept("the pause")

			// Stop answers in place and brings the start button back.
			tab.Eval(`document.querySelector("form[action$='/lookups/stop'] button").click()`, nil)
			tab.WaitFor(`document.querySelector("form[action$='/lookups'] button[type=submit]") !== null && document.querySelector(".ts-lookups progress") === null`, 10*time.Second)
			var stopped string
			tab.Eval(`document.querySelector(".ts-lookups [role=status]").textContent`, &stopped)
			if !strings.Contains(stopped, "The lookup stopped.") {
				t.Errorf("after Stop: %q", stopped)
			}
			kept("Stop")
			var r struct {
				Time     float64
				Overflow bool
			}
			tab.Eval(`({Time: document.querySelector("audio").currentTime, Overflow: document.documentElement.scrollWidth > window.innerWidth})`, &r)
			if r.Time <= before {
				t.Errorf("the book did not advance: %.2f (was %.2f)", r.Time, before)
			}
			if r.Overflow {
				t.Error("the Libraries page overflows sideways")
			}
		})
	}
}
