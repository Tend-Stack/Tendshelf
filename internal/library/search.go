package library

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Search runs over the books_fts index of migration 0004. User text is never FTS5 syntax: it is
// split into plain terms and each term is sent double-quoted. Every query passes Listable and the
// caller's Scope in SQL, so a title the person may not open never shows up in a result, a count or
// a suggestion.

// SearchMaxTerms is how many terms of a query are used; the rest are ignored.
const SearchMaxTerms = 8

const (
	searchMaxTermRunes = 64
	searchTotalCap     = 1000
	searchDefaultLimit = 50
	searchMaxLimit     = 200
	suggestDefault     = 8
	suggestMax         = 20
	vocabCandidates    = 5000
	// bm25 weights: title, subtitle, authors, narrators, series, description.
	bm25Order = `bm25(books_fts, 10, 4, 6, 4, 6, 1)`
)

// SearchTerms splits q into lowercase terms made of letters, digits and combining marks; every
// other character separates terms. Terms over 64 runes are cut, repeats dropped, and at most
// SearchMaxTerms are kept, in order.
func SearchTerms(q string) []string {
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r))
	})
	var terms []string
	seen := map[string]bool{}
	for _, f := range fields {
		if utf8.RuneCountInString(f) > searchMaxTermRunes {
			f = string([]rune(f)[:searchMaxTermRunes])
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		terms = append(terms, f)
		if len(terms) == SearchMaxTerms {
			break
		}
	}
	return terms
}

// quoteTerm double-quotes one term for MATCH, doubling any quote inside it.
func quoteTerm(t string) string { return `"` + strings.ReplaceAll(t, `"`, `""`) + `"` }

// matchExpr is `"t1"* "t2"*`: every term a prefix match, all required.
func matchExpr(terms []string) string {
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = quoteTerm(t) + "*"
	}
	return strings.Join(parts, " ")
}

// SearchResult is the ordered book IDs for one query. Total is capped at 1000. Suggestion is a
// corrected query that finds at least one book the person may see, or empty.
type SearchResult struct {
	IDs        []string
	Total      int
	Suggestion string
}

// scopeSQL returns the two scope conditions and their arguments. A zero Scope shows nothing.
func scopeSQL(sc Scope) (string, []any) {
	bs, ls := sc.Books.SQL, sc.Libraries.SQL
	if strings.TrimSpace(bs) == "" || strings.TrimSpace(ls) == "" {
		return `0=1`, nil
	}
	args := append(append([]any{}, sc.Books.Args...), sc.Libraries.Args...)
	return `(` + bs + `) AND (` + ls + `)`, args
}

// searchFrom is the shared FROM and WHERE of a book search; its arguments are the MATCH text, then
// the scope arguments.
func searchFrom(sc Scope, match string) (string, []any) {
	cond, sargs := scopeSQL(sc)
	q := ` FROM books_fts JOIN books_fts_ids i ON i.rid = books_fts.rowid
 JOIN books b ON b.id = i.book_id JOIN libraries l ON l.id = b.library_id
 WHERE books_fts MATCH ? AND ` + Listable("b") + ` AND ` + cond
	return q, append([]any{match}, sargs...)
}

func clampLimit(limit, def, top int) int {
	if limit == 0 {
		limit = def
	}
	return min(top, max(limit, 1))
}

// searchIDs returns the ids of the visible books that match terms, best first.
func (r *Repo) searchIDs(ctx context.Context, sc Scope, terms []string, limit int) ([]string, error) {
	from, args := searchFrom(sc, matchExpr(terms))
	rows, err := r.st.Reader().QueryContext(ctx,
		`SELECT b.id`+from+` ORDER BY `+bm25Order+`, b.id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Search finds the books the scope may see that match q, best first. limit 0 means 50; it is
// clamped to 1..200. A query with no usable terms returns an empty result.
func (r *Repo) Search(ctx context.Context, sc Scope, q string, limit int) (SearchResult, error) {
	terms := SearchTerms(q)
	if len(terms) == 0 {
		return SearchResult{}, nil
	}
	limit = clampLimit(limit, searchDefaultLimit, searchMaxLimit)
	ids, err := r.searchIDs(ctx, sc, terms, limit)
	if err != nil {
		return SearchResult{}, err
	}
	res := SearchResult{IDs: ids}
	from, args := searchFrom(sc, matchExpr(terms))
	if err := r.st.Reader().QueryRowContext(ctx,
		`SELECT count(*) FROM (SELECT 1`+from+` LIMIT ?)`, append(args, searchTotalCap)...).Scan(&res.Total); err != nil {
		return SearchResult{}, err
	}
	if res.Total == 0 {
		res.Suggestion, err = r.didYouMean(ctx, sc, terms)
		if err != nil {
			return SearchResult{}, err
		}
	}
	return res, nil
}

// didYouMean corrects the terms that no indexed word starts with. It offers the corrected query
// only if that finds a book the person may see: the vocabulary is global, so a word that only a
// hidden or missing book contains must never be suggested.
func (r *Repo) didYouMean(ctx context.Context, sc Scope, terms []string) (string, error) {
	corrected := append([]string{}, terms...)
	changed := false
	for i, t := range terms {
		rs := []rune(t)
		if len(rs) < 3 {
			continue
		}
		var one int
		err := r.st.Reader().QueryRowContext(ctx,
			`SELECT 1 FROM books_fts_vocab WHERE term >= ? AND term < ? LIMIT 1`, t, t+"\U0010FFFF").Scan(&one)
		if err == nil {
			continue // some word starts with t, so t is not the problem
		}
		if err != sql.ErrNoRows {
			return "", err
		}
		best, err := r.closestTerm(ctx, rs)
		if err != nil {
			return "", err
		}
		if best != "" {
			corrected[i] = best
			changed = true
		}
	}
	if !changed {
		return "", nil
	}
	ids, err := r.searchIDs(ctx, sc, corrected, 1)
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return strings.Join(corrected, " "), nil
}

// closestTerm is the indexed word nearest to t that starts with the same letter: smallest edit
// distance (at most 1 up to five runes, else 2), then the word in more books, then alphabetical.
func (r *Repo) closestTerm(ctx context.Context, t []rune) (string, error) {
	maxDist := 2
	if len(t) <= 5 {
		maxDist = 1
	}
	first := t[0]
	rows, err := r.st.Reader().QueryContext(ctx,
		`SELECT term, doc FROM books_fts_vocab WHERE term >= ? AND term < ? AND length(term) BETWEEN ? AND ? LIMIT ?`,
		string(first), string(first+1), len(t)-2, len(t)+2, vocabCandidates)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	best, bestDist, bestDoc := "", maxDist+1, -1
	for rows.Next() {
		var term string
		var doc int
		if err := rows.Scan(&term, &doc); err != nil {
			return "", err
		}
		d := damerauLevenshtein(t, []rune(term), maxDist)
		if d > maxDist {
			continue
		}
		if d < bestDist || (d == bestDist && (doc > bestDoc || (doc == bestDoc && term < best))) {
			best, bestDist, bestDoc = term, d, doc
		}
	}
	return best, rows.Err()
}

// Suggestion is one type-ahead line: a book, an author, a narrator or a series.
type Suggestion struct{ Kind, ID, Label string }

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Suggest answers type-ahead for q (at least two runes): books whose title starts the typed words,
// then authors, narrators and series with a word that starts the last typed word. Only names with
// a book the scope may see are offered. limit 0 means 8; it is clamped to 1..20.
func (r *Repo) Suggest(ctx context.Context, sc Scope, q string, limit int) ([]Suggestion, error) {
	if utf8.RuneCountInString(strings.TrimSpace(q)) < 2 {
		return nil, nil
	}
	terms := SearchTerms(q)
	if len(terms) == 0 {
		return nil, nil
	}
	limit = clampLimit(limit, suggestDefault, suggestMax)
	var out []Suggestion

	// Books: a title match, best first, up to half the line.
	from, args := searchFrom(sc, `{title} : (`+matchExpr(terms)+`)`)
	rows, err := r.st.Reader().QueryContext(ctx,
		`SELECT b.id, b.title`+from+` ORDER BY `+bm25Order+`, b.id LIMIT ?`, append(args, max(limit/2, 1))...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		s := Suggestion{Kind: "book"}
		if err := rows.Scan(&s.ID, &s.Label); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	last := terms[len(terms)-1]
	like := `% ` + escapeLike(last) + `%`
	cond, sargs := scopeSQL(sc)
	for _, k := range []struct{ kind, role string }{{"author", RoleAuthor}, {"narrator", RoleNarrator}} {
		if len(out) >= limit {
			break
		}
		got, err := r.suggestNames(ctx, k.kind, `people`, `book_people`, `person_id`, k.role, like, cond, sargs, limit-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	if len(out) < limit {
		got, err := r.suggestNames(ctx, "series", `series`, `book_series`, `series_id`, "", like, cond, sargs, limit-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// suggestNames lists names in table whose lowercase form has a word starting with the LIKE
// pattern and that have at least one visible, listable book through link (in role, when set).
// The table, link and column names are fixed strings from Suggest, never user text.
func (r *Repo) suggestNames(ctx context.Context, kind, table, link, col, role, like, cond string, sargs []any, limit int) ([]Suggestion, error) {
	roleSQL := ""
	args := []any{like}
	if role != "" {
		roleSQL = ` AND x.role = ?`
		args = append(args, role)
	}
	args = append(args, sargs...)
	args = append(args, limit)
	rows, err := r.st.Reader().QueryContext(ctx,
		`SELECT n.id, n.name FROM `+table+` n WHERE (' ' || lower(n.name)) LIKE ? ESCAPE '\'
 AND EXISTS (SELECT 1 FROM `+link+` x JOIN books b ON b.id = x.book_id JOIN libraries l ON l.id = b.library_id
   WHERE x.`+col+` = n.id`+roleSQL+` AND `+Listable("b")+` AND `+cond+`)
 ORDER BY n.name COLLATE NOCASE, n.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Suggestion
	for rows.Next() {
		s := Suggestion{Kind: kind}
		if err := rows.Scan(&s.ID, &s.Label); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RebuildSearch throws the index away and builds it again from the book rows, in one transaction.
// The triggers keep the index current, so this is for repair and for tests.
func (r *Repo) RebuildSearch(ctx context.Context) error {
	return r.Update(ctx, func(t *Tx) error {
		for _, s := range []string{
			`DELETE FROM books_fts`,
			`DELETE FROM books_fts_ids`,
			`INSERT INTO books_fts_ids(book_id) SELECT id FROM books ORDER BY added_at, id`,
			`INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
 SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
 FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id`,
		} {
			if _, err := t.exec(s); err != nil {
				return err
			}
		}
		return nil
	})
}
