package upload

import (
	"context"
	"database/sql"
	"sort"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// The "already in the library" check (decision 0045). It looks only in the library the book is going to, only
// at books the person may list (library.Scope, in SQL), with the Duplicates page's rules. It runs twice: before
// any byte is sent, when the browser sent each file's quick hash (Create), and after staging, with the server's own
// hashes and the tags of the staged audio (place). The second is the one that counts.

// holdText is the plain-words message of a held upload; the page words it per tier from Match.
const holdText = "This book may already be in the library."

// mayAddAnyway says whether the person may add a book that matches at tier: the owner and admins always, a
// contributor only on a look-alike (same_details), never on an exact copy or a shared file.
func (s *Service) mayAddAnyway(ctx context.Context, userID, libraryID, tier string) bool {
	place := PlaceTop
	if s.cfg.MayPlace != nil {
		place = s.cfg.MayPlace(ctx, userID, libraryID)
	}
	switch place {
	case PlaceTop:
		return true
	case PlaceOwnFolder:
		return tier == library.MatchSameDetails
	}
	return false
}

func (s *Service) matchOf(ctx context.Context, userID, libraryID string, m library.DupMatch) *Match {
	return &Match{BookID: m.BookID, Title: m.Title, Href: "/books/" + m.BookID, Tier: m.Tier,
		MayAddAnyway: s.mayAddAnyway(ctx, userID, libraryID, m.Tier)}
}

// bookFiles lists the staged or announced files that make the book: its audio, or without audio its ebooks.
// A PDF beside audio is a booklet and never counts.
func bookFiles[T any](items []T, path func(T) string) []T {
	var audio, ebooks []T
	for _, it := range items {
		switch p := path(it); {
		case media.Classify(p) == media.KindAudio:
			audio = append(audio, it)
		case isEbook(p):
			ebooks = append(ebooks, it)
		}
	}
	if len(audio) > 0 {
		return audio
	}
	return ebooks
}

// matchFiles asks the library for books that hold these files. nil is no match, or the check is off.
func (s *Service) matchFiles(ctx context.Context, userID, libraryID string, pairs []library.SizeQuick) (*Match, error) {
	if s.cfg.ScopeFor == nil || len(pairs) == 0 {
		return nil, nil
	}
	sc, ok := s.cfg.ScopeFor(ctx, userID)
	if !ok {
		return nil, nil
	}
	ms, err := s.repo.FileMatches(ctx, sc, libraryID, pairs)
	if err != nil || len(ms) == 0 {
		return nil, err
	}
	return s.matchOf(ctx, userID, libraryID, ms[0]), nil
}

// matchBeforeSend is the manifest check. Every book file must carry a quick hash (a .zip or a browser without
// crypto.subtle sends none), otherwise the staged check decides alone.
func (s *Service) matchBeforeSend(ctx context.Context, userID, libraryID string, files []FileIn) (*Match, error) {
	var pairs []library.SizeQuick
	for _, f := range bookFiles(files, func(f FileIn) string { return f.Path }) {
		if f.Quick == "" {
			return nil, nil
		}
		pairs = append(pairs, library.SizeQuick{Size: f.Size, Quick: f.Quick})
	}
	return s.matchFiles(ctx, userID, libraryID, pairs)
}

// holdIfInLibrary runs after inspect and before build. When the library already has the book it records the
// match, fails the upload with already_in_library and returns the job error; nothing has reached the library
// folder and the staged bytes stay until the person chooses (Finish with add anyway, or Cancel).
// The owner and admins who chose "add anyway" skip the check; a contributor who did still stops on an exact
// copy or a shared file.
func (s *Service) holdIfInLibrary(ctx context.Context, g guard, up uploadRow, files []fileRow, n bookNames, place Placement) error {
	if s.cfg.ScopeFor == nil {
		return nil
	}
	if up.AddAnyway && place == PlaceTop {
		if up.DupTier == "" {
			return nil
		}
		return s.clearHold(ctx, g, up.ID)
	}
	var pairs []library.SizeQuick
	for _, f := range bookFiles(files, func(f fileRow) string { return f.RelPath }) {
		h, size, err := media.QuickHash(s.partPath(f.UploadID, f.ID))
		if err != nil {
			return s.plainFail(ctx, g, up.ID, err)
		}
		pairs = append(pairs, library.SizeQuick{Size: size, Quick: h})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Quick < pairs[j].Quick })
	m, err := s.matchFiles(ctx, up.CreatedBy, up.LibraryID, pairs)
	if err != nil {
		return s.plainFail(ctx, g, up.ID, err)
	}
	if m == nil {
		sc, ok := s.cfg.ScopeFor(ctx, up.CreatedBy)
		if ok && n.durationMs > 0 {
			ms, err := s.repo.DetailMatches(ctx, sc, up.LibraryID, n.tagTitle, n.tagAuthor, n.tagNarrator, n.durationMs)
			if err != nil {
				return s.plainFail(ctx, g, up.ID, err)
			}
			if len(ms) > 0 {
				m = s.matchOf(ctx, up.CreatedBy, up.LibraryID, ms[0])
			}
		}
	}
	if m == nil || (up.AddAnyway && m.MayAddAnyway) {
		return s.clearHold(ctx, g, up.ID)
	}
	err = s.write(ctx, g, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE uploads SET dup_book_id = ?, dup_tier = ?, updated_at = ? WHERE id = ?`,
			m.BookID, m.Tier, s.nowMs(), up.ID)
		return err
	})
	if err != nil {
		return s.plainFail(ctx, g, up.ID, err)
	}
	return s.jobFail(ctx, g, up.ID, CodeAlreadyInLibrary, holdText, true, nil)
}

// clearHold forgets the match of an upload that is going ahead.
func (s *Service) clearHold(ctx context.Context, g guard, id string) error {
	return s.write(ctx, g, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE uploads SET dup_book_id = NULL, dup_tier = NULL WHERE id = ?`, id)
		return err
	})
}

// attachMatch fills View.Match for a held upload. The title is read again through the person's scope: a book
// they can no longer list is not named.
func (s *Service) attachMatch(ctx context.Context, u uploadRow, v *View) {
	if u.ErrorCode != CodeAlreadyInLibrary || u.DupBookID == "" || s.cfg.ScopeFor == nil {
		return
	}
	sc, ok := s.cfg.ScopeFor(ctx, u.CreatedBy)
	if !ok {
		return
	}
	title, ok := s.repo.ListableTitle(ctx, sc, u.DupBookID)
	if !ok {
		return
	}
	v.Match = s.matchOf(ctx, u.CreatedBy, u.LibraryID, library.DupMatch{BookID: u.DupBookID, Title: title, Tier: u.DupTier})
}
