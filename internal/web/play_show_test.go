package web

import (
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
	for _, w := range []string{"Show: Finished", "Zed Done", `href="/libraries?show=unfinished"`, `href="/libraries?show=not_started"`, "Clear filters", `href="/libraries"`} {
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
	// The sheet carries the group.
	sheet := playBtnGet(e, "/libraries?sheet=filters&show=not_started")
	for _, w := range []string{`name="show" value="unfinished"`, `value="not_started" checked`, `name="show" value="finished"`, `>Show</legend>`} {
		if !strings.Contains(sheet, w) {
			t.Errorf("filter sheet lacks %s", w)
		}
	}
	// An unknown value is ignored, like length.
	if body := playBtnGet(e, "/libraries?show=bogus"); !strings.Contains(body, "Progword Harbour") || !strings.Contains(body, "Zed Done") || strings.Contains(body, "Clear filters") {
		t.Error("an unknown show value filtered the list")
	}
}
