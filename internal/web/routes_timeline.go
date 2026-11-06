package web

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// timelineStyles are the stylesheets of the listening history page: the dashboard's frame and its own rules.
var timelineStyles = []string{"css/pages-listening.css", "css/pages-timeline.css"}

// getTimeline is "Listening history" (L6): the signed-in person's own sessions, newest first, grouped by day
// in their time zone. Nothing in the request names a person. `book` narrows it to one book; a book the
// person cannot see lists nothing, the same as an unknown id. `cursor` is the keyset of the page before.
func (s *server) getTimeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "timeline", err)
		return
	}
	loc := statsLocation(set.TimeZone)
	bookID := r.URL.Query().Get("book")
	rows, next, err := s.lib.Timeline(ctx, s.scope(r), u.ID, bookID, r.URL.Query().Get("cursor"), 0)
	if err != nil {
		s.serverError(w, r, "timeline", err)
		return
	}
	v := pages.TimelineView{
		Back: timelineBack, BackHref: "/me/listening", Title: timelineTitle, Lead: timelineLead,
		BackHereWord: timelineBackHere, ToWord: timelineToWord,
	}
	if len(rows) == 0 && r.URL.Query().Get("cursor") == "" {
		v.Empty = &pages.ListeningEmpty{Title: timelineEmptyTitle, Body: timelineEmptyBody, Action: timelineEmptyAction, Href: "/libraries"}
	}
	if bookID != "" && len(rows) > 0 {
		v.Only, v.OnlyHref, v.AllLabel = fmt.Sprintf(timelineOneBookFmt, rows[0].BookTitle), "/me/listening/history", timelineAllBooks
	}
	var day string
	for _, x := range rows {
		end := time.UnixMilli(x.EndedAt).In(loc)
		if d := end.Format(timelineDayFmt); d != day {
			day = d
			v.Days = append(v.Days, pages.TimelineDay{Label: d})
		}
		chapter := x.Chapter
		if chapter == "" && x.ChapterIdx >= 0 {
			chapter = fmt.Sprintf(timelineChapterFmt, x.ChapterIdx+1)
		}
		span := fmt.Sprintf(timelineRangeFmt, clockText(x.StartMs), clockText(x.EndMs))
		if chapter != "" {
			span += ", " + chapter
		}
		d := &v.Days[len(v.Days)-1]
		d.Rows = append(d.Rows, pages.TimelineRow{
			BookHref: "/books/" + x.BookID, BookTitle: x.BookTitle,
			Clock:  time.UnixMilli(x.StartedAt).In(loc).Format(timelineClockFmt) + " " + timelineToWord + " " + end.Format(timelineClockFmt),
			Span:   span,
			BookID: x.BookID, PlayAtMs: x.EndMs,
			BackLabel: fmt.Sprintf(timelineBackHereFmt, clockText(x.EndMs), x.BookTitle),
		})
	}
	if next != "" {
		q := url.Values{"cursor": {next}}
		if bookID != "" {
			q.Set("book", bookID)
		}
		v.OlderHref, v.OlderLabel = "/me/listening/history?"+q.Encode(), timelineOlder
	}
	s.pageStyled(w, r, http.StatusOK, timelineTitle, "profile", pages.Timeline(v), nil, timelineStyles...)
}
