package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// Views for Home and the Library: a library.BookCard becomes a components.CardView, and the query
// string becomes a browseQuery that can write itself back as an address.

// The two inks a tinted spine or blank cover can carry. They equal --brand-paper and --brand-walnut
// in tokens.css (a test keeps them equal), so the contrast decided here is the contrast on screen.
const (
	spinePaper  = "#e9e0d0"
	spineWalnut = "#5a4029"
	// minInkContrast is WCAG AA for text.
	minInkContrast = 4.5
)

// spineInk picks the ink for a tint: paper or walnut, whichever contrasts more, if that reaches 4.5:1.
// With neither, the tint is not used and the spine keeps the surface and ink colours.
func spineInk(tint string) (class string, ok bool) {
	paper, walnut := media.Contrast(tint, spinePaper), media.Contrast(tint, spineWalnut)
	best, class := paper, "ts-ink-paper"
	if walnut > paper {
		best, class = walnut, "ts-ink-walnut"
	}
	if best < minInkContrast {
		return "", false
	}
	return class, true
}

// spineWidth is the spine width class for a length: under 5 hours narrow, under 15 medium, else wide.
func spineWidth(durationMs int64) string {
	switch {
	case durationMs < 5*3_600_000:
		return "ts-spine--ws"
	case durationMs < 15*3_600_000:
		return "ts-spine--wm"
	}
	return "ts-spine--wl"
}

// spineHeight is one of three height classes, picked from the last character of the id so a book
// always stands as tall as it did.
func spineHeight(id string) string {
	if id == "" {
		return "ts-spine--h2"
	}
	return "ts-spine--h" + strconv.Itoa(int(id[len(id)-1])%3+1)
}

// bylineOf writes the authors for a card: the first two, then "and {n} more".
func bylineOf(authors []library.PersonRef) string {
	var names []string
	for _, a := range authors {
		if len(names) == 2 {
			break
		}
		names = append(names, a.Name)
	}
	out := strings.Join(names, ", ")
	if n := len(authors) - len(names); n > 0 {
		out += " and " + strconv.Itoa(n) + " more"
	}
	return out
}

// cardView turns a book into what the cards, rows and spines draw.
func cardView(c library.BookCard) components.CardView {
	v := components.CardView{
		ID: c.ID, Href: "/books/" + c.ID, Title: c.Title, Byline: bylineOf(c.Authors),
		Duration: durationText(c.DurationMs), AriaLabel: c.Title,
		WidthClass: spineWidth(c.DurationMs), HeightClass: spineHeight(c.ID),
	}
	if v.Byline != "" {
		v.AriaLabel = c.Title + " by " + v.Byline
	}
	if c.Cover == nil {
		return v
	}
	if c.Cover.SHA != "" {
		v.CoverSrc = coverURL(c.ID, c.Cover.SHA, 320)
		v.CoverSrcset = coverURL(c.ID, c.Cover.SHA, 160) + " 160w, " + v.CoverSrc + " 320w, " + coverURL(c.ID, c.Cover.SHA, 640) + " 640w"
		v.CoverW, v.CoverH = c.Cover.Width, c.Cover.Height
	}
	if ink, ok := spineInk(c.Cover.Tint); ok {
		v.Tint, v.InkClass = c.Cover.Tint, ink
	}
	return v
}

// Limits of a list address.
const (
	browsePageSize  = 48
	browseMaxGenres = 5
	browseMaxGenreN = 100 // runes
)

// browseQuery is what a list address says: the order, the view, the genres, the page position and
// whether the filter sheet is open. Unknown values fall back to the defaults.
type browseQuery struct {
	Sort   library.Sort
	View   string // "grid" or "list"
	Genres []string
	Cursor string
	Sheet  bool
}

func parseBrowseQuery(r *http.Request) browseQuery {
	v := r.URL.Query()
	q := browseQuery{Sort: library.ParseSort(v.Get("sort")), View: "grid", Cursor: v.Get("cursor"), Sheet: v.Get("sheet") == "filters"}
	if v.Get("view") == "list" {
		q.View = "list"
	}
	seen := map[string]bool{}
	for _, g := range v["genre"] {
		g = strings.TrimSpace(g)
		if g == "" || utf8.RuneCountInString(g) > browseMaxGenreN || seen[g] {
			continue
		}
		if len(q.Genres) == browseMaxGenres {
			break
		}
		seen[g] = true
		q.Genres = append(q.Genres, g)
	}
	return q
}

func (q browseQuery) hasGenre(g string) bool {
	for _, x := range q.Genres {
		if x == g {
			return true
		}
	}
	return false
}

// href writes path with the query, after mod changed a copy of it. Defaults are left out so the
// canonical address stays short. The order of the parameters is fixed.
func (q browseQuery) href(path string, mod func(*browseQuery)) string {
	if mod != nil {
		q.Genres = append([]string(nil), q.Genres...)
		mod(&q)
	}
	v := url.Values{}
	if q.View == "list" {
		v.Set("view", "list")
	}
	if q.Sort != library.SortAdded {
		v.Set("sort", string(q.Sort))
	}
	for _, g := range q.Genres {
		v.Add("genre", g)
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Sheet {
		v.Set("sheet", "filters")
	}
	if enc := v.Encode(); enc != "" {
		return path + "?" + enc
	}
	return path
}

// without drops genre g.
func (q *browseQuery) without(g string) {
	var out []string
	for _, x := range q.Genres {
		if x != g {
			out = append(out, x)
		}
	}
	q.Genres = out
}
