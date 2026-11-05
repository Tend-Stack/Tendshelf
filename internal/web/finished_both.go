package web

import (
	"context"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// setFinishedBoth is the one act behind "Mark as finished" and "Mark as not started", wherever the person
// presses it (the book page, the audiobook player, the reader): finished is one state per book, so both
// formats the person may use change together. A format they have no right to is never touched, and only
// that person's own rows are written. Nothing about a file changes.
//
// Clearing leaves a format with no row alone: a row made only to hold "not started" would make a book
// look begun in a series tile.
func (s *server) setFinishedBoth(ctx context.Context, u users.User, book library.Book, finished bool) error {
	mayListen := book.Kind != library.BookText && book.RefusedReason == "" && book.MissingSince == 0 &&
		s.can.Can(ctx, u, share.ListenBook, share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID})
	mayRead, err := s.mayReadBook(ctx, u, book)
	if err != nil {
		return err
	}
	if mayListen {
		_, err := s.progress.Get(ctx, u.ID, book.ID)
		switch {
		case err == nil || finished:
			if _, err := s.progress.SetFinished(ctx, u.ID, book.ID, finished, book.DurationMs); err != nil {
				return err
			}
		case !errors.Is(err, progress.ErrNotFound):
			return err
		}
	}
	if mayRead {
		_, ok, err := s.progress.Reading(ctx, u.ID, book.ID)
		if err != nil {
			return err
		}
		if ok || finished {
			if _, err := s.progress.SetReadingFinished(ctx, u.ID, book.ID, finished); err != nil {
				return err
			}
		}
	}
	return nil
}
