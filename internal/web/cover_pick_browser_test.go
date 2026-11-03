//go:build dev

package web

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestCoverPickBrowser drives the cover picker in Chromium at desktop-1440 and mobile-390: open
// "Change cover", choose a synthetic PNG, add it with the plain upload form, wait for the poll to
// report the job done, and see the new cover in use. The jobs run for real on the fake ffmpeg and
// nothing touches the network. Needs TENDSHELF_TEST_BROWSER.
func TestCoverPickBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range []struct {
		name   string
		w, h   int
		mobile bool
	}{{"desktop-1440", 1440, 900, false}, {"mobile-390", 390, 844, true}} {
		t.Run(vp.name, func(t *testing.T) {
			l := newCoverLab(t)
			l.start(t)
			base := themedServer(t, l.a.signedIn(t))
			tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
			tab.Navigate(base + "/books/" + l.book.ID + "/details?theme=dark")
			tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)

			tab.Eval(`document.querySelector(".ts-book__cover-change").click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet form.ts-cover-pick__upload") !== null`, 5*time.Second)
			// Put the synthetic picture in the file input the way a person's choice would.
			png := base64.StdEncoding.EncodeToString(coverPNG(t))
			tab.Eval(fmt.Sprintf(`(() => {
				const bytes = Uint8Array.from(atob(%q), c => c.charCodeAt(0));
				const dt = new DataTransfer();
				dt.items.add(new File([bytes], "new-cover.png", {type: "image/png"}));
				document.querySelector('#sheet input[type="file"]').files = dt.files;
			})()`, png), nil)
			tab.Eval(`document.querySelector('#sheet form.ts-cover-pick__upload button[type="submit"]').click()`, nil)
			// The form posts, the server answers with the sheet and the waiting line, and the poll replaces
			// the body with the finished one.
			tab.WaitFor(`document.querySelector("#sheet #cover-body") !== null`, 8*time.Second)
			tab.WaitFor(`document.querySelector("#sheet #cover-body .ts-cover-pick__status") !== null
				&& document.querySelector("#sheet #cover-body").textContent.includes("The new cover is in place.")`, 15*time.Second)
			var cards int
			tab.Eval(`document.querySelectorAll("#sheet .ts-cover-pick__card").length`, &cards)
			if cards != 2 {
				t.Errorf("cover cards = %d, want 2 (the folder cover and the upload)", cards)
			}
			var checked string
			tab.Eval(`document.querySelector('#sheet input[name="cover_id"]:checked').closest("label").textContent`, &checked)
			if want := coverFromUpload; !strings.Contains(checked, want) {
				t.Errorf("checked card = %q, want it to say %q", checked, want)
			}
			if got := l.bookCover(t, l.book.ID); got == l.cover {
				t.Error("the book's cover did not change")
			}
		})
	}
}
