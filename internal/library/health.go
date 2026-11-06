package library

import (
	"context"
	"errors"
	"path/filepath"
)

// Library health (S3): the books that need a look, in groups, with exact counts. Everything here reads only.
// Problems (the setup summary and the Libraries card) shares noCoverCond with it, so the two never disagree.

// ErrUnknownHealthGroup is returned for a group name HealthGroups does not list.
var ErrUnknownHealthGroup = errors.New("library: unknown health group")

// The health groups, in the order the page shows them.
const (
	HealthMissing         = "missing"
	HealthRefused         = "refused"
	HealthUnreadable      = "unreadable"
	HealthNoChapters      = "no_chapters"
	HealthNoCover         = "no_cover"
	HealthNoMatch         = "no_match"
	HealthChaptersPastEnd = "chapters_past_end"
)

// HealthGroups lists every group name in display order.
var HealthGroups = []string{HealthMissing, HealthRefused, HealthUnreadable, HealthNoChapters, HealthNoCover, HealthNoMatch, HealthChaptersPastEnd}

// noCoverCond is the condition (books aliased b) for a listable book without a cover whose files have all settled.
var noCoverCond = Listable("b") + ` AND b.cover_id IS NULL AND ` + noUnsettled

// healthCond is the condition per group over books b. A missing book counts only as missing and a refused one
// only as refused; the other five need a listable book, and one book may sit in several of them.
var healthCond = map[string]string{
	HealthMissing: `b.missing_since IS NOT NULL`,
	HealthRefused: `b.refused_reason IS NOT NULL AND b.missing_since IS NULL`,
	HealthUnreadable: Listable("b") + ` AND EXISTS (SELECT 1 FROM audio_files a WHERE a.book_id = b.id
  AND a.missing_since IS NULL AND a.probe_error IS NOT NULL)`,
	HealthNoChapters: Listable("b") + ` AND b.kind = 'audio' AND ` + noUnsettled +
		` AND NOT EXISTS (SELECT 1 FROM chapters c WHERE c.book_id = b.id)`,
	HealthNoCover: noCoverCond,
	HealthNoMatch: Listable("b") + ` AND EXISTS (SELECT 1 FROM book_lookups bl WHERE bl.book_id = b.id
  AND bl.outcome IN ('no_match','needs_look','failed'))`,
	HealthChaptersPastEnd: Listable("b") + ` AND ` + ChaptersPastEnd,
}

// HealthRow is one book under a group. FolderGone is true when no book of its folder is present any more.
type HealthRow struct {
	ID, Title, FolderID, LibraryID string
	Refused                        string // refused reason (drm, damaged, unsupported, no_audio), empty when none
	FolderGone                     bool
}

// HealthCount is one group: the exact count and the first rows.
type HealthCount struct {
	Group string
	Count int
	Rows  []HealthRow
}

func scanHealthRow(s scanner) (HealthRow, error) {
	var (
		h           HealthRow
		title, root string
	)
	err := s.Scan(&h.ID, &title, &root, &h.FolderID, &h.LibraryID, &h.Refused, &h.FolderGone)
	if title == "" {
		title = filepath.Base(root)
	}
	h.Title = title
	return h, err
}

// HealthPage returns one page of a group (rows ordered by title) and the group's exact count, for the books the
// scope may see.
func (r *Repo) HealthPage(ctx context.Context, sc Scope, group string, offset, limit int) ([]HealthRow, int, error) {
	cond, ok := healthCond[group]
	if !ok {
		return nil, 0, ErrUnknownHealthGroup
	}
	where, wargs := scopeWhere(sc)
	from := ` FROM books b JOIN libraries l ON l.id = b.library_id WHERE ` + where + ` AND (` + cond + `)`
	var n int
	if err := r.st.Reader().QueryRowContext(ctx, `SELECT count(*)`+from, wargs...).Scan(&n); err != nil {
		return nil, 0, err
	}
	if n == 0 || limit <= 0 || offset >= n {
		return nil, n, nil
	}
	rows, err := many(ctx, r.st.Reader(), scanHealthRow,
		`SELECT b.id, b.title, b.root_path, b.folder_id, b.library_id, COALESCE(b.refused_reason, ''),
  NOT EXISTS (SELECT 1 FROM books p WHERE p.folder_id = b.folder_id AND p.missing_since IS NULL)`+from+
			` ORDER BY b.sort_title COLLATE NOCASE, b.root_path LIMIT ? OFFSET ?`,
		append(append([]any{}, wargs...), limit, offset)...)
	return rows, n, err
}

// Health returns every group with its exact count and at most perGroup rows (0 means 5).
func (r *Repo) Health(ctx context.Context, sc Scope, perGroup int) ([]HealthCount, error) {
	if perGroup <= 0 {
		perGroup = 5
	}
	out := make([]HealthCount, 0, len(HealthGroups))
	for _, g := range HealthGroups {
		rows, n, err := r.HealthPage(ctx, sc, g, 0, perGroup)
		if err != nil {
			return nil, err
		}
		out = append(out, HealthCount{Group: g, Count: n, Rows: rows})
	}
	return out, nil
}

// HealthTotal is the sum of every group's exact count, with no rows read. The Libraries page needs only this
// number for its "needs a look" line, so it no longer fetches and sorts a preview of every group on each load.
func (r *Repo) HealthTotal(ctx context.Context, sc Scope) (int, error) {
	where, wargs := scopeWhere(sc)
	total := 0
	for _, g := range HealthGroups {
		var n int
		if err := r.st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM books b JOIN libraries l ON l.id = b.library_id WHERE `+
			where+` AND (`+healthCond[g]+`)`, wargs...).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
