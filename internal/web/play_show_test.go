package web

import (
	"html"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

func TestPlayShowFilterChips(t *testing.T) {
	e := playBtnEnv(t)
	now := e.nowMs.Load()
	done := playBtnBook(t, e, librarytest.BookSpec{Title: "Zed Done", Authors: []string{"Zoe"}})
	playBtnProgress(t, e, e.ownerID, done.ID, 100_000, now, now)
	playBtnProgress(t, e, e.ownerID, e.book.ID, 50_000, 0, now)

	body := playBtnGet(e, "/libraries?show=finished")
	for _, w := range []string{"Show: Finished", "Zed Done", `href="/libraries?show=unfinished"`, `href="/libraries?show=not_started"`, "Clear all", `href="/libraries"`} {
		if !strings.Contains(body, w) {
			t.Errorf("show=finished lacks %q", w)
		}
	}
	if strings.Contains(body, "Progword Harbour") {
		t.Error("an unfinished book is listed under Finished")
	}
	if body := playBtnGet(e, "/libraries?show=unfinished"); !strings.Contains(body, "Progword Harbour") || strings.Contains(body, "Zed Done") {
		t.Error("show=unfinished lists the wrong books")
	}
	// The Show menu carries the group.
	menu := html.UnescapeString(playBtnGet(e, "/libraries?show=not_started"))
	for _, w := range []string{`href="/libraries?show=unfinished"`, `href="/libraries?show=finished"`, `href="/libraries?show=not_started" aria-current="true"`, `>Show</p>`} {
		if !strings.Contains(menu, w) {
			t.Errorf("Show menu lacks %s", w)
		}
	}
	// An unknown value is ignored, like length.
	if body := playBtnGet(e, "/libraries?show=bogus"); !strings.Contains(body, "Progword Harbour") || !strings.Contains(body, "Zed Done") || strings.Contains(body, "Clear all") {
		t.Error("an unknown show value filtered the list")
	}
}
