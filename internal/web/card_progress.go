package web

import (
	"context"
	"fmt"
	"math"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// bookStand is where a person stands in a book, from the format they used last (decision 0018:
// listening and reading are two places that never write each other).
type bookStand struct {
	Finished bool // done in either format
	Started  bool // an unfinished place with something behind it
	Percent  int  // 0 to 100, only when Started; -1 when the share is unknown (audio without a length)
}

// standIn merges the listening place p and the reading place rp of one book. The format with the newer
// client time is the one that counts; a book finished in either format is finished.
func standIn(p *progress.Progress, rp *progress.ReadingPosition, durationMs int64) bookStand {
	if p != nil && p.Finished() || rp != nil && rp.Finished() {
		return bookStand{Finished: true}
	}
	audio := p != nil && p.PositionMs > 0
	text := rp != nil && rp.PercentBP > 0
	if audio && text && rp.ClientUpdatedAt > p.ClientUpdatedAt {
		audio = false
	}
	switch {
	case audio:
		d := p.DurationMs
		if d <= 0 {
			d = durationMs
		}
		if d <= 0 {
			return bookStand{Started: true, Percent: -1}
		}
		return bookStand{Started: true, Percent: min(max(int(math.Round(float64(p.PositionMs)*100/float64(d))), 0), 100)}
	case text:
		return bookStand{Started: true, Percent: min(rp.PercentBP/100, 100)}
	}
	return bookStand{}
}

// withCardProgress draws the thin line on the covers of the books the person has started and not
// finished, audio and ebook alike. views[i] is the card of cards[i]. It reads the person's own places
// for the whole page in two queries (no per-card query); another person's progress is never asked for.
func (s *server) withCardProgress(ctx context.Context, userID string, cards []library.BookCard, views []components.CardView) error {
	if len(cards) == 0 || len(cards) != len(views) {
		return nil
	}
	ids := make([]string, len(cards))
	for i, c := range cards {
		ids[i] = c.ID
	}
	prog, err := s.progress.ForBooks(ctx, userID, ids)
	if err != nil {
		return err
	}
	reads, err := s.progress.ReadingForBooks(ctx, userID, ids)
	if err != nil {
		return err
	}
	for i, c := range cards {
		var p *progress.Progress
		if x, ok := prog[c.ID]; ok {
			p = &x
		}
		var rp *progress.ReadingPosition
		if x, ok := reads[c.ID]; ok {
			rp = &x
		}
		st := standIn(p, rp, c.DurationMs)
		if !st.Started || st.Percent < 1 {
			continue
		}
		pct := min(st.Percent, 99)
		views[i].Progress = fmt.Sprint(pct)
		views[i].ProgressLabel = fmt.Sprintf(cardThrough, pct)
		views[i].AriaLabel += ", " + views[i].ProgressLabel
	}
	return nil
}
