package web

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Views for the book, people and search pages: library reads become the plain words and addresses
// the templates draw.

const (
	bookIntroRunes   = 320 // the description shows this much before "More"
	bookChaptersHead = 12  // chapters listed before "Show all"
	bookSeriesMax    = 10  // lines of "More in {series}"
	bookRailBooks    = 6   // cards of "Also narrated by"
	bookPersonPage   = 48  // books on a person's page
)

// bookTabs are the tabs of the book page in order; the first is the bare address.
var bookTabs = []struct{ ID, Label string }{
	{"about", bookTabAbout}, {"chapters", bookTabChapters}, {"bookmarks", bookTabBookmarks}, {"details", bookTabDetails},
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
		ByLabel: bookBy, NarratedLabel: bookNarratedBy,
	}
	if len(d.Series) > 0 {
		s := d.Series[0]
		v.SeriesLine = joinNonEmpty(" · ", s.Name, bookSeqLabel(s.Sequence))
		v.SeriesHref = "/series/" + s.ID
	}
	year := ""
	if b.PublishedYear > 0 {
		year = fmt.Sprint(b.PublishedYear)
	}
	v.Meta = joinNonEmpty(" · ", durationText(b.DurationMs), year)
	chapters := ""
	if n := len(d.Chapters); n > 0 {
		chapters = countText(n, "1 chapter", "%d chapters")
	}
	v.Meta2 = joinNonEmpty(" · ", b.Publisher, strings.Join(editionKinds(b), ", "), chapters)
	switch {
	case b.MissingSince != 0:
		v.Banner = bookBanner
	case b.RefusedReason != "":
		if v.Banner = scan.RefusalText(b.RefusedReason); v.Banner == "" {
			v.Banner = bookRefusedFallback
		}
	}
	for _, t := range bookTabs {
		v.Tabs = append(v.Tabs, pages.BookTab{ID: t.ID, Label: t.Label, Href: bookTabHref(b.ID, t.ID), Current: t.ID == tab})
	}
	return v
}

// editionKinds is the meta line's part: only "Abridged" and "Dramatized" (Explicit is in Details).
func editionKinds(b library.Book) []string {
	var out []string
	for _, k := range bookEdition(b) {
		if k != "Explicit" {
			out = append(out, k)
		}
	}
	return out
}

// bookAbout fills the About panel. series are the visible books of the book's first series in
// reading order, seqs their places; narrated are cards by the first narrator.
func bookAbout(v *pages.BookView, d library.BookDetail, series []library.BookCard, seqs map[string]string, narrated []library.BookCard) {
	v.Intro, v.Rest = bookIntro(d.Book.Description)
	v.MoreLabel = bookMore
	if len(d.Series) > 0 && len(series) > 1 {
		v.SeriesTitle, v.ThisBook = bookSeriesPrefix+d.Series[0].Name, bookThisBook
		start, end := 0, len(series)
		if end > bookSeriesMax {
			at := 0
			for i, s := range series {
				if s.ID == d.Book.ID {
					at = i
				}
			}
			start = min(max(at-bookSeriesMax/2, 0), end-bookSeriesMax)
			end = start + bookSeriesMax
		}
		for _, s := range series[start:end] {
			v.SeriesRows = append(v.SeriesRows, pages.SeriesRow{
				Label: bookSeqLabel(seqs[s.ID]), Title: s.Title, Href: "/books/" + s.ID, Current: s.ID == d.Book.ID,
			})
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
func bookChapters(v *pages.BookView, d library.BookDetail, all bool) {
	v.NoChaptersText = bookNoChapters
	rows := d.Chapters
	if !all && len(rows) > bookChaptersHead {
		rows = rows[:bookChaptersHead]
		v.Chapters.ShowAllHref = bookTabHref(d.Book.ID, "chapters") + "?all=1"
		v.Chapters.ShowAllLabel = fmt.Sprintf(bookShowAllChapters, len(d.Chapters))
	}
	for i, c := range rows {
		title := strings.TrimSpace(c.Title)
		if title == "" {
			title = fmt.Sprintf(bookChapterN, i+1)
		}
		v.Chapters.Rows = append(v.Chapters.Rows, components.ChapterRow{Num: i + 1, Title: title, Dur: clockText(c.EndMs - c.StartMs)})
	}
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
