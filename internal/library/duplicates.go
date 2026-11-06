package library

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/textmatch"
)

// Finding two copies of one audiobook (S4). Nothing here opens a file: it reads the sizes and quick hashes the
// scan already stored, and the details the book already has. Detection runs at page load, no job.

// Tiers of a duplicate group.
const (
	// DupSameAudio: the same files, by size and quick hash, whatever their names or order.
	DupSameAudio = "same_audio"
	// DupSameDetails: the same title, first author and first narrator, and nearly the same length.
	DupSameDetails = "same_details"
)

const (
	// dupLengthSlackMs and dupLengthSlackPct: a different edition has another length, so "the same details"
	// also needs lengths within 60 seconds or 2 percent.
	dupLengthSlackMs  = 60_000
	dupLengthSlackPct = 2
)

// DuplicateBook is one copy on the Duplicates page.
type DuplicateBook struct {
	ID, LibraryID, Title, RootPath string
	Files                          int
	Bytes, DurationMs, AddedAt     int64
	// Listeners is how many people have listening progress on the copy; Bookmarkers how many have bookmarks.
	Listeners, Bookmarkers int

	sig, author, narrator string
}

// DuplicateGroup is copies of one book; Books[0] is the oldest, the one the page suggests keeping.
type DuplicateGroup struct {
	Tier  string
	Books []DuplicateBook
}

const dupBookSelect = `SELECT b.id, b.library_id, b.title, b.root_path, b.duration_ms, b.added_at,
  (SELECT count(*) FROM audio_files a WHERE a.book_id = b.id AND a.missing_since IS NULL),
  COALESCE((SELECT sum(a.size) FROM audio_files a WHERE a.book_id = b.id AND a.missing_since IS NULL), 0),
  COALESCE((SELECT group_concat(a.size || ':' || a.quick_hash, ',') FROM audio_files a WHERE a.book_id = b.id AND a.missing_since IS NULL), ''),
  (SELECT count(*) FROM progress p WHERE p.book_id = b.id),
  (SELECT count(DISTINCT m.user_id) FROM bookmarks m WHERE m.book_id = b.id AND m.deleted_at IS NULL),
  COALESCE((SELECT p.name FROM book_people bp JOIN people p ON p.id = bp.person_id WHERE bp.book_id = b.id AND bp.role = 'author'
    ORDER BY bp.idx, p.name, p.id LIMIT 1), ''),
  COALESCE((SELECT p.name FROM book_people bp JOIN people p ON p.id = bp.person_id WHERE bp.book_id = b.id AND bp.role = 'narrator'
    ORDER BY bp.idx, p.name, p.id LIMIT 1), '')`

func scanDupBook(s scanner) (DuplicateBook, error) {
	var b DuplicateBook
	var sig string
	err := s.Scan(&b.ID, &b.LibraryID, &b.Title, &b.RootPath, &b.DurationMs, &b.AddedAt, &b.Files, &b.Bytes, &sig,
		&b.Listeners, &b.Bookmarkers, &b.author, &b.narrator)
	if sig != "" {
		parts := strings.Split(sig, ",")
		sort.Strings(parts) // the order of the files does not matter, only which files there are
		b.sig = strings.Join(parts, ",")
	}
	b.author, b.narrator = textmatch.Norm(b.author), textmatch.Norm(b.narrator)
	return b, err
}

// Duplicates finds the groups of copies among the audiobooks the scope can list: one grouped read and one pass
// in Go. Only books of one library are grouped. A pair marked "not the same" is not offered again.
func (r *Repo) Duplicates(ctx context.Context, sc Scope) ([]DuplicateGroup, error) {
	w := visible(sc, true)
	w.add("b.kind = 'audio'")
	books, err := many(ctx, r.st.Reader(), scanDupBook,
		dupBookSelect+fromVisible+` WHERE `+w.sql()+` ORDER BY b.library_id, b.added_at, b.id`, w.args...)
	if err != nil {
		return nil, err
	}
	pairs, err := r.notDuplicatePairs(ctx)
	if err != nil {
		return nil, err
	}
	return groupDuplicates(books, pairs), nil
}

// notDuplicatePairs is every "not the same" pair, as "a|b" with a < b.
func (r *Repo) notDuplicatePairs(ctx context.Context) (map[string]bool, error) {
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (string, error) {
		var a, b string
		return a + "|" + b, s.Scan(&a, &b)
	}, `SELECT a_id, b_id FROM book_not_duplicates`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, k := range rows {
		out[k] = true
	}
	return out, nil
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// closeLengths says whether two durations are within 60 seconds or 2 percent; a missing length never is.
func closeLengths(a, b int64) bool {
	if a <= 0 || b <= 0 {
		return false
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= dupLengthSlackMs || diff*100 <= max(a, b)*dupLengthSlackPct
}

// groupDuplicates is the pass in Go. books are in library, then oldest-first order.
func groupDuplicates(books []DuplicateBook, notSame map[string]bool) []DuplicateGroup {
	var audio, details []DuplicateGroup

	// Same audio: the same sorted list of size:hash, in one library. Cluster around the oldest copy; a copy
	// marked "not the same" against it is left out.
	byAudio := map[string][]int{}
	var audioKeys []string
	for i, b := range books {
		if b.sig == "" {
			continue
		}
		k := b.LibraryID + "|" + b.sig
		if _, seen := byAudio[k]; !seen {
			audioKeys = append(audioKeys, k)
		}
		byAudio[k] = append(byAudio[k], i)
	}
	sameAudio := func(a, b DuplicateBook) bool { return a.sig != "" && a.LibraryID == b.LibraryID && a.sig == b.sig }
	for _, k := range audioKeys {
		idx := byAudio[k]
		anchor := books[idx[0]]
		g := DuplicateGroup{Tier: DupSameAudio, Books: []DuplicateBook{anchor}}
		for _, i := range idx[1:] {
			if !notSame[pairKey(anchor.ID, books[i].ID)] {
				g.Books = append(g.Books, books[i])
			}
		}
		if len(g.Books) > 1 {
			audio = append(audio, g)
		}
	}

	// Same details: title, first author and first narrator all present and equal, lengths close, and not
	// already a same-audio pair.
	type key struct{ lib, title, author, narrator string }
	buckets := map[key][]int{}
	var order []key
	for i, b := range books {
		t := textmatch.Norm(b.Title)
		if t == "" || b.author == "" || b.narrator == "" || b.DurationMs <= 0 {
			continue
		}
		k := key{b.LibraryID, t, b.author, b.narrator}
		if _, seen := buckets[k]; !seen {
			order = append(order, k)
		}
		buckets[k] = append(buckets[k], i)
	}
	for _, k := range order {
		idx := buckets[k]
		taken := map[int]bool{}
		for n, i := range idx {
			if taken[i] {
				continue
			}
			anchor := books[i]
			g := DuplicateGroup{Tier: DupSameDetails, Books: []DuplicateBook{anchor}}
			var members []int
			for _, j := range idx[n+1:] {
				o := books[j]
				if !taken[j] && closeLengths(anchor.DurationMs, o.DurationMs) && !sameAudio(anchor, o) && !notSame[pairKey(anchor.ID, o.ID)] {
					g.Books = append(g.Books, o)
					members = append(members, j)
				}
			}
			if len(g.Books) > 1 {
				taken[i] = true
				for _, j := range members {
					taken[j] = true
				}
				details = append(details, g)
			}
		}
	}
	return append(audio, details...)
}

// MergePreview is what the confirm page says before a merge: both copies and what would move.
type MergePreview struct {
	Keep, Drop DuplicateBook
	// SameAudio is true when both copies hold the same files; otherwise positions move unscaled.
	SameAudio bool
	// People with a place in the dropped copy, and how many of them also have one in the kept copy (the newer wins).
	Listeners, ListenersBoth int
	Bookmarks, Highlights    int
	Shelves                  int
	// Grants is people with access to the dropped copy alone, and Links the share links on it: both would
	// reach the kept copy.
	Grants, Links int
	Ebooks        int
}

// MergePreview reads the counts for the confirm page. It refuses the same pairs MergeBooks does, except a busy
// file write, which MergeBooks decides in its own transaction.
func (r *Repo) MergePreview(ctx context.Context, keep, drop string) (MergePreview, error) {
	var p MergePreview
	if keep == drop {
		return p, ErrMergeSame
	}
	rd := r.st.Reader()
	load := func(id string) (DuplicateBook, string, error) {
		var kind string
		err := rd.QueryRowContext(ctx, `SELECT kind FROM books WHERE id = ?`, id).Scan(&kind)
		if err != nil || kind != BookAudio {
			return DuplicateBook{}, "", ErrBookNotFound
		}
		b, err := scanDupBook(rd.QueryRowContext(ctx, dupBookSelect+` FROM books b WHERE b.id = ?`, id))
		return b, kind, err
	}
	var err error
	if p.Keep, _, err = load(keep); err != nil {
		return p, err
	}
	if p.Drop, _, err = load(drop); err != nil {
		return p, err
	}
	if p.Keep.LibraryID != p.Drop.LibraryID {
		return p, ErrMergeLibrary
	}
	var listable int
	if err := rd.QueryRowContext(ctx, `SELECT count(*) FROM books b WHERE b.id = ? AND `+Listable("b"), keep).Scan(&listable); err != nil || listable == 0 {
		if err != nil {
			return p, err
		}
		return p, ErrBookNotFound
	}
	p.SameAudio = p.Keep.sig != "" && p.Keep.sig == p.Drop.sig
	count := func(dst *int, q string) {
		if err == nil {
			err = rd.QueryRowContext(ctx, q, sql.Named("from", drop), sql.Named("to", keep)).Scan(dst)
		}
	}
	count(&p.Listeners, `SELECT count(*) FROM progress WHERE book_id = :from`)
	count(&p.ListenersBoth, `SELECT count(*) FROM progress f JOIN progress t ON t.user_id = f.user_id AND t.book_id = :to WHERE f.book_id = :from`)
	count(&p.Bookmarks, `SELECT count(*) FROM bookmarks WHERE book_id = :from AND deleted_at IS NULL`)
	count(&p.Highlights, `SELECT count(*) FROM highlights WHERE book_id = :from AND deleted_at IS NULL`)
	count(&p.Shelves, `SELECT count(*) FROM shelf_items WHERE book_id = :from`)
	count(&p.Grants, `SELECT count(*) FROM grants g WHERE g.book_id = :from AND NOT EXISTS
  (SELECT 1 FROM grants k WHERE k.book_id = :to AND k.user_id = g.user_id)`)
	count(&p.Links, `SELECT count(*) FROM share_links WHERE book_id = :from AND revoked_at IS NULL`)
	count(&p.Ebooks, `SELECT count(*) FROM ebook_files WHERE book_id = :from`)
	return p, err
}

// MergedFolderRow is a merged-away folder for the restore list.
type MergedFolderRow struct {
	LibraryID, RootPath, KeptID, KeptTitle string
	MergedAt                               int64
}

// MergedFolderList lists the folders merged away whose kept book the scope can list, newest first.
func (r *Repo) MergedFolderList(ctx context.Context, sc Scope) ([]MergedFolderRow, error) {
	w := visible(sc, false)
	return many(ctx, r.st.Reader(), func(s scanner) (MergedFolderRow, error) {
		var m MergedFolderRow
		return m, s.Scan(&m.LibraryID, &m.RootPath, &m.KeptID, &m.KeptTitle, &m.MergedAt)
	}, `SELECT f.library_id, f.root_path, f.to_id, b.title, f.merged_at FROM merged_folders f JOIN books b ON b.id = f.to_id
  JOIN libraries l ON l.id = b.library_id WHERE `+w.sql()+` ORDER BY f.merged_at DESC, f.root_path`, w.args...)
}
