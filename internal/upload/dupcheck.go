package upload

import (
	"context"
	"database/sql"
	"sort"
	"strings"

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

// scope is the person's listing scope. An error from the lookup is returned, never turned into "no check": the
// upload then fails as retryable and nothing is placed unchecked. ok false (no such or a disabled person) lists nothing.
func (s *Service) scope(ctx context.Context, userID string) (library.Scope, bool, error) {
	return s.cfg.ScopeFor(ctx, userID)
}

// uploadRefPrefix marks a stored dup_book_id that names the person's own earlier upload (not scanned yet).
const uploadRefPrefix = "upl_"

// ownHit is the person's own earlier upload whose placed files match.
type ownHit struct {
	uploadID, label, tier string
	matched               int
}

// ownMatches looks at the placed files (upload_paths, state present) of the uploader's OWN earlier uploads in this
// library, which are not in audio_files until their scan job has run. It never reads anyone else's uploads.
func (s *Service) ownMatches(ctx context.Context, userID, libraryID, exclude string, pairs []library.SizeQuick) (*ownHit, error) {
	want := map[library.SizeQuick]bool{}
	var totalBytes int64
	for _, p := range pairs {
		if !want[p] {
			want[p] = true
			totalBytes += p.Size
		}
	}
	list := make([]library.SizeQuick, 0, len(want))
	for p := range want {
		list = append(list, p)
	}
	type acc struct {
		label string
		seen  map[library.SizeQuick]bool
		bytes int64
	}
	by := map[string]*acc{}
	var order []string
	const chunk = 200
	for lo := 0; lo < len(list); lo += chunk {
		part := list[lo:min(lo+chunk, len(list))]
		q := `SELECT p.upload_id, u.label, p.size, p.quick_hash FROM upload_paths p JOIN uploads u ON u.id = p.upload_id
  WHERE p.kind = 'file' AND p.state = 'present' AND u.created_by = ? AND u.library_id = ? AND u.id <> ? AND (`
		args := []any{userID, libraryID, exclude}
		for i, p := range part {
			if i > 0 {
				q += " OR "
			}
			q += "(p.size = ? AND p.quick_hash = ?)"
			args = append(args, p.Size, p.Quick)
		}
		rows, err := s.st.Reader().QueryContext(ctx, q+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, label string
			var p library.SizeQuick
			if err := rows.Scan(&id, &label, &p.Size, &p.Quick); err != nil {
				_ = rows.Close()
				return nil, err
			}
			a := by[id]
			if a == nil {
				a = &acc{label: label, seen: map[library.SizeQuick]bool{}}
				by[id] = a
				order = append(order, id)
			}
			if !a.seen[p] {
				a.seen[p] = true
				a.bytes += p.Size
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	var best *ownHit
	for _, id := range order {
		a := by[id]
		tier := library.FileTier(len(a.seen), len(want), a.bytes, totalBytes)
		if tier == "" {
			continue
		}
		if best == nil || (tier == library.MatchSameFiles && best.tier != library.MatchSameFiles) || (tier == best.tier && len(a.seen) > best.matched) {
			best = &ownHit{uploadID: id, label: a.label, tier: tier, matched: len(a.seen)}
		}
	}
	return best, nil
}

// ownBook returns the book an own upload published, when it is scanned and the scope may list it.
func (s *Service) ownBook(ctx context.Context, sc library.Scope, uploadID string) (id, title string, ok bool) {
	err := s.st.Reader().QueryRowContext(ctx, `SELECT b.id FROM upload_books ub JOIN books b ON b.root_path = ub.root_path
  WHERE ub.upload_id = ? ORDER BY b.id LIMIT 1`, uploadID).Scan(&id)
	if err != nil {
		return "", "", false
	}
	title, ok = s.repo.ListableTitle(ctx, sc, id)
	return id, title, ok
}

// found is a match and the text stored in uploads.dup_book_id to find it again: the book's id, or the person's own
// upload id while that book is not scanned yet.
type found struct {
	m   *Match
	ref string
}

// matchFiles asks the library, and the person's own placed-but-unscanned uploads, for a book that holds these
// files. nil is no match, or the check is off. exclude is the upload being checked.
func (s *Service) matchFiles(ctx context.Context, userID, libraryID, exclude string, pairs []library.SizeQuick) (*found, error) {
	if s.cfg.ScopeFor == nil || len(pairs) == 0 {
		return nil, nil
	}
	sc, ok, err := s.scope(ctx, userID)
	if err != nil || !ok {
		return nil, err
	}
	ms, err := s.repo.FileMatches(ctx, sc, libraryID, pairs)
	if err != nil {
		return nil, err
	}
	own, err := s.ownMatches(ctx, userID, libraryID, exclude, pairs)
	if err != nil {
		return nil, err
	}
	if len(ms) > 0 && (own == nil || ms[0].Tier == library.MatchSameFiles || own.tier != library.MatchSameFiles) {
		return &found{s.matchOf(ctx, userID, libraryID, ms[0]), ms[0].BookID}, nil
	}
	if own == nil {
		return nil, nil
	}
	return s.ownFound(ctx, sc, userID, libraryID, own.uploadID, own.label, own.tier), nil
}

// ownFound is the match for one of the person's own uploads. Scanned and listable, it names the book. Otherwise
// book_id and href are empty strings and the title is the upload's own label (the person's own words).
func (s *Service) ownFound(ctx context.Context, sc library.Scope, userID, libraryID, uploadID, label, tier string) *found {
	if id, title, ok := s.ownBook(ctx, sc, uploadID); ok {
		return &found{s.matchOf(ctx, userID, libraryID, library.DupMatch{BookID: id, Title: title, Tier: tier}), id}
	}
	return &found{&Match{Title: label, Tier: tier, MayAddAnyway: s.mayAddAnyway(ctx, userID, libraryID, tier)}, uploadID}
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
	f, err := s.matchFiles(ctx, userID, libraryID, "", pairs)
	if f == nil || err != nil {
		return nil, err
	}
	return f.m, nil
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
	f, err := s.matchFiles(ctx, up.CreatedBy, up.LibraryID, up.ID, pairs)
	if err != nil {
		return s.plainFail(ctx, g, up.ID, err)
	}
	if f == nil && n.durationMs > 0 {
		sc, ok, err := s.scope(ctx, up.CreatedBy)
		if err != nil {
			return s.plainFail(ctx, g, up.ID, err)
		}
		if ok {
			ms, err := s.repo.DetailMatches(ctx, sc, up.LibraryID, n.tagTitle, n.tagAuthor, n.tagNarrator, n.durationMs)
			if err != nil {
				return s.plainFail(ctx, g, up.ID, err)
			}
			if len(ms) > 0 {
				f = &found{s.matchOf(ctx, up.CreatedBy, up.LibraryID, ms[0]), ms[0].BookID}
			}
		}
	}
	if f == nil || (up.AddAnyway && f.m.MayAddAnyway) {
		return s.clearHold(ctx, g, up.ID)
	}
	err = s.write(ctx, g, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE uploads SET dup_book_id = ?, dup_tier = ?, updated_at = ? WHERE id = ?`,
			f.ref, f.m.Tier, s.nowMs(), up.ID)
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
	sc, ok, err := s.scope(ctx, u.CreatedBy)
	if err != nil {
		s.log.Warn("upload: cannot read the person's scope", "upload", u.ID, "err", err)
		return
	}
	if !ok {
		return
	}
	if strings.HasPrefix(u.DupBookID, uploadRefPrefix) {
		// The match is the person's own earlier upload, not scanned when this one was checked.
		var label string
		if err := s.st.Reader().QueryRowContext(ctx, `SELECT label FROM uploads WHERE id = ? AND created_by = ?`,
			u.DupBookID, u.CreatedBy).Scan(&label); err != nil {
			return
		}
		v.Match = s.ownFound(ctx, sc, u.CreatedBy, u.LibraryID, u.DupBookID, label, u.DupTier).m
		return
	}
	title, ok := s.repo.ListableTitle(ctx, sc, u.DupBookID)
	if !ok {
		return
	}
	v.Match = s.matchOf(ctx, u.CreatedBy, u.LibraryID, library.DupMatch{BookID: u.DupBookID, Title: title, Tier: u.DupTier})
}
