package web

import (
	"context"
	"fmt"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// The rule for mixed formats (decision 0018): one resume button per book on any screen. The primary
// button belongs to the format the person used last; the other format, if they may use it, is a plain
// secondary button with no place and no time.

// Copy for the reading buttons. NEW (B2); the voice follows BRAND.md.
const (
	readStart      = "Start reading"
	readContinue   = "Continue reading"
	readAgain      = "Read again"
	readOther      = "Read"
	listenOther    = "Listen"
	readNeedsRight = "You can see this book, but reading it needs permission."
	readWhere      = "Chapter %d · %d %%"
)

const (
	formatAudio = "audio"
	formatText  = "text"
)

// lastFormat names the format of the primary button. It compares the client times of the person's
// listening place and reading place; with neither, audio when they may listen, else text. A format
// the person has no right to is never primary.
func lastFormat(p *progress.Progress, rp *progress.ReadingPosition, mayListen, mayRead bool) string {
	switch {
	case mayListen && !mayRead:
		return formatAudio
	case mayRead && !mayListen:
		return formatText
	case !mayListen && !mayRead:
		return ""
	case p != nil && rp != nil:
		if rp.ClientUpdatedAt > p.ClientUpdatedAt {
			return formatText
		}
		return formatAudio
	case rp != nil:
		return formatText
	}
	return formatAudio
}

// readState is the reading primary button's label, and the "Chapter n · pct %" line under it (empty
// when there is no place to describe).
func readState(rp *progress.ReadingPosition) (label, where string) {
	switch {
	case rp == nil:
		return readStart, ""
	case rp.Finished():
		return readAgain, ""
	case rp.PercentBP > 0:
		return readContinue, fmt.Sprintf(readWhere, rp.ChapterIdx, rp.PercentBP/100)
	}
	return readStart, ""
}

// readHref is the reader's address for a book.
func readHref(bookID string) string { return "/books/" + bookID + "/read" }

// mayReadBook is invariant 2 for the text routes: the right to read, and a ready edition to open.
func (s *server) mayReadBook(ctx context.Context, u users.User, book library.Book) (bool, error) {
	if s.ebooks == nil || book.RefusedReason != "" || book.MissingSince != 0 ||
		!s.can.Can(ctx, u, share.ReadText, share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID}) {
		return false, nil
	}
	return s.ebooks.Store().ReadyForBook(ctx, book.ID)
}

// readingOf reads the person's reading place on a book; nil when there is none.
func (s *server) readingOf(ctx context.Context, userID, bookID string) (*progress.ReadingPosition, error) {
	rp, ok, err := s.progress.Reading(ctx, userID, bookID)
	if err != nil || !ok {
		return nil, err
	}
	return &rp, nil
}

// readButton builds the button that opens the reader: primary with the reading labels, or the plain
// secondary "Read".
func readButton(book library.Book, rp *progress.ReadingPosition, secondary bool) *components.ReadButtonView {
	v := &components.ReadButtonView{Href: readHref(book.ID), Secondary: secondary, Label: readOther}
	if !secondary {
		v.Label, _ = readState(rp)
	}
	return v
}
