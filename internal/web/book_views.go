package web

import (
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Views for the book, people and search pages: library reads become the plain words and addresses
// the templates draw.

const (
	bookIntroRunes   = 320 // the description shows this much before "More"
	bookChaptersHead = 12  // chapters listed before "Show all"
	bookSeriesTiles  = 6   // tiles of "More in {series}", windowed around the open book
	bookRailBooks    = 3   // cards of "Also narrated by"
	bookPersonPage   = 48  // books on a person's page
)

// bookTabs are the tabs of the book page in order; the first is the bare address.
var bookTabs = []struct{ ID, Label string }{
	{"about", bookTabAbout}, {"chapters", bookTabChapters}, {"text", ebookTabLabel}, {"bookmarks", bookTabBookmarks}, {"details", bookTabDetails},
}

func bookTabValid(tab string) bool {
	for _, t := range bookTabs {
		if t.ID == tab {
			return true
		}
	}
	return false
}

// bookTabHref is the canonical address of a tab: the bare address for About.
func bookTabHref(id, tab string) string {
	if tab == "about" {
		return "/books/" + id
	}
	return "/books/" + id + "/" + tab
}

// bookLinks turns people into links to their page; role is "authors" or "narrators".
func bookLinks(people []library.PersonRef, role string) []pages.Link {
	var out []pages.Link
	for _, p := range people {
		out = append(out, pages.Link{Name: p.Name, Href: "/" + role + "/" + p.ID})
	}
	return out
}

// bookIntro splits a description at a word boundary after about bookIntroRunes runes: the first part
// shows at once, the rest sits behind "More". A short description has no rest.
func bookIntro(desc string) (intro, rest string) {
	desc = strings.TrimSpace(desc)
	if utf8.RuneCountInString(desc) <= bookIntroRunes {
		return desc, ""
	}
	rs := []rune(desc)
	cut := bookIntroRunes
	for cut > 0 && !unicode.IsSpace(rs[cut-1]) && !unicode.IsSpace(rs[cut]) {
		cut--
	}
	if cut == 0 { // one long word: cut it where it stands
		cut = bookIntroRunes
	}
	return strings.TrimSpace(string(rs[:cut])), strings.TrimSpace(string(rs[cut:]))
}

// bookSeqLabel is "Book 2", or "" when the book has no place number in the series.
func bookSeqLabel(seq string) string {
	if seq = strings.TrimSpace(seq); seq == "" {
		return ""
	}
	return bookSeriesBook + seq
}

// bookEdition lists what is special about the recording: "Abridged", "Dramatized", "Explicit".
func bookEdition(b library.Book) []string {
	var out []string
	if b.Abridged {
		out = append(out, "Abridged")
	}
	if b.Dramatized {
		out = append(out, "Dramatized")
	}
	if b.Explicit {
		out = append(out, "Explicit")
	}
	return out
}

func joinNonEmpty(sep string, parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, sep)
}

// bookHeader fills the part of the view every tab shares.
func bookHeader(d library.BookDetail, tab string) pages.BookView {
	b, c := d.Book, d.Card
	cover := cardView(c)
	if c.Cover != nil && c.Cover.SHA != "" {
		cover.CoverSrc, cover.CoverSrcset = coverURL(c.ID, c.Cover.SHA, 640), ""
	}
	v := pages.BookView{
		Title: b.Title, Subtitle: b.Subtitle, Cover: cover, Tab: tab,
		BackHref: "/libraries", BackLabel: bookBack,
		Authors: bookLinks(c.Authors, "authors"), Narrators: bookLinks(c.Narrators, "narrators"),
		ByLabel: bookBy, NarratedLabel: bookNarratedBy, BylineSep: bookBylineSep,
		MoreLabel: bookMore, LessLabel: bookLess,
	}
	v.Intro, v.Rest = bookIntro(b.Description)
	if len(d.Series) > 0 {
		s := d.Series[0]
		v.ChipSeries = joinNonEmpty(" · ", s.Name, bookSeqLabel(s.Sequence))
		v.ChipSeriesHref = "/series/" + s.ID
		v.SeriesHref = v.ChipSeriesHref
	}
	v.ChipDuration = durationText(b.DurationMs)
	if b.PublishedYear > 0 {
		v.ChipYear = fmt.Sprint(b.PublishedYear)
	}
	v.ChipPublisher = b.Publisher
	if n := len(d.Chapters); n > 0 {
		v.ChipChapters = countText(n, "1 chapter", "%d chapters")
	}
	v.Caption = joinNonEmpty(" · ", b.Publisher, strings.Join(editionKinds(b), ", "), v.ChipChapters)
	switch {
	case b.MissingSince != 0:
		v.Banner = bookBanner
	case b.RefusedReason != "":
		if v.Banner = scan.RefusalText(b.RefusedReason); v.Banner == "" {
			v.Banner = bookRefusedFallback
		}
	}
	if c.Cover != nil {
		v.BandTint, v.BandClass = bandClasses(c.Cover.Tint)
	}
	for _, t := range bookTabs {
		v.Tabs = append(v.Tabs, pages.BookTab{
			ID: t.ID, Label: t.Label, Href: bookTabHref(b.ID, t.ID), Current: t.ID == tab,
			Home: t.ID == "chapters" && tab == "about",
		})
	}
	return v
}

// bandClasses returns the tint for the band and its theme classes ("ts-band-dark-1 ts-band-light-2"),
// or nothing when no theme keeps the text readable on it: the band is then plain.
func bandClasses(tint string) (string, string) {
	levels := media.BandLevels(tint)
	var classes []string
	for _, th := range media.Themes {
		if lv, ok := levels[th.Name]; ok {
			classes = append(classes, fmt.Sprintf("ts-band-%s-%d", th.Name, lv))
		}
	}
	if len(classes) == 0 {
		return "", ""
	}
	return tint, strings.Join(classes, " ")
}

// editionKinds is the caption's part: only "Abridged" and "Dramatized" (Explicit is in Details).
func editionKinds(b library.Book) []string {
	var out []string
	for _, k := range bookEdition(b) {
		if k != "Explicit" {
			out = append(out, k)
		}
	}
	return out
}

// bookSeriesPlace is "Book 2 of 3" when the visible places of the series are exactly the whole
// numbers 1 to m, else "Book 2" (a gap or a fraction means the "of m" would be a guess, and a count
// of hidden books must never show). It is empty when the book has no place.
func bookSeriesPlace(seq string, seqs map[string]string) string {
	label := bookSeqLabel(seq)
	if label == "" || len(seqs) == 0 {
		return label
	}
	seen := make(map[int]bool, len(seqs))
	for _, q := range seqs {
		n, err := strconv.Atoi(strings.TrimSpace(q))
		if err != nil || n < 1 || n > len(seqs) {
			return label
		}
		seen[n] = true
	}
	if len(seen) != len(seqs) {
		return label
	}
	return fmt.Sprintf(bookSeriesOf, strings.TrimSpace(seq), len(seqs))
}

// bookOverline is the line above the title: "Harbour Lights · Book 2 of 3 · finished in March"
// (the year follows when it is not this one). A book with no series has only the finished part,
// started with a capital; a book with nothing to say has none.
func bookOverline(d library.BookDetail, place string, p *progress.Progress, now time.Time) string {
	var parts []string
	if len(d.Series) > 0 {
		parts = append(parts, d.Series[0].Name, place)
	}
	if p != nil && p.Finished() {
		t := time.UnixMilli(p.FinishedAt).UTC()
		part := fmt.Sprintf(bookFinishedIn, t.Month().String())
		if t.Year() != now.UTC().Year() {
			part = fmt.Sprintf(bookFinishedInYear, t.Month().String(), t.Year())
		}
		if len(parts) == 0 {
			part = strings.ToUpper(part[:1]) + part[1:]
		}
		parts = append(parts, part)
	}
	return joinNonEmpty(bookBylineSep, parts...)
}

// seriesTileState is the line under a tile of the series rail. The first match wins: the open book,
// finished, started (a rounded share of 1 to 99, or "Started" when the length is unknown), the book
// right after the open one ("Next in series"), else "Not started".
func seriesTileState(i, at int, books []library.BookCard, prog map[string]progress.Progress) string {
	if i == at {
		return bookThisBook
	}
	p, ok := prog[books[i].ID]
	switch {
	case ok && p.Finished():
		return bookTileFinished
	case ok && p.PositionMs > 0:
		if p.DurationMs <= 0 {
			return bookTileStarted
		}
		pct := int(math.Round(float64(p.PositionMs) * 100 / float64(p.DurationMs)))
		return fmt.Sprintf(bookTilePercent, min(max(pct, 1), 99))
	case i == at+1 && at >= 0:
		return bookTileUpNext
	}
	return bookTileNotStarted
}

// bookRails fills the two rails under the tabs: the tiles of the book's first series (series are the
// visible books in reading order, seqs their places, prog the person's progress on them) and the
// other books by the first narrator. Everything passed in was read with the person's scope.
func bookRails(v *pages.BookView, d library.BookDetail, series []library.BookCard, seqs map[string]string, prog map[string]progress.Progress, narrated []library.BookCard) {
	if len(d.Series) > 0 && len(series) > 1 {
		v.SeriesTitle = bookSeriesPrefix + d.Series[0].Name
		v.SeriesHref = "/series/" + d.Series[0].ID
		at := slices.IndexFunc(series, func(c library.BookCard) bool { return c.ID == d.Book.ID })
		start, end := 0, len(series)
		if end > bookSeriesTiles {
			start = min(max(max(at, 0)-bookSeriesTiles/2, 0), end-bookSeriesTiles)
			end = start + bookSeriesTiles
		}
		for i := start; i < end; i++ {
			c := series[i]
			tile := cardView(c)
			label := bookSeqLabel(seqs[c.ID])
			tile.Title = joinNonEmpty(" · ", label, c.Title)
			tile.AriaLabel = tile.Title
			tile.Byline = seriesTileState(i, at, series, prog)
			if i == at {
				tile.Current = true
				if label != "" {
					tile.Title = label
				} else {
					tile.Title = c.Title
				}
			}
			v.SeriesTiles = append(v.SeriesTiles, tile)
		}
		// Start series: the first visible book not yet finished, unless that is the open book.
		for _, c := range series {
			if p, ok := prog[c.ID]; ok && p.Finished() {
				continue
			}
			if c.ID != d.Book.ID {
				v.StartHref, v.StartLabel = "/books/"+c.ID, bookStartSeries
			}
			break
		}
	}
	if len(d.Card.Narrators) > 0 {
		v.NarratedTitle = bookAlsoNarrated + d.Card.Narrators[0].Name
		for _, c := range narrated {
			if c.ID == d.Book.ID || len(v.Narrated) == bookRailBooks {
				continue
			}
			v.Narrated = append(v.Narrated, cardView(c))
		}
	}
}

// bookChapters fills the Chapters panel: the first rows, or all of them when all is true.
func bookChapters(v *pages.BookView, d library.BookDetail, all bool, play *chapterPlay) {
	v.NoChaptersText = bookNoChapters
	rows := d.Chapters
	if !all && len(rows) > bookChaptersHead {
		rows = rows[:bookChaptersHead]
		v.Chapters.ShowAllHref = bookTabHref(d.Book.ID, "chapters") + "?all=1"
		v.Chapters.ShowAllLabel = fmt.Sprintf(bookShowAllChapters, len(d.Chapters))
	}
	if play != nil {
		v.Chapters.BookID = d.Book.ID
	}
	current := currentChapterIdx(d.Chapters, play)
	for i, c := range rows {
		title := strings.TrimSpace(c.Title)
		if title == "" {
			title = fmt.Sprintf(bookChapterN, i+1)
		}
		row := components.ChapterRow{Num: i + 1, Title: title, Dur: clockText(c.EndMs - c.StartMs), PlayAtMs: -1}
		if play != nil {
			if id, off, ok := playTarget(play.book, play.files, c.StartMs); ok {
				row.PlayAtMs = c.StartMs
				row.Href = "/stream/" + id + "#t=" + strconv.FormatFloat(float64(off)/1000, 'f', 3, 64)
				row.Current = i == current
				if play.p != nil && (play.p.Finished() || c.EndMs <= play.p.PositionMs) {
					row.Played = true
					row.PlayedName = fmt.Sprintf(chapterPlayed, title)
				}
			}
		}
		v.Chapters.Rows = append(v.Chapters.Rows, row)
	}
}

// chapterPlay is what the chapter rows need to become links: the book, its files and the person's
// progress (nil when there is none). A nil *chapterPlay leaves the rows plain text.
type chapterPlay struct {
	book  library.Book
	files []library.AudioFile
	p     *progress.Progress
}

// currentChapterIdx is the index of the chapter at the stored place of an unfinished book, or -1.
func currentChapterIdx(chs []library.Chapter, play *chapterPlay) int {
	if play == nil || play.p == nil || play.p.Finished() || play.p.PositionMs <= 0 {
		return -1
	}
	cur := -1
	for i, c := range chs {
		if c.StartMs <= play.p.PositionMs {
			cur = i
		}
	}
	return cur
}

// bookDetailsView lists the Details rows. Empty values are left out. The folder on disk shows only
// when canManage: it tells a person who may not manage the server where its files are.
func bookDetailsView(d library.BookDetail, canManage bool) []pages.DetailRow {
	b := d.Book
	var rows []pages.DetailRow
	add := func(label, value string) {
		if value = strings.TrimSpace(value); value != "" {
			rows = append(rows, pages.DetailRow{Label: label, Value: value})
		}
	}
	add(bookLabelPublisher, b.Publisher)
	if b.PublishedYear > 0 {
		add(bookLabelPublished, fmt.Sprint(b.PublishedYear))
	}
	add(bookLabelLanguage, b.Language)
	add(bookLabelISBN, b.ISBN)
	add(bookLabelASIN, b.ASIN)
	add(bookLabelEdition, strings.Join(bookEdition(b), ", "))
	if len(d.Genres) > 0 {
		r := pages.DetailRow{Label: bookLabelGenres}
		for _, g := range d.Genres {
			r.Links = append(r.Links, pages.Link{Name: g, Href: "/libraries?genre=" + url.QueryEscape(g)})
		}
		rows = append(rows, r)
	}
	add(bookLabelTags, strings.Join(d.Tags, ", "))
	add(bookLabelLength, hoursMinutesText(b.DurationMs))
	if n := len(d.Chapters); n > 0 {
		add(bookLabelChapters, fmt.Sprint(n))
	}
	if d.Parts > 0 {
		files := countText(d.Parts, "1 file", "%d files")
		if d.MissingParts > 0 {
			files += fmt.Sprintf(" · %d missing", d.MissingParts)
		}
		add(bookLabelFiles, files)
	}
	if b.AddedAt > 0 {
		add(bookLabelAdded, time.UnixMilli(b.AddedAt).UTC().Format("2 Jan 2006"))
	}
	if canManage {
		add(bookLabelFolder, b.RootPath)
	}
	return rows
}

// bookUniquePeople lists the people on a run of books in the order they first appear, up to max.
func bookUniquePeople(cards []library.BookCard, narrators bool, max int) []library.PersonRef {
	var out []library.PersonRef
	seen := map[string]bool{}
	for _, c := range cards {
		list := c.Authors
		if narrators {
			list = c.Narrators
		}
		for _, p := range list {
			if seen[p.ID] || len(out) == max {
				continue
			}
			seen[p.ID] = true
			out = append(out, p)
		}
	}
	return out
}
