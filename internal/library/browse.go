package library

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Browse reads: the lists, cards, facets and detail pages the listener sees. Every query carries the
// caller's Scope in SQL (Books on alias b, Libraries on alias l) and, for lists, Listable("b"); nothing
// is filtered in Go. Counts and facets use the same WHERE as the list they describe.

// Sort is the order of a book list.
type Sort string

// The sorts a list can use.
const (
	SortAdded    Sort = "added"
	SortTitle    Sort = "title"
	SortAuthor   Sort = "author"
	SortDuration Sort = "duration"
)

// ParseSort maps a query value to a Sort; anything unknown or empty is SortAdded.
func ParseSort(s string) Sort {
	switch Sort(s) {
	case SortTitle, SortAuthor, SortDuration:
		return Sort(s)
	}
	return SortAdded
}

// MaxTitleRunes is the longest title filter; a longer one is cut.
const MaxTitleRunes = 100

const (
	defaultLimit  = 48
	maxLimit      = 120
	maxCardIDs    = 200
	maxSeriesList = 500
)

// ListQuery selects and orders a page of books. Empty fields do not filter.
type ListQuery struct {
	LibraryID                      string
	Sort                           Sort
	Genres                         []string // every genre must match
	AuthorID, NarratorID, SeriesID string
	// Title keeps the books whose title contains it, ignoring case (at most 100 runes; % and _ are
	// literal). MinDurationMs is inclusive and MaxDurationMs exclusive; 0 leaves that side open.
	Title                        string
	MinDurationMs, MaxDurationMs int64
	// HasCover keeps the books that have a cover file (the setup page's cover wall).
	HasCover bool
	// Show narrows to a person's listening state: ShowUnfinished (started, not finished),
	// ShowNotStarted (no progress or position 0) or ShowFinished. It reads UserID's progress, so
	// UserID is required with it (ErrInvalid otherwise); any other value is ErrInvalid.
	Show   string
	UserID string
	Cursor string // opaque, from BookPage.Next
	Limit  int    // 0 = 48, clamped to 1..120
}

// Show values of ListQuery.Show.
const (
	ShowUnfinished = "unfinished"
	ShowNotStarted = "not_started"
	ShowFinished   = "finished"
	// ShowShared keeps the books shared with the person (Scope.Shared); it needs no user.
	ShowShared = "shared"
)

// checkShow is the ErrInvalid a Show filter without a user or with an unknown value earns.
func checkShow(q ListQuery) error {
	switch q.Show {
	case "", ShowShared:
		return nil
	case ShowUnfinished, ShowNotStarted, ShowFinished:
		if q.UserID == "" {
			return invalid("show needs a user")
		}
		return nil
	}
	return invalid("show is not valid")
}

// PersonRef names a person on a card.
type PersonRef struct{ ID, Name string }

// SeriesRef names a series and the book's place in it.
type SeriesRef struct{ ID, Name, Sequence string }

// CoverRef is what a card needs to draw a cover: the hash for the URL, and the colours.
type CoverRef struct {
	SHA, Mime, Tint string
	Width, Height   int
	Accent          map[string]string // per-theme accent colours, nil when the cover has none
}

// BookCard is a book as every list shows it.
type BookCard struct {
	ID, LibraryID, Title, Subtitle string
	Authors, Narrators             []PersonRef
	Series                         *SeriesRef // the book's first series
	DurationMs, AddedAt            int64
	PublishedYear                  int
	Cover                          *CoverRef
}

// BookPage is one page of a list. Next is "" on the last page; Total counts the whole filtered list.
type BookPage struct {
	Books []BookCard
	Next  string
	Total int
}

// Facet is a value and how many visible books carry it.
type Facet struct {
	Value string
	Count int
}

// LibrarySummary is a visible library with its number of listable books the person may see.
type LibrarySummary struct {
	Library
	Books int
}

// BookDetail is everything the book page shows.
type BookDetail struct {
	Card                BookCard
	Book                Book
	Series              []SeriesRef
	Genres, Tags        []string
	Chapters            []Chapter
	Parts, MissingParts int
}

// Person is an author or narrator with the number of visible books in each role.
type Person struct {
	ID, Name, Description string
	AuthorOf, NarratorOf  int
}

// SeriesPage is a series with its visible books in reading order.
type SeriesPage struct {
	ID, Name, Description string
	Books                 []BookCard
}

// cursor is the decoded form of ListQuery.Cursor. Keys a sort does not use stay nil.
type cursor struct {
	S string  `json:"s"`
	N *int64  `json:"n,omitempty"`
	T *string `json:"t,omitempty"`
	A *string `json:"a,omitempty"`
	I string  `json:"i"`
}

func encodeCursor(c cursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor validates a cursor for sort s: bad base64 or JSON, another sort, an empty id or a
// missing key are ErrInvalid.
func decodeCursor(s Sort, str string) (cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(str)
	if err != nil {
		return cursor{}, invalid("cursor is not valid")
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return cursor{}, invalid("cursor is not valid")
	}
	if c.S != string(s) || c.I == "" {
		return cursor{}, invalid("cursor is not valid")
	}
	ok := false
	switch s {
	case SortAdded, SortDuration:
		ok = c.N != nil
	case SortTitle:
		ok = c.T != nil
	case SortAuthor:
		ok = c.A != nil && c.T != nil
	}
	if !ok {
		return cursor{}, invalid("cursor is not valid")
	}
	return c, nil
}

// whereClause is a WHERE body (conditions joined by AND) with its arguments, in order.
type whereClause struct {
	conds []string
	args  []any
}

func (w *whereClause) add(cond string, args ...any) {
	w.conds = append(w.conds, cond)
	w.args = append(w.args, args...)
}

func (w *whereClause) sql() string { return strings.Join(w.conds, " AND ") }

// visible is the scope and listable conditions every list shares. The FROM clause must be
// "books b JOIN libraries l ON l.id = b.library_id".
func visible(sc Scope, listable bool) *whereClause {
	w := &whereClause{}
	w.add("("+sc.Books.SQL+")", sc.Books.Args...)
	w.add("("+sc.Libraries.SQL+")", sc.Libraries.Args...)
	if listable {
		w.add(Listable("b"))
	}
	return w
}

// likeEscape makes s literal inside LIKE ... ESCAPE '\'.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// where builds the list WHERE for q: scope, listable and every filter.
func where(sc Scope, q ListQuery) *whereClause {
	return whereExcept(sc, q, "")
}

// whereExcept is where without one filter ("author", "narrator" or "series"), which is how a facet
// ignores its own choice while applying every other.
func whereExcept(sc Scope, q ListQuery, skip string) *whereClause {
	w := visible(sc, true)
	if q.LibraryID != "" {
		w.add("b.library_id = ?", q.LibraryID)
	}
	for _, g := range q.Genres {
		w.add("EXISTS (SELECT 1 FROM book_labels x WHERE x.book_id = b.id AND x.kind = 'genre' AND x.value = ?)", g)
	}
	if q.AuthorID != "" && skip != "author" {
		w.add("EXISTS (SELECT 1 FROM book_people x WHERE x.book_id = b.id AND x.role = 'author' AND x.person_id = ?)", q.AuthorID)
	}
	if q.NarratorID != "" && skip != "narrator" {
		w.add("EXISTS (SELECT 1 FROM book_people x WHERE x.book_id = b.id AND x.role = 'narrator' AND x.person_id = ?)", q.NarratorID)
	}
	if q.SeriesID != "" && skip != "series" {
		w.add("EXISTS (SELECT 1 FROM book_series x WHERE x.book_id = b.id AND x.series_id = ?)", q.SeriesID)
	}
	if t := cutRunes(strings.TrimSpace(q.Title), MaxTitleRunes); t != "" {
		w.add("("+titleKey+") LIKE '%' || ? || '%' ESCAPE '\\'", likeEscape(t))
	}
	if q.HasCover {
		w.add("b.cover_id IS NOT NULL")
	}
	if q.MinDurationMs > 0 {
		w.add("b.duration_ms >= ?", q.MinDurationMs)
	}
	if q.MaxDurationMs > 0 {
		w.add("b.duration_ms < ?", q.MaxDurationMs)
	}
	switch {
	case q.Show == "":
	case q.Show == ShowShared && sc.Shared.SQL == "":
		w.add("0")
	case q.Show == ShowShared:
		w.add("b.id IN (SELECT book_id FROM ("+sc.Shared.SQL+"))", sc.Shared.Args...)
	case q.UserID == "":
		w.add("0") // checkShow refuses this; fail closed if a caller skips it
	case q.Show == ShowUnfinished:
		w.add("EXISTS (SELECT 1 FROM progress p WHERE p.user_id = ? AND p.book_id = b.id AND p.finished_at IS NULL AND p.position_ms > 0)", q.UserID)
	case q.Show == ShowNotStarted:
		w.add("NOT EXISTS (SELECT 1 FROM progress p WHERE p.user_id = ? AND p.book_id = b.id AND (p.finished_at IS NOT NULL OR p.position_ms > 0))", q.UserID)
	case q.Show == ShowFinished:
		w.add("EXISTS (SELECT 1 FROM progress p WHERE p.user_id = ? AND p.book_id = b.id AND p.finished_at IS NOT NULL)", q.UserID)
	default:
		w.add("0")
	}
	return w
}

func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

const fromVisible = ` FROM books b JOIN libraries l ON l.id = b.library_id`

const titleKey = `COALESCE(NULLIF(b.sort_title, ''), b.title)`

const authorKey = `COALESCE((SELECT COALESCE(NULLIF(p.sort_name, ''), p.name) FROM book_people bp
  JOIN people p ON p.id = bp.person_id WHERE bp.book_id = b.id AND bp.role = 'author'
  ORDER BY bp.idx, bp.person_id LIMIT 1), char(1114111))`

// listRow is one row of the key query: the id plus the values a cursor needs.
type listRow struct {
	id         string
	added, dur int64
	tk, ak     string
}

// ListBooks returns one page of the books the scope may see, in q.Sort order.
func (r *Repo) ListBooks(ctx context.Context, sc Scope, q ListQuery) (BookPage, error) {
	if err := checkShow(q); err != nil {
		return BookPage{}, err
	}
	q.Sort = ParseSort(string(q.Sort))
	limit := q.Limit
	switch {
	case limit == 0:
		limit = defaultLimit
	case limit < 1:
		limit = 1
	case limit > maxLimit:
		limit = maxLimit
	}
	var cur *cursor
	if q.Cursor != "" {
		c, err := decodeCursor(q.Sort, q.Cursor)
		if err != nil {
			return BookPage{}, err
		}
		cur = &c
	}
	w := where(sc, q)

	ak := `''`
	if q.Sort == SortAuthor {
		ak = authorKey
	}
	query := `WITH k AS (SELECT b.id AS id, b.added_at AS added_at, b.duration_ms AS duration_ms, ` + titleKey + ` AS tk, ` +
		ak + ` AS ak` + fromVisible + ` WHERE ` + w.sql() + `) SELECT id, added_at, duration_ms, tk, ak FROM k`
	args := append([]any(nil), w.args...)
	if cur != nil {
		switch q.Sort {
		case SortAdded:
			query += ` WHERE (added_at < ? OR (added_at = ? AND id < ?))`
			args = append(args, *cur.N, *cur.N, cur.I)
		case SortDuration:
			query += ` WHERE (duration_ms < ? OR (duration_ms = ? AND id < ?))`
			args = append(args, *cur.N, *cur.N, cur.I)
		case SortTitle:
			query += ` WHERE (tk > ? COLLATE NOCASE OR (tk = ? COLLATE NOCASE AND id > ?))`
			args = append(args, *cur.T, *cur.T, cur.I)
		case SortAuthor:
			query += ` WHERE (ak > ? COLLATE NOCASE OR (ak = ? COLLATE NOCASE AND (tk > ? COLLATE NOCASE OR (tk = ? COLLATE NOCASE AND id > ?))))`
			args = append(args, *cur.A, *cur.A, *cur.T, *cur.T, cur.I)
		}
	}
	switch q.Sort {
	case SortAdded:
		query += ` ORDER BY added_at DESC, id DESC`
	case SortDuration:
		query += ` ORDER BY duration_ms DESC, id DESC`
	case SortTitle:
		query += ` ORDER BY tk COLLATE NOCASE ASC, id ASC`
	case SortAuthor:
		query += ` ORDER BY ak COLLATE NOCASE ASC, tk COLLATE NOCASE ASC, id ASC`
	}
	query += ` LIMIT ?`
	args = append(args, limit+1)

	rows, err := many(ctx, r.st.Reader(), func(s scanner) (listRow, error) {
		var lr listRow
		return lr, s.Scan(&lr.id, &lr.added, &lr.dur, &lr.tk, &lr.ak)
	}, query, args...)
	if err != nil {
		return BookPage{}, err
	}

	var total int
	if err := r.st.Reader().QueryRowContext(ctx, `SELECT COUNT(*)`+fromVisible+` WHERE `+w.sql(), w.args...).Scan(&total); err != nil {
		return BookPage{}, err
	}
	page := BookPage{Total: total}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		c := cursor{S: string(q.Sort), I: last.id}
		switch q.Sort {
		case SortAdded:
			c.N = &last.added
		case SortDuration:
			c.N = &last.dur
		case SortTitle:
			c.T = &last.tk
		case SortAuthor:
			c.A, c.T = &last.ak, &last.tk
		}
		page.Next = encodeCursor(c)
	}
	ids := make([]string, len(rows))
	for i, lr := range rows {
		ids[i] = lr.id
	}
	page.Books, err = r.cards(ctx, ids)
	return page, err
}

// CardsByID turns ids (from search, for one) into cards, keeping the input order and dropping every
// id the scope cannot see, unlisted books and unknown ids. More than 200 ids is ErrInvalid.
func (r *Repo) CardsByID(ctx context.Context, sc Scope, ids []string) ([]BookCard, error) {
	if len(ids) > maxCardIDs {
		return nil, invalid("too many ids")
	}
	if len(ids) == 0 {
		return nil, nil
	}
	w := visible(sc, true)
	marks, idArgs := inList(ids)
	w.add("b.id IN ("+marks+")", idArgs...)
	ok, err := many(ctx, r.st.Reader(), func(s scanner) (string, error) {
		var id string
		return id, s.Scan(&id)
	}, `SELECT b.id`+fromVisible+` WHERE `+w.sql(), w.args...)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(ok))
	for _, id := range ok {
		allowed[id] = true
	}
	var keep []string
	for _, id := range ids {
		if allowed[id] {
			keep = append(keep, id)
			delete(allowed, id) // a repeated id appears once
		}
	}
	return r.cards(ctx, keep)
}

func inList(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

// cards hydrates already-authorized book ids into cards, in the given order: one query for the
// books and covers, one for people, one for series. Ids that no longer exist are skipped.
func (r *Repo) cards(ctx context.Context, ids []string) ([]BookCard, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	marks, args := inList(ids)
	db := r.st.Reader()
	byID := make(map[string]*BookCard, len(ids))
	_, err := many(ctx, db, func(s scanner) (struct{}, error) {
		var c BookCard
		var root string
		var year sql.NullInt64
		var sha, mime, tint, accent sql.NullString
		var cw, ch sql.NullInt64
		err := s.Scan(&c.ID, &c.LibraryID, &c.Title, &c.Subtitle, &root, &c.DurationMs, &c.AddedAt, &year,
			&sha, &mime, &tint, &cw, &ch, &accent)
		if err != nil {
			return struct{}{}, err
		}
		if strings.TrimSpace(c.Title) == "" {
			c.Title = filepath.Base(root)
		}
		c.PublishedYear = int(year.Int64)
		if sha.Valid {
			ref := &CoverRef{SHA: sha.String, Mime: mime.String, Tint: tint.String, Width: int(cw.Int64), Height: int(ch.Int64)}
			if accent.Valid {
				var m map[string]string
				if json.Unmarshal([]byte(accent.String), &m) == nil {
					ref.Accent = m
				}
			}
			c.Cover = ref
		}
		byID[c.ID] = &c
		return struct{}{}, nil
	}, `SELECT b.id, b.library_id, b.title, b.subtitle, b.root_path, b.duration_ms, b.added_at, b.published_year,
  c.sha256, c.mime, c.tint, c.width, c.height, c.accent_json
  FROM books b LEFT JOIN covers c ON c.id = b.cover_id WHERE b.id IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	type person struct {
		book, role string
		ref        PersonRef
	}
	people, err := many(ctx, db, func(s scanner) (person, error) {
		var p person
		return p, s.Scan(&p.book, &p.role, &p.ref.ID, &p.ref.Name)
	}, `SELECT bp.book_id, bp.role, p.id, p.name FROM book_people bp JOIN people p ON p.id = bp.person_id
  WHERE bp.book_id IN (`+marks+`) ORDER BY bp.book_id, bp.role, bp.idx, p.id`, args...)
	if err != nil {
		return nil, err
	}
	for _, p := range people {
		c := byID[p.book]
		if c == nil {
			continue
		}
		if p.role == RoleAuthor {
			c.Authors = append(c.Authors, p.ref)
		} else {
			c.Narrators = append(c.Narrators, p.ref)
		}
	}
	type series struct {
		book string
		ref  SeriesRef
	}
	ser, err := many(ctx, db, func(s scanner) (series, error) {
		var x series
		return x, s.Scan(&x.book, &x.ref.ID, &x.ref.Name, &x.ref.Sequence)
	}, `SELECT bs.book_id, s.id, s.name, bs.sequence FROM book_series bs JOIN series s ON s.id = bs.series_id
  WHERE bs.book_id IN (`+marks+`) ORDER BY bs.book_id, bs.idx, s.id`, args...)
	if err != nil {
		return nil, err
	}
	for _, x := range ser {
		if c := byID[x.book]; c != nil && c.Series == nil {
			ref := x.ref
			c.Series = &ref
		}
	}
	out := make([]BookCard, 0, len(ids))
	for _, id := range ids {
		if c := byID[id]; c != nil {
			out = append(out, *c)
		}
	}
	return out, nil
}

// Genres returns the genres of the visible listable books with their counts, most used first.
// libraryID "" counts across every library the scope may see.
func (r *Repo) Genres(ctx context.Context, sc Scope, libraryID string) ([]Facet, error) {
	w := where(sc, ListQuery{LibraryID: libraryID})
	return many(ctx, r.st.Reader(), func(s scanner) (Facet, error) {
		var f Facet
		return f, s.Scan(&f.Value, &f.Count)
	}, `SELECT x.value, COUNT(*) FROM book_labels x JOIN books b ON b.id = x.book_id
  JOIN libraries l ON l.id = b.library_id WHERE x.kind = 'genre' AND `+w.sql()+`
  GROUP BY x.value ORDER BY COUNT(*) DESC, x.value`, w.args...)
}

// VisibleLibraries returns the libraries the scope may see, by name, with their listable book counts.
func (r *Repo) VisibleLibraries(ctx context.Context, sc Scope) ([]LibrarySummary, error) {
	// The subselect's arguments come first: its placeholders precede the WHERE's in the text.
	args := append([]any(nil), sc.Books.Args...)
	args = append(args, sc.Libraries.Args...)
	return many(ctx, r.st.Reader(), func(s scanner) (LibrarySummary, error) {
		var ls LibrarySummary
		return ls, s.Scan(&ls.ID, &ls.Name, &ls.ScanIntervalMin, &ls.CreatedAt, &ls.Books)
	}, `SELECT l.id, l.name, l.scan_interval_min, l.created_at,
  (SELECT COUNT(*) FROM books b WHERE b.library_id = l.id AND (`+sc.Books.SQL+`) AND `+Listable("b")+`)
  FROM libraries l WHERE (`+sc.Libraries.SQL+`) ORDER BY l.name COLLATE NOCASE, l.id`, args...)
}

// BookDetail returns the book page's data. The scope applies but Listable does not: a missing or
// refused book still has a page. A book outside the scope is ErrNotFound.
func (r *Repo) BookDetail(ctx context.Context, sc Scope, id string) (BookDetail, error) {
	w := visible(sc, false)
	w.add("b.id = ?", id)
	book, err := one(r.st.Reader().QueryRowContext(ctx, `SELECT `+bookCols+` FROM books WHERE id IN
  (SELECT b.id`+fromVisible+` WHERE `+w.sql()+`)`, w.args...), scanBook, "book")
	if err != nil {
		return BookDetail{}, err
	}
	d := BookDetail{Book: book}
	cards, err := r.cards(ctx, []string{book.ID})
	if err != nil {
		return BookDetail{}, err
	}
	if len(cards) != 1 {
		return BookDetail{}, fmt.Errorf("%w: book", ErrNotFound)
	}
	d.Card = cards[0]
	db := r.st.Reader()
	d.Series, err = many(ctx, db, func(s scanner) (SeriesRef, error) {
		var x SeriesRef
		return x, s.Scan(&x.ID, &x.Name, &x.Sequence)
	}, `SELECT s.id, s.name, bs.sequence FROM book_series bs JOIN series s ON s.id = bs.series_id
  WHERE bs.book_id = ? ORDER BY bs.idx, s.id`, book.ID)
	if err != nil {
		return BookDetail{}, err
	}
	labels := func(kind string) ([]string, error) {
		return many(ctx, db, func(s scanner) (string, error) {
			var v string
			return v, s.Scan(&v)
		}, `SELECT value FROM book_labels WHERE book_id = ? AND kind = ? ORDER BY value`, book.ID, kind)
	}
	if d.Genres, err = labels(LabelGenre); err != nil {
		return BookDetail{}, err
	}
	if d.Tags, err = labels(LabelTag); err != nil {
		return BookDetail{}, err
	}
	if d.Chapters, err = r.Chapters(ctx, book.ID); err != nil {
		return BookDetail{}, err
	}
	err = db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(missing_since IS NOT NULL), 0) FROM audio_files WHERE book_id = ?`,
		book.ID).Scan(&d.Parts, &d.MissingParts)
	return d, err
}

// Person returns an author or narrator, or ErrNotFound unless at least one visible listable book
// links them. The counts cover visible listable books only.
func (r *Repo) Person(ctx context.Context, sc Scope, id string) (Person, error) {
	w := visible(sc, true)
	w.add("p.id = ?", id)
	row := r.st.Reader().QueryRowContext(ctx, `SELECT p.id, p.name, p.description,
  COUNT(DISTINCT CASE WHEN bp.role = 'author' THEN b.id END),
  COUNT(DISTINCT CASE WHEN bp.role = 'narrator' THEN b.id END)
  FROM people p JOIN book_people bp ON bp.person_id = p.id JOIN books b ON b.id = bp.book_id
  JOIN libraries l ON l.id = b.library_id WHERE `+w.sql()+` GROUP BY p.id`, w.args...)
	return one(row, func(s scanner) (Person, error) {
		var p Person
		return p, s.Scan(&p.ID, &p.Name, &p.Description, &p.AuthorOf, &p.NarratorOf)
	}, "person")
}

// SeriesInfo returns a series with its visible listable books (at most 500) in reading order, or
// ErrNotFound when no such book exists.
func (r *Repo) SeriesInfo(ctx context.Context, sc Scope, id string) (SeriesPage, error) {
	w := visible(sc, true)
	w.add("bs.series_id = ?", id)
	ids, err := many(ctx, r.st.Reader(), func(s scanner) (string, error) {
		var v string
		return v, s.Scan(&v)
	}, `SELECT b.id`+fromVisible+` JOIN book_series bs ON bs.book_id = b.id WHERE `+w.sql()+`
  ORDER BY CASE WHEN bs.sequence GLOB '[0-9]*' THEN CAST(bs.sequence AS REAL) END ASC NULLS LAST,
  `+titleKey+` COLLATE NOCASE, b.id LIMIT ?`, append(append([]any(nil), w.args...), maxSeriesList)...)
	if err != nil {
		return SeriesPage{}, err
	}
	if len(ids) == 0 {
		return SeriesPage{}, fmt.Errorf("%w: series", ErrNotFound)
	}
	sp := SeriesPage{ID: id}
	err = r.st.Reader().QueryRowContext(ctx, `SELECT name, description FROM series WHERE id = ?`, id).Scan(&sp.Name, &sp.Description)
	if err != nil {
		return SeriesPage{}, err
	}
	sp.Books, err = r.cards(ctx, ids)
	return sp, err
}

// NamedFacet is a person or series and how many visible books carry it under the other filters.
type NamedFacet struct {
	ID, Name string
	Count    int
}

const defaultFacetLimit = 30

// PeopleFacet lists the authors (role "author") or narrators ("narrator") of the books q selects,
// most books first. It ignores q's own role filter and applies every other, so the chip menu shows
// what is left to choose. limit 0 is 30. A person chosen in q who has no visible book under the
// other filters is still listed, with count 0, so the chip can name them.
func (r *Repo) PeopleFacet(ctx context.Context, sc Scope, q ListQuery, role string, limit int) ([]NamedFacet, error) {
	if role != RoleAuthor && role != RoleNarrator {
		return nil, invalid("role is not valid")
	}
	if limit <= 0 {
		limit = defaultFacetLimit
	}
	if err := checkShow(q); err != nil {
		return nil, err
	}
	w := whereExcept(sc, q, role)
	args := append([]any{role}, w.args...)
	out, err := many(ctx, r.st.Reader(), scanFacet, `SELECT p.id, p.name, COUNT(DISTINCT b.id)
  FROM book_people bp JOIN people p ON p.id = bp.person_id JOIN books b ON b.id = bp.book_id
  JOIN libraries l ON l.id = b.library_id WHERE bp.role = ? AND `+w.sql()+`
  GROUP BY p.id ORDER BY COUNT(DISTINCT b.id) DESC, p.name COLLATE NOCASE, p.id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	sel := q.AuthorID
	if role == RoleNarrator {
		sel = q.NarratorID
	}
	return r.withChosen(ctx, sc, out, sel, `SELECT p.id, p.name FROM people p JOIN book_people bp ON bp.person_id = p.id
  JOIN books b ON b.id = bp.book_id JOIN libraries l ON l.id = b.library_id WHERE bp.role = ? AND p.id = ? AND `, role)
}

// SeriesFacet is PeopleFacet for series.
func (r *Repo) SeriesFacet(ctx context.Context, sc Scope, q ListQuery, limit int) ([]NamedFacet, error) {
	if limit <= 0 {
		limit = defaultFacetLimit
	}
	if err := checkShow(q); err != nil {
		return nil, err
	}
	w := whereExcept(sc, q, "series")
	out, err := many(ctx, r.st.Reader(), scanFacet, `SELECT s.id, s.name, COUNT(DISTINCT b.id)
  FROM book_series bs JOIN series s ON s.id = bs.series_id JOIN books b ON b.id = bs.book_id
  JOIN libraries l ON l.id = b.library_id WHERE `+w.sql()+`
  GROUP BY s.id ORDER BY COUNT(DISTINCT b.id) DESC, s.name COLLATE NOCASE, s.id LIMIT ?`, append(append([]any(nil), w.args...), limit)...)
	if err != nil {
		return nil, err
	}
	return r.withChosen(ctx, sc, out, q.SeriesID, `SELECT s.id, s.name FROM series s JOIN book_series bs ON bs.series_id = s.id
  JOIN books b ON b.id = bs.book_id JOIN libraries l ON l.id = b.library_id WHERE s.id = ? AND `)
}

func scanFacet(s scanner) (NamedFacet, error) {
	var f NamedFacet
	return f, s.Scan(&f.ID, &f.Name, &f.Count)
}

// withChosen appends the chosen id with count 0 when the facet did not list it and the scope may
// see a listable book that carries it. prefix is a SELECT of id and name ending in "WHERE ... AND ";
// its placeholders are lead (the role, for people) then the id, then the scope's.
func (r *Repo) withChosen(ctx context.Context, sc Scope, list []NamedFacet, id, prefix string, lead ...any) ([]NamedFacet, error) {
	if id == "" {
		return list, nil
	}
	for _, f := range list {
		if f.ID == id {
			return list, nil
		}
	}
	w := visible(sc, true)
	args := append(append([]any(nil), lead...), id)
	args = append(args, w.args...)
	got, err := many(ctx, r.st.Reader(), func(s scanner) (NamedFacet, error) {
		var f NamedFacet
		return f, s.Scan(&f.ID, &f.Name)
	}, prefix+w.sql()+` LIMIT 1`, args...)
	if err != nil {
		return nil, err
	}
	return append(list, got...), nil
}
