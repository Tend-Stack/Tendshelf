package library

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
)

// Reads for the Home shelves that join a person's listening progress to the books they may see.
// They live here, not in internal/progress, because they join books and need the scope helpers.
// The progress table is read directly (user_id is always a bound filter), so this package does not
// import internal/progress.

const (
	maxListeningLimit = 24
	// becauseSeedMinMs is how far into an unfinished book a person must be for it to seed
	// "Because you listened to".
	becauseSeedMinMs = 600_000
)

func listeningLimit(n int) int { return min(max(n, 1), maxListeningLimit) }

// Listening is a book in progress with where the person stopped.
type Listening struct {
	Book                              BookCard
	PositionMs, DurationMs, UpdatedAt int64
	// Format is the format the person used last: BookAudio, or BookText with the reading place below
	// (ContinueBooks fills it; ContinueListening is audio only).
	Format                      string
	ChapterIdx, PercentBP, Char int
	EditionID                   string
}

// ContinueListening lists the visible books the person has started and not finished, most recently
// listened first. The first element is the hero book.
func (r *Repo) ContinueListening(ctx context.Context, sc Scope, userID string, limit int) ([]Listening, error) {
	w := visible(sc, true)
	w.add("p.user_id = ?", userID)
	w.add("p.finished_at IS NULL")
	w.add("p.position_ms > 0") // a book marked "not started" leaves the shelf
	// Finished is one state per book: finished in the other format hides it too, which also covers
	// older data where only one format carries the mark (read at query time, no migration).
	w.add("NOT EXISTS (SELECT 1 FROM reading_positions rf WHERE rf.user_id = p.user_id AND rf.book_id = b.id AND rf.finished_at IS NOT NULL)")
	type row struct {
		id                string
		pos, dur, updated int64
	}
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (row, error) {
		var x row
		var bookDur int64
		err := s.Scan(&x.id, &x.pos, &x.dur, &bookDur, &x.updated)
		if bookDur > 0 {
			x.dur = bookDur
		}
		return x, err
	}, `SELECT b.id, p.position_ms, p.duration_ms, b.duration_ms, p.client_updated_at`+fromVisible+`
  JOIN progress p ON p.book_id = b.id WHERE `+w.sql()+`
  ORDER BY p.client_updated_at DESC, p.book_id LIMIT ?`, append(append([]any(nil), w.args...), listeningLimit(limit))...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, x := range rows {
		ids[i] = x.id
	}
	cards, err := r.cards(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]BookCard, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	out := make([]Listening, 0, len(rows))
	for _, x := range rows {
		if c, ok := byID[x.id]; ok {
			out = append(out, Listening{Book: c, PositionMs: x.pos, DurationMs: x.dur, UpdatedAt: x.updated})
		}
	}
	return out, nil
}

// ContinueBooks lists the visible books the person is under way with in either format, one row per book
// with the format they used last, newest first. Audio is under way when started and not finished; text
// when its percent is above 0 and it is not finished.
func (r *Repo) ContinueBooks(ctx context.Context, sc Scope, userID string, limit int) ([]Listening, error) {
	audio, err := r.ContinueListening(ctx, sc, userID, limit)
	if err != nil {
		return nil, err
	}
	for i := range audio {
		audio[i].Format = BookAudio
	}
	w := visible(sc, true)
	w.add("r.user_id = ?", userID)
	w.add("r.finished_at IS NULL")
	w.add("r.percent_bp > 0")
	w.add("NOT EXISTS (SELECT 1 FROM progress pf WHERE pf.user_id = r.user_id AND pf.book_id = b.id AND pf.finished_at IS NOT NULL)")
	type row struct {
		id, edition       string
		chapter, bp, char int
		updated           int64
	}
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (row, error) {
		var x row
		err := s.Scan(&x.id, &x.edition, &x.chapter, &x.bp, &x.char, &x.updated)
		return x, err
	}, `SELECT b.id, COALESCE(r.edition_id,''), r.chapter_idx, r.percent_bp, r.char_pos, r.client_updated_at`+fromVisible+`
  JOIN reading_positions r ON r.book_id = b.id WHERE `+w.sql()+`
  ORDER BY r.client_updated_at DESC, r.book_id LIMIT ?`, append(append([]any(nil), w.args...), listeningLimit(limit))...)
	if err != nil || len(rows) == 0 {
		return audio, err
	}
	ids := make([]string, len(rows))
	for i, x := range rows {
		ids[i] = x.id
	}
	cards, err := r.cards(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]BookCard, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	last := make(map[string]int, len(audio))
	for i, a := range audio {
		last[a.Book.ID] = i
	}
	for _, x := range rows {
		c, ok := byID[x.id]
		if !ok {
			continue
		}
		t := Listening{Book: c, UpdatedAt: x.updated, Format: BookText, EditionID: x.edition, ChapterIdx: x.chapter, PercentBP: x.bp, Char: x.char}
		if i, both := last[x.id]; both {
			if audio[i].UpdatedAt >= x.updated {
				continue // the audio place is newer: one row per book
			}
			audio[i] = t
			continue
		}
		audio = append(audio, t)
	}
	sort.SliceStable(audio, func(i, j int) bool { return audio[i].UpdatedAt > audio[j].UpdatedAt })
	if n := listeningLimit(limit); len(audio) > n {
		audio = audio[:n]
	}
	return audio, nil
}

// SeriesNext is the book to listen to or read next in a series the person has started, in either format.
type SeriesNext struct {
	Series SeriesRef
	Next   BookCard
	// Started is true when Next already has unfinished progress (resume), false when it is a fresh start.
	Started    bool
	PositionMs int64
	// ReadBP is the reading place of Next in hundredths of a percent; ReadLast is true when that reading
	// is newer than the listening, or there is no listening (the row then offers Read, not Play).
	ReadBP   int
	ReadLast bool
	// NextIndex is the 1-based place of Next in the visible reading order of the series.
	NextIndex int
	// Done and Total count the finished and the visible books of the series.
	Done, Total int
	// UpdatedAt is the newest progress time in the series.
	UpdatedAt int64
}

// SeriesInProgress lists, for each series with a visible book the person has touched (listening or
// reading), the book to continue: the book they stopped in if unfinished, else the next one in order.
// A book read to the end counts as done, whichever format it was. A series
// that is complete is left out. Newest activity first. The caller drops a series whose Next is the
// hero book (one resume button per book).
func (r *Repo) SeriesInProgress(ctx context.Context, sc Scope, userID string, limit int) ([]SeriesNext, error) {
	w := visible(sc, true)
	type entry struct {
		id, seq       string
		has, finished bool
		pos, updated  int64
		readBP        int
		readAt        int64 // client time of the reading place, 0 when none
		listenAt      int64 // client time of the listening place, 0 when none
	}
	type series struct {
		ref     SeriesRef
		books   []entry
		updated int64
	}
	args := append([]any{userID, userID, userID, userID}, w.args...)
	var order []string
	bySeries := map[string]*series{}
	_, err := many(ctx, r.st.Reader(), func(s scanner) (struct{}, error) {
		var sid, name string
		var e entry
		var has, fin, rHas, rFin int
		if err := s.Scan(&sid, &name, &e.id, &e.seq, &has, &fin, &e.pos, &e.listenAt, &rHas, &rFin, &e.readBP, &e.readAt); err != nil {
			return struct{}{}, err
		}
		e.has, e.finished = has == 1 || rHas == 1, fin == 1 || rFin == 1
		e.updated = max(e.listenAt, e.readAt)
		sr := bySeries[sid]
		if sr == nil {
			sr = &series{ref: SeriesRef{ID: sid, Name: name}}
			bySeries[sid] = sr
			order = append(order, sid)
		}
		if len(sr.books) < maxSeriesList {
			sr.books = append(sr.books, e)
		}
		sr.updated = max(sr.updated, e.updated)
		return struct{}{}, nil
	}, `SELECT bs.series_id, sr.name, b.id, bs.sequence, p.user_id IS NOT NULL, p.finished_at IS NOT NULL,
  COALESCE(p.position_ms, 0), COALESCE(p.client_updated_at, 0),
  rp.user_id IS NOT NULL, rp.finished_at IS NOT NULL, COALESCE(rp.percent_bp, 0), COALESCE(rp.client_updated_at, 0)`+fromVisible+`
  JOIN book_series bs ON bs.book_id = b.id JOIN series sr ON sr.id = bs.series_id
  LEFT JOIN progress p ON p.book_id = b.id AND p.user_id = ?
  LEFT JOIN reading_positions rp ON rp.book_id = b.id AND rp.user_id = ?
  WHERE (EXISTS (SELECT 1 FROM book_series bs2 JOIN progress p2 ON p2.book_id = bs2.book_id
                 WHERE bs2.series_id = bs.series_id AND p2.user_id = ?)
         OR EXISTS (SELECT 1 FROM book_series bs3 JOIN reading_positions r3 ON r3.book_id = bs3.book_id
                    WHERE bs3.series_id = bs.series_id AND r3.user_id = ?)) AND `+w.sql()+`
  ORDER BY bs.series_id, CASE WHEN bs.sequence GLOB '[0-9]*' THEN CAST(bs.sequence AS REAL) END ASC NULLS LAST,
  `+titleKey+` COLLATE NOCASE, b.id`, args...)
	if err != nil {
		return nil, err
	}

	type pick struct {
		sr      *series
		next    entry
		index   int
		started bool
	}
	var picks []pick
	for _, sid := range order {
		sr := bySeries[sid]
		k := -1
		for i, e := range sr.books {
			if e.has {
				k = i
			}
		}
		if k < 0 {
			continue // only hidden books carry progress
		}
		next := -1
		if !sr.books[k].finished {
			next = k
		} else {
			for i := k + 1; i < len(sr.books); i++ {
				if !(sr.books[i].has && sr.books[i].finished) {
					next = i
					break
				}
			}
		}
		if next < 0 {
			continue
		}
		n := sr.books[next]
		sr.ref.Sequence = n.seq
		picks = append(picks, pick{sr: sr, next: n, index: next + 1, started: n.has && !n.finished})
	}
	sort.SliceStable(picks, func(i, j int) bool {
		if picks[i].sr.updated != picks[j].sr.updated {
			return picks[i].sr.updated > picks[j].sr.updated
		}
		return picks[i].sr.ref.ID < picks[j].sr.ref.ID
	})
	if lim := listeningLimit(limit); len(picks) > lim {
		picks = picks[:lim]
	}
	if len(picks) == 0 {
		return nil, nil
	}
	ids := make([]string, len(picks))
	for i, p := range picks {
		ids[i] = p.next.id
	}
	cards, err := r.cards(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]BookCard, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	out := make([]SeriesNext, 0, len(picks))
	for _, p := range picks {
		c, ok := byID[p.next.id]
		if !ok {
			continue
		}
		done := 0
		for _, e := range p.sr.books {
			if e.has && e.finished {
				done++
			}
		}
		sn := SeriesNext{Series: p.sr.ref, Next: c, Started: p.started, NextIndex: p.index, Done: done, Total: len(p.sr.books), UpdatedAt: p.sr.updated}
		if p.started {
			sn.PositionMs = p.next.pos
			sn.ReadBP = p.next.readBP
			sn.ReadLast = p.next.readAt > 0 && p.next.readAt > p.next.listenAt
		}
		out = append(out, sn)
	}
	return out, nil
}

// Because is a seed book the person has listened to and visible books like it.
type Because struct {
	Seed  BookCard
	Books []BookCard
}

// BecauseYouListened picks the seed (the visible book the person finished most recently, else the
// unfinished one they listened to most recently and got at least 10 minutes into) and lists unstarted
// visible books that share its authors (3 points each), narrators (2) or genres (1), outside the
// seed's series. The bool is false when there is no seed or nothing to suggest.
func (r *Repo) BecauseYouListened(ctx context.Context, sc Scope, userID string, limit int) (Because, bool, error) {
	seedQuery := func(cond, order string, extra ...any) (string, error) {
		w := visible(sc, true)
		w.add("p.user_id = ?", userID)
		w.add(cond, extra...)
		ids, err := many(ctx, r.st.Reader(), func(s scanner) (string, error) {
			var id string
			return id, s.Scan(&id)
		}, `SELECT b.id`+fromVisible+` JOIN progress p ON p.book_id = b.id WHERE `+w.sql()+` ORDER BY `+order+` LIMIT 1`, w.args...)
		if err != nil || len(ids) == 0 {
			return "", err
		}
		return ids[0], nil
	}
	seed, err := seedQuery("p.finished_at IS NOT NULL", "p.finished_at DESC, b.id")
	if err == nil && seed == "" {
		seed, err = seedQuery("p.finished_at IS NULL AND p.position_ms >= ?", "p.client_updated_at DESC, b.id", becauseSeedMinMs)
	}
	if err != nil || seed == "" {
		return Because{}, false, err
	}

	w := visible(sc, true)
	args := []any{seed, seed, seed}
	args = append(args, w.args...)
	args = append(args, seed, userID, seed, listeningLimit(limit))
	ids, err := many(ctx, r.st.Reader(), func(s scanner) (string, error) {
		var id string
		var added, score int64
		return id, s.Scan(&id, &added, &score)
	}, `SELECT id, added_at, score FROM (SELECT b.id AS id, b.added_at AS added_at,
  3 * (SELECT count(*) FROM book_people x JOIN book_people y ON y.person_id = x.person_id AND y.role = x.role
       WHERE x.book_id = b.id AND x.role = 'author' AND y.book_id = ?)
  + 2 * (SELECT count(*) FROM book_people x JOIN book_people y ON y.person_id = x.person_id AND y.role = x.role
       WHERE x.book_id = b.id AND x.role = 'narrator' AND y.book_id = ?)
  + (SELECT count(*) FROM book_labels x JOIN book_labels y ON y.value = x.value AND y.kind = x.kind
       WHERE x.book_id = b.id AND x.kind = 'genre' AND y.book_id = ?) AS score`+fromVisible+`
  WHERE `+w.sql()+` AND b.id <> ?
  AND NOT EXISTS (SELECT 1 FROM progress p WHERE p.book_id = b.id AND p.user_id = ?)
  AND NOT EXISTS (SELECT 1 FROM book_series x JOIN book_series y ON y.series_id = x.series_id
                  WHERE x.book_id = b.id AND y.book_id = ?))
  WHERE score > 0 ORDER BY score DESC, added_at DESC, id LIMIT ?`, args...)
	if err != nil || len(ids) == 0 {
		return Because{}, false, err
	}
	all := append([]string{seed}, ids...)
	cards, err := r.cards(ctx, all)
	if err != nil || len(cards) == 0 || cards[0].ID != seed {
		return Because{}, false, err
	}
	if len(cards) == 1 {
		return Because{}, false, nil
	}
	return Because{Seed: cards[0], Books: cards[1:]}, true, nil
}

// ChapterAt returns the chapter that holds positionMs (the one with the greatest start at or before
// it; before the first chapter, the first), and how many chapters the book has. The bool is false when
// the book has no chapters. It is not scoped: callers authorize the book first.
func (r *Repo) ChapterAt(ctx context.Context, bookID string, positionMs int64) (Chapter, int, bool, error) {
	chs, err := r.Chapters(ctx, bookID)
	if err != nil || len(chs) == 0 {
		return Chapter{}, 0, false, err
	}
	best := chs[0]
	for _, c := range chs {
		if c.StartMs <= positionMs && (best.StartMs > positionMs || c.StartMs > best.StartMs || (c.StartMs == best.StartMs && c.Idx > best.Idx)) {
			best = c
		}
	}
	return best, len(chs), true, nil
}

// Sync feeds. Both walk one person's own rows changed after a time, in (time, id) order, and keep only
// rows of books the person may list (the scope SQL), so a hidden book never shows up in a sync.

const (
	syncDefaultLimit = 100
	syncMaxLimit     = 500
)

// SyncRef names one changed row and when it changed.
type SyncRef struct {
	ID        string
	UpdatedAt int64
}

type syncCursor struct {
	U int64  `json:"u"`
	I string `json:"i"`
}

func decodeSyncCursor(s string) (syncCursor, error) {
	var c syncCursor
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, ErrInvalid
	}
	if err := json.Unmarshal(raw, &c); err != nil || c.I == "" || c.U < 0 {
		return syncCursor{}, ErrInvalid
	}
	return c, nil
}

// syncChanges runs the shared keyset query. table/idCol/timeCol are fixed names from this file.
func (r *Repo) syncChanges(ctx context.Context, sc Scope, table, idCol, timeCol, userID string, since int64, cursor string, limit int) ([]SyncRef, string, error) {
	if limit <= 0 {
		limit = syncDefaultLimit
	}
	limit = min(limit, syncMaxLimit)
	w := visible(sc, true)
	w.add("x.user_id = ?", userID)
	w.add("x."+timeCol+" > ?", since)
	if cursor != "" {
		c, err := decodeSyncCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		w.add("(x."+timeCol+", x."+idCol+") > (?, ?)", c.U, c.I)
	}
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (SyncRef, error) {
		var x SyncRef
		err := s.Scan(&x.ID, &x.UpdatedAt)
		return x, err
	}, `SELECT x.`+idCol+`, x.`+timeCol+` FROM `+table+` x JOIN books b ON b.id = x.book_id
  JOIN libraries l ON l.id = b.library_id WHERE `+w.sql()+` ORDER BY x.`+timeCol+`, x.`+idCol+` LIMIT ?`,
		append(append([]any(nil), w.args...), limit+1)...)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		raw, _ := json.Marshal(syncCursor{U: last.UpdatedAt, I: last.ID})
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return rows, next, nil
}

// ProgressChanges lists the book ids whose progress for the person changed on the server after since,
// oldest change first, with a cursor for the next page ("" at the end). A bad cursor is ErrInvalid.
func (r *Repo) ProgressChanges(ctx context.Context, sc Scope, userID string, since int64, cursor string, limit int) ([]SyncRef, string, error) {
	return r.syncChanges(ctx, sc, "progress", "book_id", "server_updated_at", userID, since, cursor, limit)
}

// BookmarkChanges lists the ids of the person's bookmarks (deleted ones too) changed after since, on
// books the person may list, oldest change first. A bad cursor is ErrInvalid.
func (r *Repo) BookmarkChanges(ctx context.Context, sc Scope, userID string, since int64, cursor string, limit int) ([]SyncRef, string, error) {
	return r.syncChanges(ctx, sc, "bookmarks", "id", "updated_at", userID, since, cursor, limit)
}
