package web

import (
	"fmt"
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

// The two inks a tinted spine or blank cover can carry. They equal --brand-paper and --spine-ink in
// tokens.css (a test keeps them equal), so the contrast decided here is the contrast on screen. The
// dark one is the boards' deep brown, not --brand-walnut: walnut is too light for mid-tone covers.
const (
	spinePaper = "#e9e0d0"
	spineDeep  = "#2a1a0c"
	// minInkContrast is WCAG AA for text.
	minInkContrast = 4.5
)

// spineInk picks the colours of a tinted spine or blank cover: the cover's tint with paper or the
// deep brown, whichever contrasts more, if that reaches 4.5:1. A mid-tone tint that reaches it with
// neither (a bright magenta) is darkened in small steps, at most by 40 %, until paper does, so the
// spine keeps its hue. With no such step the tint is not used and the spine keeps the surface and
// ink colours.
func spineInk(tint string) (shown, class string, ok bool) {
	paper, deep := media.Contrast(tint, spinePaper), media.Contrast(tint, spineDeep)
	switch {
	case deep >= paper && deep >= minInkContrast:
		return tint, "ts-ink-deep", true
	case paper >= minInkContrast:
		return tint, "ts-ink-paper", true
	case paper == 0 && deep == 0:
		return "", "", false // not a colour
	}
	for step := 1; step <= 8; step++ {
		dark, ok := darkenHex(tint, float64(step)*0.05)
		if ok && media.Contrast(dark, spinePaper) >= minInkContrast {
			return dark, "ts-ink-paper", true
		}
	}
	return "", "", false
}

// darkenHex mixes a "#rrggbb" colour with black by the fraction f.
func darkenHex(hex string, f float64) (string, bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return "", false
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return "", false
	}
	scale := func(c uint64) uint64 { return uint64(float64(c)*(1-f) + 0.5) }
	r, g, b := scale(v>>16&0xff), scale(v>>8&0xff), scale(v&0xff)
	return fmt.Sprintf("#%02x%02x%02x", r, g, b), true
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

// spineTitle is the title as a spine prints it: the trailing bracketed groups of a catalogue title
// ("(Unabridged)", "(The Vancouver Agitators #1)", "[128k]") are dropped, with the space before each,
// so the words that name the book fit the spine. A bracket in the middle stays, and a title that
// would be left empty is kept whole. Only the spine's text uses it; the label and every other place
// keep the full title.
func spineTitle(title string) string {
	short := strings.TrimSpace(title)
	for {
		runes := []rune(short)
		open := trailingGroupStart(runes)
		if open < 0 {
			break
		}
		rest := strings.TrimSpace(string(runes[:open]))
		if rest == "" {
			break
		}
		short = rest
	}
	if short == "" {
		return title
	}
	return short
}

// trailingGroupStart returns the index of the bracket that opens the "(...)" or "[...]" group the
// text ends with (groups of the same kind may nest), or -1 when the text does not end with a
// balanced group.
func trailingGroupStart(text []rune) int {
	if len(text) == 0 {
		return -1
	}
	closer := text[len(text)-1]
	var opener rune
	switch closer {
	case ')':
		opener = '('
	case ']':
		opener = '['
	default:
		return -1
	}
	depth := 0
	for i := len(text) - 1; i >= 0; i-- {
		switch text[i] {
		case closer:
			depth++
		case opener:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// cardLength is the length slot of a card: the duration of an audiobook, "Ebook" for a text book (it has no length).
func cardLength(c library.BookCard) string {
	if c.Kind == library.BookText {
		return cardEbook
	}
	return durationText(c.DurationMs)
}

// cardView turns a book into what the cards, rows and spines draw.
func cardView(c library.BookCard) components.CardView {
	v := components.CardView{
		ID: c.ID, Href: "/books/" + c.ID, Title: c.Title, SpineTitle: spineTitle(c.Title), Byline: bylineOf(c.Authors),
		Duration: cardLength(c), AriaLabel: c.Title,
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
	if shown, ink, ok := spineInk(c.Cover.Tint); ok {
		v.Tint, v.InkClass = shown, ink
	}
	return v
}

// Limits of a list address.
const (
	browsePageSize  = 48
	browseMaxGenres = 5
	browseMaxGenreN = 100 // runes
	browseMaxIDLen  = 64  // bytes of a person or series id in an address
)

// browseQuery is what a list address says: the order, the view, the genres, the page position and
// which kind of filter the header opens. Unknown values fall back to the defaults.
type browseQuery struct {
	Sort   library.Sort
	View   string // "grid" or "list"
	Genres []string
	// Author, Narrator and Series are ids; Length is "short", "medium" or "long"; Title is the
	// words a title must contain. All of them narrow the list and none changes the order.
	Author, Narrator, Series, Length, Title string
	// Added is the id of the person whose uploads published the books (decision 0029). listQuery drops
	// another person's id unless the viewer is an owner or admin.
	Added string
	// Format is "audio" or "text" (the Format filter); empty shows both.
	Format string
	// Show is "unfinished", "not_started" or "finished" (one at a time); it reads the person's own
	// listening progress.
	Show   string
	Cursor string
	// Add opens one kind of filter ("author", "narrator", "series", "category" or "length") in the
	// header for this page view. Only the Filter menu's own links write it; href never carries it on.
	Add string
}

// Length buckets: under 5 hours, 5 to 15 hours, over 15 hours (the spine widths use the same edges).
const (
	hourMs       = int64(3_600_000)
	lengthShortH = 5
	lengthLongH  = 15
)

// lengthRange is the duration range of a length bucket: from inclusive, to exclusive, 0 = open.
func lengthRange(length string) (from, to int64) {
	switch length {
	case "short":
		return 0, lengthShortH * hourMs
	case "medium":
		return lengthShortH * hourMs, lengthLongH * hourMs
	case "long":
		return lengthLongH * hourMs, 0
	}
	return 0, 0
}

// filtered reports whether any filter narrows the list.
func (q browseQuery) filtered() bool {
	return len(q.Genres) > 0 || q.Author != "" || q.Narrator != "" || q.Series != "" || q.Added != "" || q.Length != "" || q.Title != "" || q.Show != "" || q.Format != ""
}

func parseBrowseQuery(r *http.Request) browseQuery {
	v := r.URL.Query()
	q := browseQuery{Sort: library.ParseSort(v.Get("sort")), View: "grid", Cursor: v.Get("cursor")}
	switch a := v.Get("add"); a {
	case "author", "narrator", "series", "category", "length", "format":
		q.Add = a
	}
	if v.Get("view") == "list" {
		q.View = "list"
	}
	id := func(k string) string {
		s := strings.TrimSpace(v.Get(k))
		if len(s) > browseMaxIDLen {
			return ""
		}
		return s
	}
	q.Author, q.Narrator, q.Series, q.Added = id("author"), id("narrator"), id("series"), id("added")
	switch l := v.Get("length"); l {
	case "short", "medium", "long":
		q.Length = l
	}
	switch sh := v.Get("show"); sh {
	case library.ShowUnfinished, library.ShowNotStarted, library.ShowFinished, library.ShowShared:
		q.Show = sh
	}
	switch f := v.Get("format"); f {
	case library.BookAudio, library.BookText:
		q.Format = f
	}
	q.Title = strings.TrimSpace(v.Get("title"))
	if utf8.RuneCountInString(q.Title) > library.MaxTitleRunes {
		q.Title = string([]rune(q.Title)[:library.MaxTitleRunes])
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
	q.Add = "" // a kind opens for one page view only; a link that wants it sets it in mod
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
	for _, f := range [...]struct{ k, v string }{{"author", q.Author}, {"narrator", q.Narrator}, {"series", q.Series}, {"added", q.Added},
		{"length", q.Length}, {"format", q.Format}, {"show", q.Show}, {"title", q.Title}, {"add", q.Add}} {
		if f.v != "" {
			v.Set(f.k, f.v)
		}
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
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

// clearFilters drops every filter. The order and the view stay.
func (q *browseQuery) clearFilters() {
	q.Genres, q.Author, q.Narrator, q.Series, q.Added, q.Length, q.Format, q.Show, q.Title = nil, "", "", "", "", "", "", "", ""
}
