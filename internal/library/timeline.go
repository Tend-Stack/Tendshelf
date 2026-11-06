package library

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// TimelinePage is how many sessions one page of the listening timeline holds.
const TimelinePage = 50

// TimelineRow is one listening session on the timeline: which book, when it started and ended on the
// wall clock (Unix ms), and the book positions it covered. Chapter is the title of the chapter that holds
// EndMs ("" when the book has none), ChapterIdx its index (-1 when none).
type TimelineRow struct {
	ID, BookID, BookTitle string
	StartedAt, EndedAt    int64
	StartMs, EndMs        int64
	ListenedMs            int64
	Chapter               string
	ChapterIdx            int
}

// Timeline lists the person's sessions newest first, keyset on (ended_at, id) after cursor, limit rows
// (TimelinePage when limit is not positive), books visible through sc only. A non-empty bookID narrows the
// list to that book; a book the person cannot see lists nothing. next is the cursor for the following page
// and "" on the last one. A bad cursor is the first page.
func (r *Repo) Timeline(ctx context.Context, sc Scope, userID, bookID, cursor string, limit int) ([]TimelineRow, string, error) {
	if limit <= 0 {
		limit = TimelinePage
	}
	w := visible(sc, false)
	w.add("s.user_id = ?", userID)
	if bookID != "" {
		w.add("s.book_id = ?", bookID)
	}
	if at, id, ok := decodeTimelineCursor(cursor); ok {
		w.add("(s.ended_at < ? OR (s.ended_at = ? AND s.id > ?))", at, at, id)
	}
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (TimelineRow, error) {
		var x TimelineRow
		var title sql.NullString
		err := s.Scan(&x.ID, &x.BookID, &title, &x.StartedAt, &x.EndedAt, &x.StartMs, &x.EndMs, &x.ListenedMs)
		x.BookTitle = title.String
		x.ChapterIdx = -1
		return x, err
	}, `SELECT s.id, s.book_id, b.title, s.started_at, s.ended_at, s.start_ms, s.end_ms, s.listened_ms
FROM listening_sessions s JOIN books b ON b.id = s.book_id JOIN libraries l ON l.id = b.library_id
WHERE `+w.sql()+` ORDER BY s.ended_at DESC, s.id LIMIT ?`, append(w.args, limit+1)...)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = encodeTimelineCursor(last.EndedAt, last.ID)
	}
	if err := r.timelineChapters(ctx, rows); err != nil {
		return nil, "", err
	}
	return rows, next, nil
}

// timelineChapters fills Chapter and ChapterIdx: one query for the page's books, the chapter found in Go
// as the last one that starts at or before the session's end.
func (r *Repo) timelineChapters(ctx context.Context, rows []TimelineRow) error {
	seen := map[string]bool{}
	var ids []string
	for _, x := range rows {
		if !seen[x.BookID] {
			seen[x.BookID] = true
			ids = append(ids, x.BookID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	marks, args := inList(ids)
	type ch struct {
		book string
		c    Chapter
	}
	all, err := many(ctx, r.st.Reader(), func(s scanner) (ch, error) {
		var x ch
		return x, s.Scan(&x.book, &x.c.Idx, &x.c.StartMs, &x.c.EndMs, &x.c.Title)
	}, `SELECT book_id, idx, start_ms, end_ms, title FROM chapters WHERE book_id IN (`+marks+`) ORDER BY book_id, idx`, args...)
	if err != nil {
		return err
	}
	by := map[string][]Chapter{}
	for _, x := range all {
		by[x.book] = append(by[x.book], x.c)
	}
	for i := range rows {
		for _, c := range by[rows[i].BookID] {
			if c.StartMs > rows[i].EndMs {
				break
			}
			rows[i].ChapterIdx, rows[i].Chapter = c.Idx, c.Title
		}
	}
	return nil
}

func encodeTimelineCursor(endedAt int64, id string) string {
	return strconv.FormatInt(endedAt, 10) + "." + id
}

func decodeTimelineCursor(c string) (int64, string, bool) {
	at, id, ok := strings.Cut(c, ".")
	if !ok || id == "" {
		return 0, "", false
	}
	n, err := strconv.ParseInt(at, 10, 64)
	if err != nil || n < 0 {
		return 0, "", false
	}
	return n, id, true
}
