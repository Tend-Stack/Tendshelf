//go:build dev

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestReaderSettingsBrowser drives the reading settings sheet in Chromium (make player) at desktop-1440 and
// mobile-390: open the sheet from Aa, change the text size, the highlight style, the font and the theme, see
// the page behind change without a reload, then reload and see the choices kept. It also checks the 44 px
// targets and that OpenDyslexic downloads only once chosen. Needs TENDSHELF_TEST_BROWSER.
func TestReaderSettingsBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range []struct {
		name   string
		w, h   int
		mobile bool
	}{{"desktop-1440", 1440, 900, false}, {"mobile-390", 390, 844, true}} {
		t.Run(vp.name, func(t *testing.T) {
			l := rdrSeed(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.AddCookie(l.owner)
				l.a.h.ServeHTTP(w, r)
			}))
			t.Cleanup(srv.Close)
			tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
			tab.Navigate(srv.URL + l.readPath("mode=read"))
			tab.WaitFor(`window.htmx !== undefined && document.getElementById("reader") !== null`, 5*time.Second)
			// A marker so a reload would be noticed.
			tab.Eval(`window.__noReload = true`, nil)

			tab.Eval(`document.querySelector(".ts-aa").click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet form[data-ts-reader-settings]") !== null`, 5*time.Second)
			// The script mounts from the marker on the page; wait until a change is applied before going on.
			click := func(id string) { tab.Eval(`document.getElementById("`+id+`").click()`, nil) }

			click("rs-size-22")
			tab.WaitFor(`document.getElementById("reader").classList.contains("ts-reader--size-22")`, 5*time.Second)
			click("rs-hl-underline")
			tab.WaitFor(`document.getElementById("reader").dataset.hl === "underline"`, 5*time.Second)
			click("rs-leading-airy")
			tab.WaitFor(`document.getElementById("reader").classList.contains("ts-reader--leading-airy")`, 5*time.Second)
			click("rs-theme-light")
			tab.WaitFor(`document.documentElement.dataset.theme === "light"`, 5*time.Second)

			// OpenDyslexic has not been asked for yet.
			var loaded bool
			tab.Eval(`performance.getEntriesByType("resource").some(e => e.name.includes("OpenDyslexic"))`, &loaded)
			if loaded {
				t.Error("OpenDyslexic downloaded before it was chosen")
			}
			click("rs-font-dyslexic")
			tab.WaitFor(`document.getElementById("reader").classList.contains("ts-reader--dyslexic")`, 5*time.Second)
			tab.WaitFor(`performance.getEntriesByType("resource").some(e => e.name.includes("OpenDyslexic"))`, 8*time.Second)

			// 44 px targets and no horizontal overflow in the sheet.
			var small []string
			tab.Eval(`[...document.querySelectorAll("#sheet label.ts-seg__item, #sheet label.ts-theme, #sheet .ts-switch-row")]
				.filter(el => el.getBoundingClientRect().height < 43.5).map(el => el.textContent.trim() || el.id)`, &small)
			if len(small) > 0 {
				t.Errorf("targets under 44 px: %v", small)
			}
			var over bool
			tab.Eval(`document.documentElement.scrollWidth > innerWidth`, &over)
			if over {
				t.Error("horizontal overflow")
			}
			var kept bool
			tab.Eval(`window.__noReload === true`, &kept)
			if !kept {
				t.Error("the page reloaded")
			}

			// The choices were saved: wait for the last post, then reload.
			deadline := time.Now().Add(5 * time.Second)
			for l.rsetStored(t, l.owner).Font != "dyslexic" && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
			rs := l.rsetStored(t, l.owner)
			if rs.Size != 22 || rs.Highlight != "underline" || rs.Leading != "airy" || rs.Font != "dyslexic" {
				t.Fatalf("stored %+v", rs)
			}
			tab.Navigate(srv.URL + l.readPath("mode=read"))
			tab.WaitFor(`document.getElementById("reader") !== null`, 5*time.Second)
			var ok bool
			tab.Eval(`(() => { const r = document.getElementById("reader");
				return r.classList.contains("ts-reader--size-22") && r.classList.contains("ts-reader--dyslexic") && r.dataset.hl === "underline"
					&& document.documentElement.dataset.theme === "light"; })()`, &ok)
			if !ok {
				t.Error("choices not kept after reload")
			}
		})
	}
}
