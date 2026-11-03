//go:build dev

package web

import (
	"fmt"
	"testing"
	"time"
)

// TestCompareBrowser drives "Look up details online" in Chromium (make player) at desktop-1440 and
// mobile-390: open the sheet, start the lookup, wait for the poll to bring the matches, pick one,
// untick a value and watch the count change, apply, and see the Details tab update behind the
// closed sheet. The lookup job is finished by hand; the app never touches the network. Needs
// TENDSHELF_TEST_BROWSER.
func TestCompareBrowser(t *testing.T) {
	b := chromium(t)
	cn := &compareNet{}
	for _, vp := range []struct {
		name   string
		w, h   int
		mobile bool
	}{{"desktop-1440", 1440, 900, false}, {"mobile-390", 390, 844, true}} {
		t.Run(vp.name, func(t *testing.T) {
			app := newTestApp(t, compareMod(t, cn))
			app.owner(t)
			bookID, _, _, _ := bookVisualSeed(t, app)
			base := themedServer(t, app.signedIn(t))
			tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
			tab.Navigate(base + "/books/" + bookID + "/details?theme=dark")
			tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)

			// Open the sheet and start the lookup.
			tab.Eval(`document.querySelector(".ts-book__lookup").click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet form.ts-lookup") !== null`, 5*time.Second)
			tab.Eval(`document.querySelector('#sheet form.ts-lookup button[type="submit"]').click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet #compare-body") !== null`, 5*time.Second)

			// Finish the job; the sheet polls every two seconds and then lists the matches.
			var job string
			if err := app.st.Reader().QueryRow(`SELECT id FROM jobs WHERE kind = 'metadata.online'`).Scan(&job); err != nil {
				t.Fatal(err)
			}
			compareFinishJob(t, app, cn, job)
			tab.WaitFor(`document.querySelector("#sheet .ts-compare-list") !== null`, 8*time.Second)
			tab.Eval(`document.querySelector("#sheet .ts-compare-list a").click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet table.ts-compare") !== null`, 5*time.Second)

			use := func() string {
				var s string
				tab.Eval(`document.getElementById("compare-use").textContent.trim()`, &s)
				return s
			}
			wait := func(want string) {
				t.Helper()
				tab.WaitFor(fmt.Sprintf(`document.getElementById("compare-use").textContent.trim() === %q`, want), 5*time.Second)
			}
			// The book has a publisher and narrators already, so nothing is ticked at first.
			wait("Use 0 changes")
			tab.Eval(`document.querySelector('#sheet input[name="use"][value="publisher"]').click()`, nil)
			wait("Use 1 change")
			tab.Eval(`document.querySelector('#sheet input[name="use"][value="narrators"]').click()`, nil)
			wait("Use 2 changes")
			tab.Eval(`document.querySelector('#sheet input[name="use"][value="narrators"]').click()`, nil)
			wait("Use 1 change")
			if got := use(); got != "Use 1 change" {
				t.Errorf("count = %q", got)
			}

			// Apply: the sheet closes and the Details tab shows the online publisher.
			tab.Eval(`document.getElementById("compare-use").click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet .ts-sheet") === null`, 5*time.Second)
			tab.WaitFor(`document.querySelector(".ts-book__applied") !== null && document.querySelector(".ts-book__details").textContent.includes("Online House")`, 5*time.Second)
			var status string
			tab.Eval(`document.querySelector(".ts-book__applied").textContent.trim()`, &status)
			if status != "Used 1 change." {
				t.Errorf("status = %q", status)
			}
		})
	}
}
