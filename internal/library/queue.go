package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Up next: a person's ordered list of books and podcast episodes to play after the current one
// (decision 0038, plan P5: one queue for both kinds).
//
// Rules every function here keeps:
//   - The queue is per account: rows are (user_id, item_id), one per book or episode, and a call
//     only ever reads or changes the given person's rows. item_id is the book or the episode id.
//   - Reads join books and libraries through the caller's Scope and Listable, and an episode shows
//     only while the person follows its show or owns a state row for it, so an item the person
//     cannot open, or that is refused or missing, never shows, never counts and is never popped.
//     Its row stays (it comes back if access returns) until the item or person is deleted.
//   - A caller that does not ask for episodes (QueueKinds.Episodes false) never sees them, never
//     counts them and never pops or steps over them: the app before episodes saw books only.
//   - Order is position, then item id. Play next takes min(position)-1 and Add to queue takes
//     max(position)+1, so positions are signed and nothing is ever renumbered.
//   - A person holds at most QueueMax rows, hidden ones included.
//
// Whether the person may play an item is the caller's check (share.ListenBook, share.ListenEpisode)
// before QueueAdd or QueueAddEpisode.

// QueueMax is the most items one person can hold in Up next.
const QueueMax = 100

// ErrQueueFull is returned when adding a new item to a queue that already holds QueueMax rows.
var ErrQueueFull = errors.New("library: up next is full")

// The two kinds of item in the queue.
const (
	QueueKindBook    = "book"
	QueueKindEpisode = "episode"
)

// QueueKinds says which kinds of item a call reads or steps over.
type QueueKinds struct{ Books, Episodes bool }

// QueueBooks is what every caller that predates episodes uses; QueueAll adds episodes.
var (
	QueueBooks = QueueKinds{Books: true}
	QueueAll   = QueueKinds{Books: true, Episodes: true}
)

// QueueEpisode is the part of an episode a queue row shows.
type QueueEpisode struct {
	ID, ShowID, ShowTitle, Title, ArtSHA string
	DurationMs, PublishedAt              int64
}

// QueueEntry is one visible queued item, in play order. Kind is QueueKindBook (Book is set) or
// QueueKindEpisode (Episode is set). ID is the item id either way.
type QueueEntry struct {
	Kind    string
	ID      string
	Book    BookCard
	Episode QueueEpisode
	AddedAt int64
}

// QueueItem names one queue item: what QueuePopIn hands back.
type QueueItem struct{ Kind, ID string }

// queueVisible is the WHERE fragment (alias q for queue_items) that keeps the rows the person may see
// among the asked kinds, with its arguments.
func queueVisible(sc Scope, k QueueKinds) (string, []any) {
	var parts []string
	var args []any
	if k.Books {
		w := visible(sc, true)
		parts = append(parts, `(q.book_id IS NOT NULL AND EXISTS (SELECT 1 FROM books b JOIN libraries l ON l.id = b.library_id WHERE b.id = q.book_id AND `+w.sql()+`))`)
		args = append(args, w.args...)
	}
	if k.Episodes {
		parts = append(parts, `(q.episode_id IS NOT NULL AND EXISTS (SELECT 1 FROM podcast_episodes e WHERE e.id = q.episode_id AND
  (EXISTS (SELECT 1 FROM podcast_subscriptions s WHERE s.user_id = q.user_id AND s.show_id = e.show_id)
   OR EXISTS (SELECT 1 FROM podcast_opens po WHERE po.user_id = q.user_id AND po.show_id = e.show_id)
   OR EXISTS (SELECT 1 FROM podcast_episode_state t WHERE t.user_id = q.user_id AND t.episode_id = e.id))))`)
	}
	if len(parts) == 0 {
		return "0", nil
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// Queue lists the person's queued books the scope may open, in play order.
func (r *Repo) Queue(ctx context.Context, sc Scope, userID string) ([]QueueEntry, error) {
	return r.QueueIn(ctx, sc, userID, QueueBooks)
}

// QueueIn lists the person's queued items of the asked kinds that they may open, in play order.
func (r *Repo) QueueIn(ctx context.Context, sc Scope, userID string, k QueueKinds) ([]QueueEntry, error) {
	cond, cargs := queueVisible(sc, k)
	type row struct {
		id      string
		episode bool
		at      int64
	}
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (row, error) {
		var x row
		return x, s.Scan(&x.id, &x.episode, &x.at)
	}, `SELECT q.item_id, q.book_id IS NULL, q.added_at FROM queue_items q WHERE q.user_id = ? AND `+cond+` ORDER BY q.position, q.item_id`,
		append([]any{userID}, cargs...)...)
	if err != nil {
		return nil, err
	}
	var bookIDs, epIDs []string
	for _, x := range rows {
		if x.episode {
			epIDs = append(epIDs, x.id)
		} else {
			bookIDs = append(bookIDs, x.id)
		}
	}
	cards, err := r.cards(ctx, bookIDs)
	if err != nil {
		return nil, err
	}
	byBook := make(map[string]BookCard, len(cards))
	for _, c := range cards {
		byBook[c.ID] = c
	}
	eps, err := r.queueEpisodes(ctx, epIDs)
	if err != nil {
		return nil, err
	}
	out := make([]QueueEntry, 0, len(rows))
	for _, x := range rows {
		if x.episode {
			if e, ok := eps[x.id]; ok {
				out = append(out, QueueEntry{Kind: QueueKindEpisode, ID: x.id, Episode: e, AddedAt: x.at})
			}
		} else if c, ok := byBook[x.id]; ok {
			out = append(out, QueueEntry{Kind: QueueKindBook, ID: x.id, Book: c, AddedAt: x.at})
		}
	}
	return out, nil
}

// queueEpisodes reads what a queue row shows of each episode id.
func (r *Repo) queueEpisodes(ctx context.Context, ids []string) (map[string]QueueEpisode, error) {
	out := make(map[string]QueueEpisode, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	marks, args := inList(ids)
	list, err := many(ctx, r.st.Reader(), func(s scanner) (QueueEpisode, error) {
		var e QueueEpisode
		var dur sql.NullInt64
		err := s.Scan(&e.ID, &e.ShowID, &e.Title, &e.ShowTitle, &e.ArtSHA, &dur, &e.PublishedAt)
		e.DurationMs = dur.Int64
		return e, err
	}, `SELECT e.id, e.show_id, e.title, p.title, p.art_sha, e.duration_ms, e.published_at
  FROM podcast_episodes e JOIN podcast_shows p ON p.id = e.show_id WHERE e.id IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	for _, e := range list {
		out[e.ID] = e
	}
	return out, nil
}

// QueueCount counts the person's queued books the scope may open (the same filter as Queue).
func (r *Repo) QueueCount(ctx context.Context, sc Scope, userID string) (int, error) {
	return r.QueueCountIn(ctx, sc, userID, QueueBooks)
}

// QueueCountIn counts the person's queued items of the asked kinds that they may open.
func (r *Repo) QueueCountIn(ctx context.Context, sc Scope, userID string, k QueueKinds) (int, error) {
	cond, cargs := queueVisible(sc, k)
	var n int
	err := r.st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM queue_items q WHERE q.user_id = ? AND `+cond,
		append([]any{userID}, cargs...)...).Scan(&n)
	return n, err
}

// QueueAdd puts a book at the front (front) or the end of the person's queue. A book already
// queued stays where it is, unless front moves it to the top; either way added is false. A new book
// past QueueMax rows is ErrQueueFull; an unknown book is ErrNotFound. It does not check access.
func (r *Repo) QueueAdd(ctx context.Context, userID, bookID string, front bool) (added bool, err error) {
	return r.queueAdd(ctx, userID, QueueKindBook, bookID, front)
}

// QueueAddEpisode is QueueAdd for a podcast episode. It does not check access.
func (r *Repo) QueueAddEpisode(ctx context.Context, userID, episodeID string, front bool) (added bool, err error) {
	return r.queueAdd(ctx, userID, QueueKindEpisode, episodeID, front)
}

func (r *Repo) queueAdd(ctx context.Context, userID, kind, id string, front bool) (added bool, err error) {
	now := r.nowMs()
	err = r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		var lo, hi int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*), COALESCE(min(position), 0), COALESCE(max(position), 0)
  FROM queue_items WHERE user_id = ?`, userID).Scan(&n, &lo, &hi); err != nil {
			return err
		}
		var have int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM queue_items WHERE user_id = ? AND item_id = ?`, userID, id).Scan(&have)
		switch {
		case err == nil:
			if front {
				_, err = tx.ExecContext(ctx, `UPDATE queue_items SET position = ? WHERE user_id = ? AND item_id = ?`, lo-1, userID, id)
			}
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if n >= QueueMax {
			return ErrQueueFull
		}
		pos := hi + 1
		if front {
			pos = lo - 1
		}
		if n == 0 {
			pos = 0
		}
		col := "book_id"
		if kind == QueueKindEpisode {
			col = "episode_id"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO queue_items(user_id, item_id, `+col+`, position, added_at) VALUES (?,?,?,?,?)`,
			userID, id, id, pos, now); err != nil {
			return mapErr(err, "queue "+kind)
		}
		added = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return added, nil
}

// QueueMove moves a visible queued book one place up (delta -1) or down (+1) by swapping places
// with the nearest neighbour the scope may open, so a hidden book is stepped over. At either end it
// does nothing. Any other delta is ErrInvalid; a book not queued or not visible is ErrNotFound.
func (r *Repo) QueueMove(ctx context.Context, sc Scope, userID, bookID string, delta int) error {
	return r.QueueMoveIn(ctx, sc, userID, bookID, delta, QueueBooks)
}

// QueueMoveIn is QueueMove for an item of any asked kind (id is the book or episode id); the
// neighbour it swaps with is the nearest visible item of those kinds.
func (r *Repo) QueueMoveIn(ctx context.Context, sc Scope, userID, id string, delta int, k QueueKinds) error {
	if delta != -1 && delta != 1 {
		return invalid("an item moves one place at a time")
	}
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		cond, cargs := queueVisible(sc, k)
		from := ` FROM queue_items q WHERE q.user_id = ? AND ` + cond
		var pos int64
		err := tx.QueryRowContext(ctx, `SELECT q.position`+from+` AND q.item_id = ?`,
			append(append([]any{userID}, cargs...), id)...).Scan(&pos)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: queued item", ErrNotFound)
		}
		if err != nil {
			return err
		}
		next := ` AND (q.position < ? OR (q.position = ? AND q.item_id < ?)) ORDER BY q.position DESC, q.item_id DESC LIMIT 1`
		if delta > 0 {
			next = ` AND (q.position > ? OR (q.position = ? AND q.item_id > ?)) ORDER BY q.position, q.item_id LIMIT 1`
		}
		var nb string
		var npos int64
		err = tx.QueryRowContext(ctx, `SELECT q.item_id, q.position`+from+next,
			append(append([]any{userID}, cargs...), pos, pos, id)...).Scan(&nb, &npos)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // already at that end
		}
		if err != nil {
			return err
		}
		for _, u := range []struct {
			pos  int64
			item string
		}{{npos, id}, {pos, nb}} {
			if _, err := tx.ExecContext(ctx, `UPDATE queue_items SET position = ? WHERE user_id = ? AND item_id = ?`, u.pos, userID, u.item); err != nil {
				return err
			}
		}
		return nil
	})
}

// QueueRemove takes a book or an episode off the person's queue; one that is not queued is ErrNotFound.
func (r *Repo) QueueRemove(ctx context.Context, userID, id string) error {
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM queue_items WHERE user_id = ? AND item_id = ?`, userID, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: queued item", ErrNotFound)
		}
		return nil
	})
}

// QueuePop removes and returns the first queued book the scope may open. When ended is set, that
// book's row is removed first (it finished playing). Hidden, refused and missing rows are skipped
// and kept. With nothing to play it is ErrNotFound (an ended row is still removed).
func (r *Repo) QueuePop(ctx context.Context, sc Scope, userID, ended string) (string, error) {
	it, err := r.QueuePopIn(ctx, sc, userID, ended, QueueBooks)
	return it.ID, err
}

// QueuePopIn is QueuePop over the asked kinds: the first visible item, book or episode, in play order.
func (r *Repo) QueuePopIn(ctx context.Context, sc Scope, userID, ended string, k QueueKinds) (QueueItem, error) {
	var it QueueItem
	err := r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if ended != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM queue_items WHERE user_id = ? AND item_id = ?`, userID, ended); err != nil {
				return err
			}
		}
		cond, cargs := queueVisible(sc, k)
		var episode bool
		err := tx.QueryRowContext(ctx, `SELECT q.item_id, q.book_id IS NULL FROM queue_items q WHERE q.user_id = ? AND `+cond+
			` ORDER BY q.position, q.item_id LIMIT 1`, append([]any{userID}, cargs...)...).Scan(&it.ID, &episode)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		it.Kind = QueueKindBook
		if episode {
			it.Kind = QueueKindEpisode
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM queue_items WHERE user_id = ? AND item_id = ?`, userID, it.ID)
		return err
	})
	if err != nil {
		return QueueItem{}, err
	}
	if it.ID == "" {
		return QueueItem{}, fmt.Errorf("%w: queued item", ErrNotFound)
	}
	return it, nil
}
