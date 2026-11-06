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
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

const (
	highlightsRoute      = "/me/highlights"
	highlightsExportPage = 500 // rows per read while the export is built
)

func (s *server) registerHighlights(mux *http.ServeMux) {
	mux.HandleFunc("GET /me/highlights", s.getHighlights)
	mux.HandleFunc("GET /me/highlights.md", s.getHighlightsExport)
	mux.HandleFunc("GET /me/highlights.json", s.getHighlightsExport)
}

// highlightQuery is the person's own highlights narrowed by the request. Only the signed-in person's rows are
// ever read; books come through the view and read filters in SQL.
func (s *server) highlightQuery(r *http.Request) library.HighlightQuery {
	q := r.URL.Query()
	return library.HighlightQuery{
		Text: q.Get("q"), Colour: q.Get("colour"), BookID: q.Get("book"),
		Read: s.can.BookFilterFor(currentUser(r), "b", share.ReadText),
	}
}

func (s *server) getHighlights(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	q := s.highlightQuery(r)
	page := 1
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 1 {
		page = min(n, 100_000)
	}
	q.Limit, q.Offset = library.HighlightsPage, (page-1)*library.HighlightsPage
	rows, total, err := s.lib.AllHighlights(r.Context(), s.scope(r), u.ID, q)
	if err != nil {
		s.serverError(w, r, "all highlights", err)
		return
	}
	colour := ""
	if slices.ContainsFunc(highlightsColours, func(c struct{ Value, Label string }) bool { return c.Value == q.Colour }) {
		colour = q.Colour
	}
	v := pages.HighlightsView{
		Back: highlightsBack, BackHref: "/me", Title: highlightsTitle, Lead: highlightsLead, Action: highlightsRoute,
		SearchLabel: highlightsSearchLabel, Query: q.Text, ColourLabel: highlightsColourLabel, ApplyLabel: highlightsApply,
		Book: q.BookID, ExportMD: highlightsRoute + ".md", ExportMDLabel: highlightsExportMD,
		ExportJSON: highlightsRoute + ".json", ExportJSONLabel: highlightsExportJSON,
		EmptyTitle: highlightsEmptyTitle, EmptyBody: highlightsEmptyBody,
	}
	v.Colours = append(v.Colours, pages.HighlightColour{Value: "", Label: highlightsAnyColour, Selected: colour == ""})
	for _, c := range highlightsColours {
		v.Colours = append(v.Colours, pages.HighlightColour{Value: c.Value, Label: c.Label, Selected: c.Value == colour})
	}
	if q.Text != "" || colour != "" || q.BookID != "" {
		v.EmptyTitle, v.EmptyBody = highlightsNoMatch, ""
	}
	if q.BookID != "" && len(rows) > 0 {
		v.Only, v.OnlyHref, v.AllLabel = fmt.Sprintf(highlightsOneBookFmt, rows[0].BookTitle), highlightsRoute, highlightsAllBooks
	}
	v.Count = fmt.Sprintf(highlightsCountFmt, total)
	if total == 1 {
		v.Count = highlightsCountOne
	}
	names := map[string]string{}
	for _, c := range highlightsColours {
		names[c.Value] = c.Label
	}
	for _, x := range rows {
		row := pages.HighlightRow{Quote: x.Quote, Note: x.Note, Colour: x.Colour, ColourName: fmt.Sprintf(highlightsColourFmt, names[x.Colour]),
			Book: x.BookTitle, BookHref: "/books/" + x.BookID, Chapter: highlightChapter(x), When: time.UnixMilli(x.CreatedAt).UTC().Format(highlightsDayFmt),
			OpenWord: highlightsOpen, OpenLabel: fmt.Sprintf(highlightsOpenLabel, x.BookTitle)}
		// A sentence link needs its edition; without one the highlight opens the reader at the book.
		if x.EditionID != "" {
			row.OpenHref = readerHref(x.BookID, x.EditionID, "ch", strconv.Itoa(x.Chapter), "at", x.StartSID) + "#s-" + x.StartSID
		} else {
			row.OpenHref = readerHref(x.BookID, "")
		}
		v.Rows = append(v.Rows, row)
	}
	link := func(p int) string {
		qs := url.Values{}
		for k, val := range map[string]string{"q": q.Text, "colour": colour, "book": q.BookID} {
			if val != "" {
				qs.Set(k, val)
			}
		}
		if p > 1 {
			qs.Set("page", strconv.Itoa(p))
		}
		if len(qs) == 0 {
			return highlightsRoute
		}
		return highlightsRoute + "?" + qs.Encode()
	}
	if page > 1 {
		v.NewerHref, v.NewerLabel = link(page-1), highlightsNewer
	}
	if page*library.HighlightsPage < total {
		v.OlderHref, v.OlderLabel = link(page+1), highlightsOlder
	}
	s.pageStyled(w, r, http.StatusOK, highlightsTitle, "profile", pages.Highlights(v), nil, pages.HighlightsStyles...)
}

// highlightChapter names the chapter of a row: its title, else "Chapter N".
func highlightChapter(x library.HighlightRow) string {
	if x.ChapterTitle != "" {
		return x.ChapterTitle
	}
	if x.Chapter > 0 {
		return fmt.Sprintf(readerChapter, x.Chapter)
	}
	return ""
}

// getHighlightsExport sends every highlight the person can still read, book by book, as Markdown or JSON, in the
// format of the reader's own export (marksMarkdown, marksJSON). It ignores the page's filters but honours none of
// another person's rows and no revoked book.
func (s *server) getHighlightsExport(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	q := s.highlightQuery(r)
	q.Text, q.Colour, q.BookID, q.Limit = "", "", "", highlightsExportPage
	var order []string
	titles := map[string]string{}
	marks := map[string][]rdrExport{}
	for q.Offset = 0; ; q.Offset += highlightsExportPage {
		rows, _, err := s.lib.AllHighlights(r.Context(), s.scope(r), u.ID, q)
		if err != nil {
			s.serverError(w, r, "export all highlights", err)
			return
		}
		for _, x := range rows {
			if _, ok := titles[x.BookID]; !ok {
				order = append(order, x.BookID)
				titles[x.BookID] = x.BookTitle
			}
			marks[x.BookID] = append(marks[x.BookID], rdrExport{Kind: "highlight", Chapter: x.Chapter, ChapterTitle: x.ChapterTitle, Quote: x.Quote,
				Note: x.Note, Colour: x.Colour, Created: time.UnixMilli(x.CreatedAt).UTC().Format("2006-01-02"), CreatedAt: x.CreatedAt,
				sid: x.StartSID, off: x.StartOff})
		}
		if len(rows) < highlightsExportPage {
			break
		}
	}
	slices.SortStableFunc(order, func(a, b string) int {
		return strings.Compare(strings.ToLower(titles[a])+"\x00"+a, strings.ToLower(titles[b])+"\x00"+b)
	})
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	var body strings.Builder
	if strings.HasSuffix(r.URL.Path, ".json") {
		h.Set("Content-Type", "application/json")
		h.Set("Content-Disposition", `attachment; filename="tendshelf-highlights.json"`)
		list := []marksBook{}
		for _, id := range order {
			sortMarks(marks[id])
			list = append(list, newMarksBook(titles[id], marks[id]))
		}
		body.WriteString(marksBooksJSON(list))
	} else {
		h.Set("Content-Type", "text/markdown; charset=utf-8")
		h.Set("Content-Disposition", `attachment; filename="tendshelf-highlights.md"`)
		for i, id := range order {
			sortMarks(marks[id])
			if i > 0 {
				body.WriteString("\n")
			}
			body.WriteString(marksMarkdown(titles[id], marks[id]))
		}
		if len(order) == 0 {
			body.WriteString("# " + highlightsFileTitle + "\n")
		}
	}
	_, _ = w.Write([]byte(body.String()))
}
