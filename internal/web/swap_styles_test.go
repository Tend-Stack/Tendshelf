package web

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// htmx drops the <head> of a boosted answer, so a page's own stylesheets must sit inside <main
// id="main"> (what a boosted swap keeps), and a sheet must carry its own inside its root element
// (what hx-select="#sheet > *" keeps). These tests pin that for every page that has page styles.

var swapPageSheet = regexp.MustCompile(`<link rel="stylesheet" href="/static/css/(pages-[a-z]+)\.[0-9a-f]{10}\.css"/?>`)
var swapPagePrefetch = regexp.MustCompile(`<link rel="prefetch" as="style" href="/static/css/(pages-[a-z]+)\.[0-9a-f]{10}\.css"/?>`)

// swapNames lists the page stylesheet names matched in s, in order.
func swapNames(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestPageStylesTravelInMain(t *testing.T) {
	f := bookmarkEnv(t)
	shelf := shelfMake(t, f.a, f.owner, "Sleep")
	book := "/books/" + f.book.ID
	bm := bookmarkAdd(t, f, f.ownerID, f.book.ID, 100_000, "A note", time.Now())

	browse := []string{"pages-admin", "pages-home", "pages-library"}
	bookStyles := []string{"pages-home", "pages-library", "pages-book", "pages-shelves", "pages-bookmarks"}
	adminStyles := []string{"pages-onboard", "pages-admin", "pages-settings"}
	cases := []struct {
		path string
		want []string
	}{
		{"/", browse},
		{"/libraries", browse},
		{"/search", []string{"pages-home", "pages-library", "pages-search"}},
		{"/shelves", []string{"pages-home", "pages-library", "pages-shelves"}},
		{"/shelves/" + shelf, []string{"pages-home", "pages-library", "pages-shelves"}},
		{"/shelves/" + shelf + "/delete", []string{"pages-home", "pages-library", "pages-shelves"}},
		{"/me", []string{"pages-home", "pages-library", "pages-shelves", "pages-me", "pages-profile"}},
		{"/me/settings", []string{"pages-me", "pages-settings"}},
		{"/me/password", []string{"pages-onboard", "pages-me", "pages-settings"}},
		{"/admin/libraries", adminStyles},
		{"/admin/libraries/folders/new", slices.Insert(slices.Clone(adminStyles), 2, "pages-folders")},
		// The book page links the styles of its tabs and sheets whatever tab it opens on, because the
		// tabs swap only #book-panel.
		{book, bookStyles},
		{book + "/chapters", bookStyles},
		{book + "/bookmarks", bookStyles},
		{book + "/details", bookStyles},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			body := browseGet(f.a, c.path, false, f.owner).Body.String()
			open := strings.Index(body, `<main id="main"`)
			end := strings.Index(body, "</main>")
			if open < 0 || end < open {
				t.Fatal("no <main id=\"main\">")
			}
			head, main := body[:open], body[open:end]
			if got := swapNames(swapPageSheet, main); !slices.Equal(got, c.want) {
				t.Errorf("stylesheets inside #main = %v, want %v", got, c.want)
			}
			if got := swapNames(swapPageSheet, head); len(got) != 0 {
				t.Errorf("page stylesheets in the head, where a swap loses them: %v", got)
			}
			// The first thing in #main is the links (before the content, so nothing shows unstyled).
			if !regexp.MustCompile(`^<main id="main"[^>]*>\s*<link rel="stylesheet"`).MatchString(main) {
				t.Error("the stylesheet links are not the first thing in #main")
			}
			// Every other page stylesheet is prefetched from the head, once.
			prefetched := swapNames(swapPagePrefetch, head)
			for _, n := range []string{"pages-search", "pages-shelves", "pages-book", "pages-bookmarks", "pages-me", "pages-settings", "pages-folders"} {
				if slices.Contains(c.want, n) != !slices.Contains(prefetched, n) {
					t.Errorf("%s: in #main %v, prefetched %v", n, slices.Contains(c.want, n), slices.Contains(prefetched, n))
				}
			}
			if !strings.Contains(head, `/static/css/player.`) {
				t.Error("the player stylesheet is not in the head")
			}
		})
	}

	// Every sheet carries its own stylesheet as the first child of its root element, which is what
	// hx-select="#sheet > *" swaps in.
	sheets := []struct{ name, path, want string }{
		{"shelf sheet", book + "?sheet=shelves", "pages-shelves"},
		{"note sheet", book + "/bookmarks?sheet=bookmark&bookmark=" + bm.ID, "pages-bookmarks"},
		{"export sheet", book + "/bookmarks?sheet=export", "pages-bookmarks"},
		{"folder browser", "/admin/libraries/folders/new?browse=" + t.TempDir(), "pages-folders"},
	}
	for _, c := range sheets {
		t.Run(c.name, func(t *testing.T) {
			body := browseGet(f.a, c.path, true, f.owner).Body.String()
			i := strings.Index(body, `id="sheet"`)
			if i < 0 {
				t.Fatal("no #sheet")
			}
			root := regexp.MustCompile(`^[^>]*>\s*<div class="ts-sheet-layer[^"]*">\s*<link rel="stylesheet" href="/static/css/(pages-[a-z]+)\.[0-9a-f]{10}\.css"/?>`)
			m := root.FindStringSubmatch(body[i:])
			if m == nil {
				t.Fatalf("the sheet does not start with its own stylesheet link: %.300s", body[i:])
			}
			if m[1] != c.want {
				t.Errorf("sheet links %s, want %s", m[1], c.want)
			}
		})
	}
}
