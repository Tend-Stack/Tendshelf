package web

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

func renderFit(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// checkFitRow: the part of the page holds a row with the fit module and flag, and every collapsible control has one hidden
// twin with the same words, in the row's order (decision 0044).
func checkFitRow(t *testing.T, what, part string, want []string) {
	t.Helper()
	if !strings.Contains(part, `data-ts-module="/static/js/fit-row.js"`) || !strings.Contains(part, "data-ts-fit>") && !strings.Contains(part, "data-ts-fit ") {
		t.Errorf("%s: the row lacks the fit module or data-ts-fit", what)
	}
	items := fitItems(part)
	tw := fitTwins(part)
	if strings.Join(items, ",") != strings.Join(want, ",") {
		t.Fatalf("%s: row items %v, want %v", what, items, want)
	}
	if len(tw) != len(want) {
		t.Fatalf("%s: twins %q for items %v", what, fitKeys(tw), want)
	}
	for i, k := range want {
		if tw[i][0] != k || tw[i][1] == "" {
			t.Errorf("%s: twin %d is %q, want key %q with words", what, i, tw[i], k)
		}
	}
}

// TestRemainingRowsFitTwins: the rows that used to wrap (chapter editor, bookmarks head, duplicate merge, invites) are
// fit rows whose folded buttons each have a twin in the row's own menu.
func TestRemainingRowsFitTwins(t *testing.T) {
	mod := "/static/js/fit-row.js"
	t.Run("chapters", func(t *testing.T) {
		v := pages.ChaptersEditData{FitModule: mod, BookID: "b", Rows: []pages.ChaptersEditRow{
			{Num: 1, PlayAtMs: 0, DeleteOp: "del_0", AddOp: "add_0"},
			{Num: 2, PlayAtMs: -1, DeleteOp: "del_1", AddOp: "add_1"},
		}, DeleteWord: "Delete", AddWord: "Add", ListenWord: "Listen"}
		body := renderFit(t, pages.ChaptersEditView(v))
		rows := strings.Split(body, `class="ts-chapted__row"`)[1:]
		if len(rows) != 2 {
			t.Fatalf("%d chapter rows", len(rows))
		}
		for i, r := range rows {
			checkFitRow(t, "chapter row "+string(rune('1'+i)), r, []string{"delete", "add"})
		}
		if !strings.Contains(body, `ts-chapted__bar ts-oneline`) {
			t.Errorf("the Save bar does not share one line")
		}
	})
	t.Run("bookmarks", func(t *testing.T) {
		v := pages.BookmarksView{FitModule: mod, Heading: "Bookmarks", ExportHref: "/x?sheet=export", ExportLabel: "Share bookmarks", HistoryHref: "/h", HistoryLabel: "History"}
		part := renderFit(t, pages.BookmarksPanel(v))
		part = part[strings.Index(part, "ts-bookmarks__head"):]
		checkFitRow(t, "bookmarks head", part[:strings.Index(part, "</details>")], []string{"history", "export"})
		v.HistoryHref = ""
		part = renderFit(t, pages.BookmarksPanel(v))
		part = part[strings.Index(part, "ts-bookmarks__head"):]
		checkFitRow(t, "bookmarks head without history", part[:strings.Index(part, "</details>")], []string{"export"})
	})
	t.Run("merge", func(t *testing.T) {
		v := pages.AdminDupMergeView{FitModule: mod, Merge: "Merge", Swap: "Swap", SwapHref: "/s", Cancel: "Cancel", CancelHref: "/c"}
		body := renderFit(t, pages.AdminDupMerge(v))
		checkFitRow(t, "duplicate merge", body[strings.Index(body, "ts-dup__actions"):], []string{"swap", "cancel"})
	})
	t.Run("invites", func(t *testing.T) {
		v := pages.UsersView{FitModule: mod, Invites: []pages.InviteRow{{ID: "i1", Name: "Ana", Line: "Ana", Meta: "Member"}, {ID: "i2", Name: "Bo", Line: "Bo", Meta: "Member"}}}
		body := renderFit(t, pages.InvitesList(v))
		items := strings.Split(body, `<li class="ts-invite__item"`)[1:]
		if len(items) != 2 {
			t.Fatalf("%d invite rows", len(items))
		}
		for _, it := range items {
			checkFitRow(t, "invite row", it, []string{"copy", "cancel"})
		}
	})
}
