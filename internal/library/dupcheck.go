package library

import (
	"context"
	"sort"
	"strings"
)

// The "already in the library" check for an upload (decision 0045). It reuses the Duplicates rules: the same
// file is an equal size and quick hash (the pairs the scan stored), and "looks like the same book" is SameDetails.
// Every lookup is filtered in SQL by what the asking person may list, so a title never leaks across grants.

// Tiers of an upload match (uploads.dup_tier).
const (
	MatchSameFiles   = "same_files"
	MatchSomeFiles   = "some_files"
	MatchSameDetails = DupSameDetails
)

// maxMatches is how many books one answer names.
const maxMatches = 3

// SizeQuick is one file's size and quick hash.
type SizeQuick struct {
	Size  int64
	Quick string
}

// DupMatch is one library book that an upload matches. Matched and Total count the upload's distinct files.
type DupMatch struct {
	BookID, Title, Tier string
	Matched, Total      int
}

// dupChunk is how many size and hash pairs one query carries.
const dupChunk = 200

// FileTier is the tier of an upload whose files match a book in part: same_files when every file matches;
// some_files only when the matched files are at least half of the upload's bytes (a publisher's intro or credits
// file shared across a series is not the same book); otherwise "".
func FileTier(matched, total int, matchedBytes, totalBytes int64) string {
	switch {
	case matched <= 0 || total <= 0:
		return ""
	case matched >= total:
		return MatchSameFiles
	case matchedBytes*2 >= totalBytes:
		return MatchSomeFiles
	}
	return ""
}

// FileMatches finds the books of one library, listable by sc, that hold files with the given size and quick hash.
// A book that holds every one of the pairs is same_files; one that holds some is some_files. Best tier first,
// at most three books.
func (r *Repo) FileMatches(ctx context.Context, sc Scope, libraryID string, pairs []SizeQuick) ([]DupMatch, error) {
	want := map[SizeQuick]bool{}
	for _, p := range pairs {
		if p.Size > 0 && p.Quick != "" {
			want[SizeQuick{p.Size, strings.ToLower(p.Quick)}] = true
		}
	}
	if len(want) == 0 {
		return nil, nil
	}
	var totalBytes int64
	list := make([]SizeQuick, 0, len(want))
	for p := range want {
		list = append(list, p)
		totalBytes += p.Size
	}
	w := visible(sc, true)
	w.add("b.library_id = ?", libraryID)
	type hit struct {
		id, title string
		added     int64
		p         SizeQuick
	}
	var hits []hit
	// Chunks keep the OR chain far below SQLite's expression depth limit; every pair stays a bound parameter.
	for lo := 0; lo < len(list); lo += dupChunk {
		part := list[lo:min(lo+dupChunk, len(list))]
		var or strings.Builder
		var args []any
		for _, p := range part {
			if or.Len() > 0 {
				or.WriteString(" OR ")
			}
			or.WriteString("(size = ? AND quick_hash = ?)")
			args = append(args, p.Size, p.Quick)
		}
		cond := or.String()
		all := append(append([]any{}, args...), args...)
		got, err := many(ctx, r.st.Reader(), func(s scanner) (hit, error) {
			var h hit
			return h, s.Scan(&h.id, &h.title, &h.added, &h.p.Size, &h.p.Quick)
		}, `SELECT b.id, b.title, b.added_at, x.size, x.quick_hash FROM (
  SELECT book_id, size, quick_hash FROM audio_files WHERE missing_since IS NULL AND (`+cond+`)
  UNION ALL
  SELECT book_id, size, quick_hash FROM ebook_files WHERE origin = 'library' AND missing_since IS NULL AND book_id IS NOT NULL AND (`+cond+`)
  ) x JOIN books b ON b.id = x.book_id JOIN libraries l ON l.id = b.library_id WHERE `+w.sql(), append(all, w.args...)...)
		if err != nil {
			return nil, err
		}
		hits = append(hits, got...)
	}
	byBook := map[string]*DupMatch{}
	seen := map[string]map[SizeQuick]bool{}
	added := map[string]int64{}
	bytes := map[string]int64{}
	var order []string
	for _, h := range hits {
		m := byBook[h.id]
		if m == nil {
			m = &DupMatch{BookID: h.id, Title: h.title, Total: len(want)}
			byBook[h.id], seen[h.id], added[h.id] = m, map[SizeQuick]bool{}, h.added
			order = append(order, h.id)
		}
		if !seen[h.id][h.p] {
			seen[h.id][h.p] = true
			m.Matched++
			bytes[h.id] += h.p.Size
		}
	}
	out := make([]DupMatch, 0, len(order))
	for _, id := range order {
		m := *byBook[id]
		if m.Tier = FileTier(m.Matched, m.Total, bytes[id], totalBytes); m.Tier != "" {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Tier == MatchSameFiles) != (b.Tier == MatchSameFiles) {
			return a.Tier == MatchSameFiles
		}
		if a.Matched != b.Matched {
			return a.Matched > b.Matched
		}
		if added[a.BookID] != added[b.BookID] {
			return added[a.BookID] < added[b.BookID]
		}
		return a.BookID < b.BookID
	})
	if len(out) > maxMatches {
		out = out[:maxMatches]
	}
	return out, nil
}

// DetailMatches finds audiobooks of one library, listable by sc, that SameDetails says look like the given one.
// SQL narrows by the scope and by a length window; the exact rule runs on those few rows.
func (r *Repo) DetailMatches(ctx context.Context, sc Scope, libraryID, title, author, narrator string, durMs int64) ([]DupMatch, error) {
	want := DupKey{Title: title, Author: author, Narrator: narrator, DurationMs: durMs}
	if !SameDetails(want, want) {
		return nil, nil
	}
	slack := max(int64(dupLengthSlackMs), durMs/40) // a superset of the 2 percent rule; SameDetails decides
	w := visible(sc, true)
	w.add("b.kind = 'audio'")
	w.add("b.library_id = ?", libraryID)
	w.add("abs(b.duration_ms - ?) <= ?", durMs, slack)
	books, err := many(ctx, r.st.Reader(), func(s scanner) (DuplicateBook, error) {
		var b DuplicateBook
		err := s.Scan(&b.ID, &b.LibraryID, &b.Title, &b.DurationMs, &b.AddedAt, &b.author, &b.narrator)
		return b, err
	}, dupLightSelect+fromVisible+` WHERE `+w.sql()+` ORDER BY b.added_at, b.id`, w.args...)
	if err != nil {
		return nil, err
	}
	var out []DupMatch
	for _, b := range books {
		if SameDetails(want, b.key()) {
			out = append(out, DupMatch{BookID: b.ID, Title: b.Title, Tier: MatchSameDetails})
			if len(out) == maxMatches {
				break
			}
		}
	}
	return out, nil
}

// ListableTitle returns a book's title only when sc may list it, so a held upload names a book only to someone
// who can still see it.
func (r *Repo) ListableTitle(ctx context.Context, sc Scope, bookID string) (string, bool) {
	w := visible(sc, true)
	w.add("b.id = ?", bookID)
	t, err := many(ctx, r.st.Reader(), func(s scanner) (string, error) {
		var t string
		return t, s.Scan(&t)
	}, `SELECT b.title`+fromVisible+` WHERE `+w.sql(), w.args...)
	if err != nil || len(t) == 0 {
		return "", false
	}
	return t[0], true
}

// SameAudioCopies lists the other books of the same library, listable by sc, that hold the same audio files as
// bookID (the Duplicates same-audio signature), oldest first, minus pairs marked "not the same".
func (r *Repo) SameAudioCopies(ctx context.Context, sc Scope, bookID string) ([]string, error) {
	w := visible(sc, true)
	w.add("b.kind = 'audio'")
	w.add("b.id <> ?", bookID)
	w.add("b.library_id = (SELECT library_id FROM books WHERE id = ?)", bookID)
	w.add(dupSigSQL("b.id")+" = "+dupSigSQL("?"), bookID)
	w.add(`NOT EXISTS (SELECT 1 FROM book_not_duplicates n WHERE (n.a_id = b.id AND n.b_id = ?) OR (n.a_id = ? AND n.b_id = b.id))`, bookID, bookID)
	return many(ctx, r.st.Reader(), func(s scanner) (string, error) {
		var id string
		return id, s.Scan(&id)
	}, `SELECT b.id`+fromVisible+` WHERE `+w.sql()+` ORDER BY b.added_at, b.id`, w.args...)
}
