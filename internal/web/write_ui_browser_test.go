//go:build dev

package web

import (
	"strings"
	"testing"
	"time"
)

// TestWriteUIBrowser drives writing details in Chromium at desktop-1440 and mobile-390: open the
// sheet from the Details tab, start the write, wait for the poll to show the two choices, choose
// Undo and see the file restored, then write again and choose Looks good. The jobs run for real on
// the fake ffmpeg. Needs TENDSHELF_TEST_BROWSER.
func TestWriteUIBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range []struct {
		name   string
		w, h   int
		mobile bool
	}{{"desktop-1440", 1440, 900, false}, {"mobile-390", 390, 844, true}} {
		t.Run(vp.name, func(t *testing.T) {
			l := newWriteLab(t, 1, false)
			l.start(t)
			before := fileDigest(t, l.files[0])
			base := themedServer(t, l.a.signedIn(t))
			tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
			tab.Navigate(base + "/books/" + l.book.ID + "/details?theme=dark")
			tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)

			// startWrite opens the sheet, confirms, and waits until the poll shows the two choices.
			startWrite := func() {
				tab.WaitFor(`document.querySelector(".ts-book__write") !== null`, 5*time.Second)
				tab.Eval(`document.querySelector(".ts-book__write").click()`, nil)
				tab.WaitFor(`document.querySelector("#sheet form.ts-write__form") !== null`, 5*time.Second)
				var files string
				tab.Eval(`document.querySelector("#sheet").textContent`, &files)
				if !strings.Contains(files, "01.m4b") || !strings.Contains(files, writeNote) {
					t.Errorf("sheet text = %q, want the file and the note", files)
				}
				tab.Eval(`document.querySelector('#sheet form.ts-write__form button[type="submit"]').click()`, nil)
				tab.WaitFor(`document.querySelector("#sheet #write-body") !== null`, 8*time.Second)
				tab.WaitFor(`document.querySelector("#sheet .ts-write__pending") !== null`, 20*time.Second)
			}

			startWrite()
			if fileDigest(t, l.files[0]) == before {
				t.Error("the file was not rewritten")
			}
			tab.Eval(`[...document.querySelectorAll("#sheet .ts-write__actions button")].find(b => b.textContent.trim() === "Undo").click()`, nil)
			tab.WaitFor(`document.body.textContent.includes("Put the files back as they were.")`, 8*time.Second)
			if fileDigest(t, l.files[0]) != before {
				t.Error("Undo did not restore the file")
			}

			startWrite()
			tab.Eval(`[...document.querySelectorAll("#sheet .ts-write__actions button")].find(b => b.textContent.trim() === "Looks good").click()`, nil)
			tab.WaitFor(`document.body.textContent.includes("Kept the new details.")`, 8*time.Second)
			if fileDigest(t, l.files[0]) == before {
				t.Error("the kept file went back to the original")
			}
			var pending bool
			tab.Eval(`document.querySelector(".ts-write__pending") !== null`, &pending)
			if pending {
				t.Error("the waiting panel is still shown after Looks good")
			}
		})
	}
}
