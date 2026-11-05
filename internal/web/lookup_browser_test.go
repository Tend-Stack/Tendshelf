//go:build dev

package web

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Look-up in Chromium (M5 G1). Needs TENDSHELF_TEST_BROWSER.

func TestDefineBrowser(t *testing.T) {
	b := chromium(t)
	l := pgSeedEPUB(t, pgLongChapter(), nil)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	sid := l.sids()[1]
	tab := pgOpen(t, b, srv, l, 390, 844, true, "at="+sid)
	open := `document.querySelector("dialog.ts-lookup[open]")`

	// Select a word of the sentence; the dictionary has it (the folder is read when the popup asks).
	rnSelect(tab, sid, 0, 4)
	var word string
	tab.Eval(`getSelection().toString().trim().replace(/^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$/gu, "")`, &word)
	if word == "" {
		t.Fatal("nothing selected")
	}
	lkDict(t, l, "Browser words", word+"\tA defined thing.\n")

	// A copy leaves the page as it was: no highlight, no word.
	tab.Eval(`document.querySelector('#reader-marks [data-ts-mark="copy"]').click()`, nil)
	if hls, _ := l.a.srv.progress.Highlights(context.Background(), l.ownerID, l.book.ID); len(hls) != 0 {
		t.Fatalf("Copy wrote %d highlights", len(hls))
	}

	// Look up: the popup opens with the definition, focus inside; Escape closes it and focus goes back to the text.
	rnSelect(tab, sid, 0, 4)
	tab.Eval(`document.querySelector('#reader-marks [data-ts-mark="lookup"]').click()`, nil)
	tab.WaitFor(open+` !== null`, 8*time.Second)
	var got struct {
		Text, Link string
		Inside     bool
	}
	tab.Eval(`({Text: `+open+`.textContent, Link: `+open+`.querySelector("a[target=_blank]").href, Inside: !!document.activeElement.closest("dialog")})`, &got)
	if !strings.Contains(got.Text, "A defined thing.") || !strings.Contains(got.Link, "https://duckduckgo.com/?q=") || !got.Inside {
		t.Errorf("popup: %+v", got)
	}
	pgKey(t, tab, "Escape", "Escape", 27, "", 0)
	tab.WaitFor(open+` === null`, 5*time.Second)
	tab.WaitFor(`document.activeElement.classList.contains("ts-s")`, 3*time.Second) // focus returns to the text

	// The backdrop closes it too.
	rnSelect(tab, sid, 0, 4)
	tab.Eval(`document.querySelector('#reader-marks [data-ts-mark="lookup"]').click()`, nil)
	tab.WaitFor(open+` !== null`, 8*time.Second)
	tab.Eval(`document.querySelector("dialog.ts-lookup").click()`, nil)
	tab.WaitFor(open+` === null`, 5*time.Second)

	// Save word: a toast says so, and the word is stored with its place.
	rnSelect(tab, sid, 0, 4)
	tab.Eval(`document.querySelector('#reader-marks [data-ts-mark="lookup"]').click()`, nil)
	tab.WaitFor(open+` !== null`, 8*time.Second)
	tab.Eval(`document.querySelector("dialog.ts-lookup [data-save]").click()`, nil)
	tab.WaitFor(`document.getElementById("toasts").textContent.includes("Word saved.")`, 8*time.Second)
	tab.WaitFor(open+` === null`, 5*time.Second)
	var sid2 string
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*), coalesce(max(sid), '') FROM word_list WHERE user_id = ?`, l.ownerID).Scan(&n, &sid2); err != nil || n != 1 || sid2 != sid {
		t.Errorf("stored words = %d at %q, %v; want one at %s", n, sid2, err, sid)
	}
	if hls, _ := l.a.srv.progress.Highlights(context.Background(), l.ownerID, l.book.ID); len(hls) != 0 {
		t.Errorf("Look up wrote %d highlights", len(hls))
	}
}
