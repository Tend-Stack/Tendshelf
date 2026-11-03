package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// A play control is a link that works without JavaScript: it opens the file at the person's place
// (/stream/{file}#t=). With JavaScript the player's click handler (player.js, P1c-4) takes over and
// starts sound inside the click. One book gets one such control per screen, so the resume button is
// never doubled.

// playLink is what a play control needs: the file that holds the place and where in the book it is.
type playLink struct {
	BookID, FileID, Mime string
	// FileStartMs is where the file starts on the book's timeline, StartMs the place to play from
	// on that timeline.
	FileStartMs, StartMs int64
	MayNeedCompat        bool
	FromStart            bool
	Label                string
}

// Href is the fallback address: the file itself, at the place inside it.
func (l playLink) Href() string {
	return "/stream/" + l.FileID + "#t=" + strconv.FormatFloat(float64(l.StartMs-l.FileStartMs)/1000, 'f', 3, 64)
}

// playState is the button rule: no progress or a book at 0 is "Start listening" from the start; a
// book with a place is "Resume" from it; a finished book is "Listen again" from the start.
func playState(p *progress.Progress) (label string, startMs int64, fromStart bool) {
	switch {
	case p == nil:
		return playStart, 0, false
	case p.Finished():
		return playAgain, 0, true
	case p.PositionMs > 0:
		return playResume, p.PositionMs, false
	}
	return playStart, 0, false
}

// playProgress reads the person's progress on a book; nil when there is none.
func (s *server) playProgress(ctx context.Context, userID, bookID string) (*progress.Progress, error) {
	p, err := s.progress.Get(ctx, userID, bookID)
	if errors.Is(err, progress.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// canPlay is invariant 2: the person may listen and the book is neither refused nor missing.
func (s *server) canPlay(ctx context.Context, u users.User, book library.Book) bool {
	return book.RefusedReason == "" && book.MissingSince == 0 &&
		s.can.Can(ctx, u, share.ListenBook, share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID})
}

// playLinkFor builds the play control of a book for a person, or nil when none may be drawn: no
// listen right, a refused or missing book, or no playable file. p is the person's progress (nil
// when there is none).
func (s *server) playLinkFor(ctx context.Context, u users.User, book library.Book, p *progress.Progress) (*playLink, error) {
	if !s.canPlay(ctx, u, book) {
		return nil, nil
	}
	files, err := s.lib.AudioFiles(ctx, book.ID)
	if err != nil {
		return nil, err
	}
	return playLinkIn(book, files, p), nil
}

// playLinkIn is playLinkFor once the files are known.
func playLinkIn(book library.Book, files []library.AudioFile, p *progress.Progress) *playLink {
	label, startMs, fromStart := playState(p)
	id, off, ok := playTarget(book, files, startMs)
	if !ok {
		return nil
	}
	for _, f := range files {
		if f.ID == id {
			return &playLink{
				BookID: book.ID, FileID: id, Mime: playerMime(f.Container, f.Codec),
				FileStartMs: f.OffsetMs, StartMs: f.OffsetMs + off,
				MayNeedCompat: media.MayNeedCompat(f.Container, f.Codec), FromStart: fromStart, Label: label,
			}
		}
	}
	return nil
}

// bookPlayData reads what the book page's play region and chapter links need. ok is false when the
// person may not play the book (no listen right, refused or missing): no control is drawn then.
func (s *server) bookPlayData(r *http.Request, book library.Book) (files []library.AudioFile, p *progress.Progress, ok bool, err error) {
	ctx, u := r.Context(), currentUser(r)
	if !s.canPlay(ctx, u, book) {
		return nil, nil, false, nil
	}
	if files, err = s.lib.AudioFiles(ctx, book.ID); err != nil {
		return nil, nil, false, err
	}
	if p, err = s.playProgress(ctx, u.ID, book.ID); err != nil {
		return nil, nil, false, err
	}
	return files, p, true, nil
}

// bookPlayView is the #book-play region for a book: the play button (when a file can play), the
// progress ruler and where-lines of a book that is under way, the shelf link and the mark control.
// chs are the book's chapters (for the ruler's ticks and the chapter words).
func (s *server) bookPlayView(r *http.Request, book library.Book, files []library.AudioFile, p *progress.Progress, chs []library.Chapter) (*pages.BookPlayView, error) {
	v := &pages.BookPlayView{
		Action: "/hx/books/" + book.ID + "/finished", Refresh: "/hx/books/" + book.ID + "/play",
		Next: "/books/" + book.ID, CSRF: s.csrf.TokenFrom(r.Context()),
		MarkLabel: playMarkFinished, MarkValue: "1",
	}
	if p != nil && p.Finished() {
		v.MarkLabel, v.MarkValue = playMarkNot, "0"
	}
	if l := playLinkIn(book, files, p); l != nil {
		v.Button = l.button("", false)
		// Only a signed-in person reaches this page (a share link has its own page), so the queue
		// menu always comes with the button.
		v.Queue = &components.QueueMenuView{
			BookID: book.ID, CSRF: v.CSRF, Next: v.Next, Toggle: queueToggle,
			PlayNext: queuePlayNext, AddEnd: queueAddEnd, Preserve: true,
		}
	}
	if p != nil && !p.Finished() && p.PositionMs > 0 && book.DurationMs > 0 {
		set, err := s.users.LoadSettings(r.Context(), currentUser(r).ID)
		if err != nil {
			return nil, err
		}
		lr := listenRulerFor(chs, p.PositionMs, book.DurationMs, set.Speed)
		v.Ruler = &components.RulerView{Label: lr.Label, Now: lr.Now, Ticks: lr.Ticks}
		v.Where, v.WhereShort = lr.Where, lr.WhereShort
	}
	if s.canShelve(r) {
		v.ShelfHref, v.ShelfLabel = shelfSheetHref(book.ID), shelfAddLabel
	}
	if s.canManageServerUsers(r) {
		v.ShareHref = "/books/" + book.ID + "/share"
	}
	v.Download = s.bookDownloadView(r, book, v.Button != nil)
	return v, nil
}

// getBookPlay answers GET /hx/books/{id}/play: only the #book-play region, so it follows a mark or
// a save without a page load. A book the person may not see is 404; one they may not play is an
// empty region.
func (s *server) getBookPlay(w http.ResponseWriter, r *http.Request) {
	b, ok := s.loadBook(w, r, r.PathValue("id"), share.ViewBook)
	if !ok {
		return
	}
	files, p, can, err := s.bookPlayData(r, b)
	if err != nil {
		s.serverError(w, r, "book play", err)
		return
	}
	var v *pages.BookPlayView
	if can {
		chs, err := s.lib.Chapters(r.Context(), b.ID)
		if err == nil {
			v, err = s.bookPlayView(r, b, files, p, chs)
		}
		if err != nil {
			s.serverError(w, r, "book play", err)
			return
		}
	}
	s.fragment(w, r, http.StatusOK, pages.BookPlay(v))
}

// button turns the link into the component's view. aria is the accessible name ("" keeps the label).
func (l playLink) button(aria string, round bool) *components.PlayButtonView {
	return &components.PlayButtonView{
		BookID: l.BookID, FileID: l.FileID, Mime: l.Mime, Href: l.Href(), Label: l.Label, AriaLabel: aria,
		FileStartMs: l.FileStartMs, StartMs: l.StartMs, MayNeedCompat: l.MayNeedCompat, FromStart: l.FromStart, Round: round,
	}
}
