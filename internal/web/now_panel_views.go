package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// The Now playing panel's views (NP1). They reuse the book page's own helpers (bookHeader,
// bookSeriesPlace, seriesTileState, bookmarkRow, cardView) and add only the composition.

// ChapterLine splits "Chapter 12 of 19 · The Second Lamp" into its lead and the chapter's own name.
// index is 1-based. A book of one chapter (or none) has no line. Many files name their chapters
// "Chapter 13" or just "13", which would print twice, so those give no name.
func ChapterLine(index, count int, title string) (lead, rest string) {
	if count <= 1 {
		return "", ""
	}
	lead = fmt.Sprintf(nowPanelChapterOf, index, count)
	t := strings.TrimSpace(title)
	if t == "" || strings.EqualFold(t, fmt.Sprintf("Chapter %d", index)) || t == strconv.Itoa(index) {
		return lead, ""
	}
	return lead, t
}

// nowLinks turns people into links with their ids (the avatar colour follows the id).
func nowLinks(people []library.PersonRef, role string) []components.NowLink {
	var out []components.NowLink
	for _, p := range people {
		out = append(out, components.NowLink{ID: p.ID, Name: p.Name, Href: "/" + role + "/" + p.ID})
	}
	return out
}

// nowChapterLine is the lead and name of the 0-based chapter idx (both empty when out of range).
func nowChapterLine(chs []library.Chapter, idx int) (string, string) {
	if idx < 0 || idx >= len(chs) {
		return "", ""
	}
	return ChapterLine(idx+1, len(chs), chs[idx].Title)
}

// nowPanelHref is the address of one of the panel's fragments.
func nowPanelHref(bookID, part string) string {
	h := "/hx/player/panel/" + bookID
	if part != "" {
		h += "/" + part
	}
	return h
}

// nowReadBy is the narrators for the subtitle line: "read by Nan Narrator, Theo Marsh", or "".
func nowReadBy(narrators []library.PersonRef) string {
	var names []string
	for _, n := range narrators {
		names = append(names, n.Name)
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf(nowPanelReadByLine, strings.Join(names, ", "))
}

// nowPanelView is the panel's frame: the hooks and the tabs. The caller fills Menu, ShareHref and About.
func nowPanelView(d library.BookDetail, listen bool, marks int) components.NowPanelView {
	id := d.Book.ID
	v := components.NowPanelView{
		BookID: id, BookHref: "/books/" + id, AriaLabel: nowPanelAriaLabel, TabsLabel: nowPanelTabsLabel,
		ShareLabel: nowPanelShare, ReadBy: nowReadBy(d.Card.Narrators),
		Tabs: []components.NowTab{{Key: "about", Label: nowPanelAbout, Href: nowPanelHref(id, "about"), Selected: true}},
	}
	if listen {
		v.Tabs = append(v.Tabs, components.NowTab{
			Key: "bookmarks", Label: nowPanelBookmarks, Count: strconv.Itoa(marks), Href: nowPanelHref(id, "bookmarks"),
		})
	}
	return v
}

// nowSeriesState is the words under a series row: the book's own row says where the person is, the
// rows after it say how long they are.
func nowSeriesState(i, at int, books []library.BookCard, prog map[string]progress.Progress) string {
	if i == at {
		if p, ok := prog[books[i].ID]; ok && !p.Finished() && p.PositionMs > 0 && p.DurationMs > 0 {
			pct := int(float64(p.PositionMs) * 100 / float64(p.DurationMs))
			return fmt.Sprintf(nowPanelYoureHereN, min(max(pct, 1), 99))
		}
		return nowPanelYoureHere
	}
	state := seriesTileState(i, at, books, prog)
	if state == bookTileUpNext || state == bookTileNotStarted {
		if t := hoursMinutesText(books[i].DurationMs); t != "" {
			state += nowPanelStateSep + t
		}
	}
	return state
}

// nowSeries is the series block, or nil when the book is alone in its series (or in none).
func nowSeries(d library.BookDetail, series []library.BookCard, seqs map[string]string, prog map[string]progress.Progress) *components.NowSeries {
	if len(d.Series) == 0 || len(series) < 2 {
		return nil
	}
	at := slices.IndexFunc(series, func(c library.BookCard) bool { return c.ID == d.Book.ID })
	out := &components.NowSeries{
		Name: d.Series[0].Name, Href: "/series/" + d.Series[0].ID,
		Place: bookSeriesPlace(bookFirstSequence(d), seqs),
	}
	for i, c := range series {
		out.Books = append(out.Books, components.NowSeriesBook{
			Number: strings.TrimSpace(seqs[c.ID]), Title: c.Title, Href: "/books/" + c.ID,
			State: nowSeriesState(i, at, series, prog), Cover: cardView(c), Current: i == at,
		})
	}
	return out
}

// nowDetails lists the facts the book has. A fact it does not have is not listed.
func nowDetails(d library.BookDetail) []components.NowDetail {
	b := d.Book
	var out []components.NowDetail
	add := func(x components.NowDetail) {
		if x.Value != "" || len(x.People) > 0 {
			out = append(out, x)
		}
	}
	add(components.NowDetail{Label: nowPanelWrittenBy, People: nowLinks(d.Card.Authors, "authors")})
	add(components.NowDetail{Label: nowPanelReadBy, People: nowLinks(d.Card.Narrators, "narrators")})
	year := ""
	if b.PublishedYear > 0 {
		year = strconv.Itoa(b.PublishedYear)
	}
	if pub := joinNonEmpty(nowPanelStateSep, strings.TrimSpace(b.Publisher), year); pub != "" {
		edition := nowPanelUnabridged
		if b.Abridged {
			edition = nowPanelAbridged
		}
		add(components.NowDetail{Label: bookLabelPublished, Value: pub, Sub: edition})
	}
	length := joinNonEmpty(nowPanelStateSep, hoursMinutesText(b.DurationMs),
		func() string {
			if n := len(d.Chapters); n > 0 {
				return countText(n, "1 chapter", nowPanelChapters)
			}
			return ""
		}())
	add(components.NowDetail{Label: bookLabelLength, Value: length})
	add(components.NowDetail{Label: bookLabelLanguage, Value: strings.TrimSpace(b.Language)})
	if b.AddedAt > 0 {
		add(components.NowDetail{
			Label: nowPanelOnShelf,
			Value: fmt.Sprintf(nowPanelAddedOn, time.UnixMilli(b.AddedAt).UTC().Format("2 January 2006")),
		})
	}
	return out
}

// nowMoreBy is the small rail at the end of About: the first narrator's other books, else the first
// author's. It returns the heading and at most nowPanelMaxMore books, or nothing.
func (s *server) nowMoreBy(r *http.Request, d library.BookDetail, narrated []library.BookCard) (string, []components.CardView, error) {
	pick := func(cards []library.BookCard) []components.CardView {
		var out []components.CardView
		for _, c := range cards {
			if c.ID != d.Book.ID && len(out) < nowPanelMaxMore {
				out = append(out, cardView(c))
			}
		}
		return out
	}
	if len(d.Card.Narrators) > 0 {
		if more := pick(narrated); len(more) > 0 {
			return fmt.Sprintf(nowPanelAlsoReadBy, d.Card.Narrators[0].Name), more, nil
		}
	}
	if len(d.Card.Authors) == 0 {
		return "", nil, nil
	}
	page, err := s.lib.ListBooks(r.Context(), s.scope(r), library.ListQuery{AuthorID: d.Card.Authors[0].ID, Limit: nowPanelMaxMore + 1})
	if err != nil {
		return "", nil, err
	}
	if more := pick(page.Books); len(more) > 0 {
		return fmt.Sprintf(nowPanelMoreBy, d.Card.Authors[0].Name), more, nil
	}
	return "", nil, nil
}

// nowAboutView builds the About tab from the book page's own reads.
func (s *server) nowAboutView(r *http.Request, d library.BookDetail, rails bookRailReads) (components.NowAboutView, error) {
	h := bookHeader(d, "about")
	v := components.NowAboutView{
		Intro: h.Intro, Rest: h.Rest, SummaryLabel: nowPanelSummary, SeriesLabel: nowPanelSeries,
		DetailsLabel: nowPanelDetails, ReadMore: nowPanelReadMore, ShowLess: nowPanelShowLess,
		Series:  nowSeries(d, rails.series, rails.seqs, rails.prog),
		Details: nowDetails(d),
	}
	if v.Intro == "" {
		v.EmptyText = nowPanelNoSummary
	}
	for _, g := range d.Genres {
		v.Genres = append(v.Genres, components.NowLink{Name: g, Href: "/libraries?genre=" + url.QueryEscape(g)})
	}
	var err error
	v.MoreTitle, v.More, err = s.nowMoreBy(r, d, rails.narrated)
	return v, err
}

// nowBookmarksView builds the Bookmarks tab: the newest few of the person's own bookmarks.
func nowBookmarksView(d library.BookDetail, list []progress.Bookmark, total int, now time.Time) components.NowBookmarksView {
	v := components.NowBookmarksView{
		EmptyText: nowPanelNoMarks, Count: strconv.Itoa(total),
		AllHref: bookmarkTabHref(d.Book.ID), AllLabel: fmt.Sprintf(nowPanelAllMarks, total),
	}
	for i, b := range list {
		if i == nowPanelMaxMarks {
			break
		}
		v.Rows = append(v.Rows, bookmarkRow(b, d.Chapters, now))
	}
	return v
}
