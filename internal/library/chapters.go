package library

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Chapter edits (L3a). An edit is the chapters rows themselves, written with source "user" (typed
// by a person) or "online" (taken from a lookup); every reader of chapters sees it without change.

// Limits of a chapter list.
const (
	MaxChapters      = 1000
	MaxChapterTitle  = 500
	chapterFitSlackM = 1000 // ms a last end may pass the duration before the list no longer fits
)

// Codes of a ChapterError.
const (
	ChapterEmpty        = "empty"
	ChapterTooMany      = "too_many"
	ChapterFirstNotZero = "first_not_zero"
	ChapterOutOfOrder   = "out_of_order"
	ChapterPastEnd      = "past_end"
	ChapterNoTitle      = "no_title"
	ChapterTitleLong    = "title_long"
)

// ChaptersPastEnd is an SQL condition for books aliased b: an edited row ends more than a second
// after the book does, so the list no longer fits the audio.
const ChaptersPastEnd = `EXISTS (SELECT 1 FROM chapters c WHERE c.book_id = b.id AND c.source IN ('user','online') AND c.end_ms > b.duration_ms + 1000)`

// ChaptersOverridden reports whether a person's edit (source user or online) is present in rows.
func ChaptersOverridden(rows []Chapter) bool {
	for _, c := range rows {
		if c.Source == "user" || c.Source == "online" {
			return true
		}
	}
	return false
}

// ChaptersPastDuration reports whether the last row ends more than a second after durationMs.
func ChaptersPastDuration(rows []Chapter, durationMs int64) bool {
	return len(rows) > 0 && rows[len(rows)-1].EndMs > durationMs+chapterFitSlackM
}

// ChapterDraft is one row of the form: a start and a title; the end is derived.
type ChapterDraft struct {
	StartMs int64
	Title   string
}

// ChapterError says which row (0-based; -1 for the whole list) broke which rule.
type ChapterError struct {
	Row  int
	Code string
}

func (e *ChapterError) Error() string { return fmt.Sprintf("chapters: row %d: %s", e.Row, e.Code) }

// CleanChapterTitle trims a title and drops control characters.
func CleanChapterTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// BuildChapters checks a draft and returns the whole stored list: first start 0, starts strictly
// rising, each end the next start, the last end durationMs.
func BuildChapters(rows []ChapterDraft, durationMs int64, source string) ([]Chapter, *ChapterError) {
	if len(rows) == 0 {
		return nil, &ChapterError{-1, ChapterEmpty}
	}
	if len(rows) > MaxChapters {
		return nil, &ChapterError{-1, ChapterTooMany}
	}
	out := make([]Chapter, len(rows))
	for i, r := range rows {
		title := CleanChapterTitle(r.Title)
		switch {
		case title == "":
			return nil, &ChapterError{i, ChapterNoTitle}
		case utf8.RuneCountInString(title) > MaxChapterTitle:
			return nil, &ChapterError{i, ChapterTitleLong}
		case i == 0 && r.StartMs != 0:
			return nil, &ChapterError{i, ChapterFirstNotZero}
		case i > 0 && r.StartMs <= rows[i-1].StartMs:
			return nil, &ChapterError{i, ChapterOutOfOrder}
		case r.StartMs >= durationMs:
			return nil, &ChapterError{i, ChapterPastEnd}
		}
		out[i] = Chapter{Idx: i, StartMs: r.StartMs, Title: title, Source: source}
		if i > 0 {
			out[i-1].EndMs = r.StartMs
		}
	}
	out[len(out)-1].EndMs = durationMs
	return out, nil
}

// ChaptersRev is a short fingerprint of a stored list; the form carries it so a save can tell
// that the rows changed while it was open.
func ChaptersRev(rows []Chapter) string {
	h := sha256.New()
	for _, c := range rows {
		fmt.Fprintf(h, "%d\x00%d\x00%d\x00%s\x00%s\x00", c.Idx, c.StartMs, c.EndMs, c.Title, c.Source)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
