package web

import (
	"html"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

// moreMenuItems returns the labels in the page's "More options" menu, and whether the button is there.
func moreMenuItems(body string) ([]string, bool) {
	i := strings.Index(body, `class="ts-chipmenu ts-moremenu`)
	if i < 0 {
		return nil, false
	}
	j := strings.Index(body[i:], "</details>")
	var out []string
	for _, m := range regexp.MustCompile(`class="ts-chipmenu__item"[^<]*>([^<>]+)</a>`).FindAllStringSubmatch(body[i:i+j], -1) {
		out = append(out, m[1])
	}
	return out, true
}

// TestBookMoreMenuItems: the three-dot menu holds only what the person may use, and no item means no button.
func TestBookMoreMenuItems(t *testing.T) {
	l := newFeedLab(t)
	cases := []struct {
		who, book string
		want      []string
		button    bool
	}{
		{"owner", l.multi.ID, []string{"Go to series", "Share", "Podcast feed"}, true},
		{"owner", l.single.ID, []string{"Share", "Podcast feed"}, true},
		// A member with the download right: the feed (and the series page), nothing that edits or shares.
		{"m_dl", l.multi.ID, []string{"Go to series", "Podcast feed"}, true},
		{"m_dl", l.single.ID, []string{"Podcast feed"}, true},
		// No download right and no series: nothing to put in it, so no button.
		{"m_nodl", l.single.ID, nil, false},
		// No download right but a series: only the series page.
		{"m_nodl", l.multi.ID, []string{"Go to series"}, true},
	}
	for _, c := range cases {
		code, body := l.page(c.who, "/books/"+c.book)
		if code != http.StatusOK {
			t.Errorf("%s /books/%s = %d", c.who, c.book, code)
			continue
		}
		got, button := moreMenuItems(bookMain(body))
		if button != c.button || strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s on %s: menu %v (button %v), want %v (button %v)", c.who, c.book, got, button, c.want, c.button)
		}
		if button && !strings.Contains(body, `aria-label="More options"`) {
			t.Errorf("%s: the button lacks its name", c.who)
		}
		if strings.Contains(body, "ts-book__feed") {
			t.Errorf("%s: a loose feed link is still on the page", c.who)
		}
		if strings.Contains(body, "ts-book__cover-change") {
			t.Errorf("%s: a Change cover link is under the cover again", c.who)
		}
	}
}

// TestBookPlayFragmentKeepsMenu: the #book-play region re-read after a save carries the whole menu, with the
// series page, so the button does not lose items after a swap.
func TestBookPlayFragmentKeepsMenu(t *testing.T) {
	l := newFeedLab(t)
	_, body := l.page("owner", "/hx/books/"+l.multi.ID+"/play")
	got, button := moreMenuItems(body)
	if !button || strings.Join(got, "|") != "Go to series|Share|Podcast feed" {
		t.Errorf("fragment menu %v (button %v)", got, button)
	}
}

// TestSeriesPageMoreMenu: the series page's Podcast feed link moved into the same menu.
func TestSeriesPageMoreMenu(t *testing.T) {
	l := newFeedLab(t)
	_, body := l.page("m_dl", "/series/"+l.seriesID)
	got, button := moreMenuItems(bookMain(body))
	if !button || strings.Join(got, "|") != "Podcast feed" {
		t.Errorf("series menu %v (button %v)", got, button)
	}
	if strings.Contains(bookMain(body), `ts-btn--quiet" href="/series/`+l.seriesID+`/feed`) {
		t.Error("a loose Podcast feed link is still on the series page")
	}
}

// TestBookMoreMenuEditItems: Change cover and Edit details are in the menu for a person who may edit the book, with the
// sheet's own attributes, and absent for a member who may only listen.
func TestBookMoreMenuEditItems(t *testing.T) {
	l := newCoverLab(t)
	svc := metadata.New(l.a.lib, l.a.runner, slog.New(slog.NewTextHandler(io.Discard, nil)))
	l.a.srv.details = svc
	path := "/books/" + l.book.ID
	rec := browseGet(l.a, path, false, l.owner)
	got, button := moreMenuItems(bookMain(rec.Body.String()))
	if !button || strings.Join(got, "|") != "Share|Change cover|Edit details" {
		t.Errorf("owner menu %v (button %v)", got, button)
	}
	body := html.UnescapeString(rec.Body.String())
	for _, want := range []string{`aria-haspopup="dialog"`, `hx-get="` + coverSheetHref(l.book.ID, "") + `"`, `href="` + detailsTabHref(l.book.ID) + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("owner page lacks %s", want)
		}
	}
	rec = browseGet(l.a, path, false, l.member)
	got, button = moreMenuItems(bookMain(rec.Body.String()))
	if button {
		t.Errorf("a member who only listens sees a menu: %v", got)
	}
}
