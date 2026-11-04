package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestPaginatorSettings: the five RD2 choices are stored per person, reach the page as classes and data-flow,
// and show up as rows of the sheet. A post that leaves a switch out leaves it alone.
func TestPaginatorSettings(t *testing.T) {
	l := rdrSeed(t)
	def := l.rsetStored(t, l.owner)
	if def.Margin != "normal" || def.Flow != "auto" || def.Justify || !def.Hyphens || def.TapSwap {
		t.Fatalf("defaults %+v", def)
	}
	if code, _, _ := rsetPost(t, l, l.owner, true, "margin", "wide", "flow", "pages", "justify", "0", "justify", "1",
		"hyphens", "0", "tap_swap", "0", "tap_swap", "1"); code != http.StatusNoContent {
		t.Fatalf("post: %d", code)
	}
	got := l.rsetStored(t, l.owner)
	if got.Margin != "wide" || got.Flow != "pages" || !got.Justify || got.Hyphens || !got.TapSwap {
		t.Fatalf("stored %+v", got)
	}
	body := l.get(l.owner, l.readPath(""), false).Body.String()
	for _, want := range []string{`ts-reader--margin-wide`, `ts-reader--justify`, `ts-reader--nohyph`, `ts-reader--tapswap`, `data-flow="pages"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	// Another person keeps the defaults.
	if other := l.rsetStored(t, l.admin); other != users.DefaultReader {
		t.Errorf("another person sees %+v", other)
	}
	// A post about the size alone leaves the switches.
	rsetPost(t, l, l.owner, true, "size", "22")
	if kept := l.rsetStored(t, l.owner); !kept.Justify || kept.Hyphens || !kept.TapSwap || kept.Size != 22 {
		t.Errorf("a size post changed the switches: %+v", kept)
	}
	for _, bad := range [][]string{{"margin", "huge"}, {"flow", "sideways"}} {
		if code, _, _ := rsetPost(t, l, l.owner, true, bad...); code != http.StatusBadRequest {
			t.Errorf("%v: %d", bad, code)
		}
	}
	sheet := l.get(l.owner, l.readPath("sheet=reading"), false).Body.String()
	for _, want := range []string{`Margins`, `Justify text`, `Hyphens`, `Page or scroll`, `Swap tap sides`, `id="rs-margin-wide"`, `id="rs-flow-scroll"`,
		`id="rs-justify"`, `id="rs-hyphens"`, `id="rs-tap_swap"`, `role="switch"`} {
		if !strings.Contains(sheet, want) {
			t.Errorf("sheet misses %q", want)
		}
	}
	if regexp.MustCompile(`id="rs-justify"[^>]*checked`).MatchString(sheet) == false {
		t.Error("the justify switch is not on")
	}
}

// TestPaginatorRoot: what the client script reads from the reader root and the chapter part.
func TestPaginatorRoot(t *testing.T) {
	l := rdrSeed(t)
	body := l.get(l.owner, l.readPath("ch=2"), false).Body.String()
	for _, want := range []string{`data-ts-module="/static/js/reader.`, `data-pages-src="/static/js/reader-pages.`, `data-chapter-count="4"`,
		`data-flow="auto"`, `data-edition="` + l.ed.ID + `"`, `data-chars="`} {
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	m := regexp.MustCompile(` data-copy="([^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no data-copy")
	}
	var copy map[string]string
	if err := json.Unmarshal([]byte(htmlUnescape(m[1])), &copy); err != nil {
		t.Fatalf("data-copy: %v", err)
	}
	for _, k := range []string{"pageOf", "ch", "chAbout", "bk", "bkAbout", "further", "go", "stay"} {
		if copy[k] == "" {
			t.Errorf("data-copy has no %q", k)
		}
	}
	if copy["further"] != "You read further on %s." || copy["go"] != "Go there" || copy["stay"] != "Stay here" {
		t.Errorf("data-copy words %v", copy)
	}
	if strings.Contains(body, ` style="`) {
		t.Error("an inline style in the reader page")
	}
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&#34;", `"`, "&quot;", `"`, "&amp;", "&", "&#39;", "'", "&lt;", "<", "&gt;", ">").Replace(s)
}

// TestPaginatorEnd: the last part of the book carries "That's the end of the book." and a form; marking the book
// finished writes the reading place only.
func TestPaginatorEnd(t *testing.T) {
	l := rdrSeed(t)
	frag := func(idx string) string {
		rec := l.get(l.owner, "/hx/books/"+l.book.ID+"/read/chapters/"+idx+"?edition="+l.ed.ID, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("chapter %s: %d", idx, rec.Code)
		}
		return rec.Body.String()
	}
	if strings.Contains(frag("2"), "ts-reader__end") {
		t.Error("the end block is on a chapter that is not the last")
	}
	last := frag("4")
	action := "/reading/" + l.book.ID + "/finished"
	for _, want := range []string{`class="ts-reader__end"`, "That&#39;s the end of the book.", `action="` + action + `"`, `name="finished" value="1"`, "Mark as finished", `name="_csrf"`} {
		if !strings.Contains(last, want) {
			t.Errorf("the last chapter misses %q", want)
		}
	}
	if strings.Contains(last, "ts-reader__next") {
		t.Error("the last chapter still asks for a next one")
	}
	post := func(ck *http.Cookie, form url.Values, htmx bool) *http.Response {
		if form.Get("_csrf") == "" {
			form.Set("_csrf", l.a.tokenFor(t, ck))
		}
		rec := l.a.do(http.MethodPost, action, form, func(r *http.Request) {
			if htmx {
				r.Header.Set("HX-Request", "true")
			}
		}, ck)
		return rec.Result()
	}
	if res := post(l.owner, url.Values{"finished": {"2"}}, true); res.StatusCode != http.StatusBadRequest {
		t.Errorf("a bad value: %d", res.StatusCode)
	}
	if res := post(l.owner, url.Values{"finished": {"1"}}, false); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/books/"+l.book.ID {
		t.Errorf("plain post: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	p, found, err := l.a.srv.progress.Reading(context.Background(), l.ownerID, l.book.ID)
	if err != nil || !found || !p.Finished() || p.PercentBP != 10000 {
		t.Errorf("the reading place after finishing: %+v %v %v", p, found, err)
	}
	if pr, err := l.a.srv.progress.Get(context.Background(), l.ownerID, l.book.ID); !errors.Is(err, progress.ErrNotFound) || pr.ClientUpdatedAt != 0 {
		t.Errorf("finishing the text touched the listening place: %+v %v", pr, err)
	}
	// Read again: 0 puts it back to the start.
	post(l.owner, url.Values{"finished": {"0"}}, true)
	if p, _, _ := l.a.srv.progress.Reading(context.Background(), l.ownerID, l.book.ID); p.Finished() {
		t.Error("finished=0 did not undo")
	}
	// Someone without the right to read gets nothing; another person's place is untouched.
	if res := post(l.noRead, url.Values{"finished": {"1"}}, true); res.StatusCode != http.StatusNotFound {
		t.Errorf("a member without can_read: %d", res.StatusCode)
	}
	if _, found, _ := l.a.srv.progress.Reading(context.Background(), sweepUserID(t, l.a, l.reader), l.book.ID); found {
		t.Error("another person got a reading place")
	}
	if res := post(l.owner, url.Values{"finished": {"1"}, "_csrf": {"wrong"}}, true); res.StatusCode != http.StatusForbidden {
		t.Errorf("a wrong token: %d", res.StatusCode)
	}
}
