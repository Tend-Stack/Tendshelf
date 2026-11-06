package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Duplicate books (S4): find two copies of one audiobook and merge them. People who manage libraries only
// (owner and admins); everyone else gets a 404 on every route. Both books of a merge also need the right to edit
// them, and the lists are filtered in SQL by what the person can reach. Merging changes database rows only.

func (s *server) registerDuplicates(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/duplicates", s.getDuplicates)
	mux.HandleFunc("GET /admin/duplicates/merge", s.getDuplicateMerge)
	mux.HandleFunc("POST /admin/duplicates/merge", s.postDuplicateMerge)
	mux.HandleFunc("POST /admin/duplicates/not-same", s.postDuplicateNotSame)
	mux.HandleFunc("GET /admin/duplicates/merged", s.getDuplicatesMerged)
	mux.HandleFunc("POST /admin/duplicates/merged/restore", s.postDuplicateRestore)
}

func (s *server) dupRender(w http.ResponseWriter, r *http.Request, status int, title string, body pages.AdminDuplicatesView) {
	s.prefsPage(w, r, status, title, "admin-libraries", "libraries", false, pages.AdminDuplicates(body), nil, adminStyles...)
}

// dupBookMeta is the line under a copy: files, size, length.
func dupBookMeta(b library.DuplicateBook) string {
	meta := countText(b.Files, dupFilesOne, dupFilesMany) + " · " + sizeText(b.Bytes)
	if d := durationText(b.DurationMs); d != "" {
		meta += " · " + d
	}
	return meta
}

func (s *server) getDuplicates(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	groups, err := s.lib.Duplicates(r.Context(), s.scope(r))
	if err != nil {
		s.serverError(w, r, "duplicate books", err)
		return
	}
	v := pages.AdminDuplicatesView{CSRF: s.csrf.TokenFrom(r.Context()), Title: dupTitle, Lead: dupLead, Empty: dupEmpty,
		Suggested: dupSuggested, HiddenHref: "/admin/duplicates/merged", HiddenLabel: dupHiddenLink}
	switch r.URL.Query().Get("done") {
	case "merged":
		v.Banner = dupMergedBanner
	case "notsame":
		v.Banner = dupNotSameBanner
	}
	for _, g := range groups {
		pg := pages.DupGroup{Head: dupAudioHead, Note: dupAudioNote}
		if g.Tier == library.DupSameDetails {
			pg.Head, pg.Note = dupDetailsHead, dupDetailsNote
		}
		first := g.Books[0]
		for i, b := range g.Books {
			row := pages.DupRow{Title: b.Title, Href: "/books/" + b.ID, Path: b.RootPath, Meta: dupBookMeta(b), First: i == 0}
			var people []string
			if b.Listeners > 0 {
				people = append(people, countText(b.Listeners, dupListeningOne, dupListeningMany))
			}
			if b.Bookmarkers > 0 {
				people = append(people, countText(b.Bookmarkers, dupBookmarksOne, dupBookmarksMany))
			}
			for j, p := range people {
				if j > 0 {
					row.People += " · "
				}
				row.People += p
			}
			if i > 0 {
				row.MergeHref = "/admin/duplicates/merge?keep=" + url.QueryEscape(first.ID) + "&drop=" + url.QueryEscape(b.ID)
				row.MergeLabel = fmt.Sprintf(dupMergeInto, first.Title)
				row.MergeAria = fmt.Sprintf(dupMergeAria, b.Title, first.Title)
				row.NotSameAction, row.NotSameLabel = "/admin/duplicates/not-same", dupNotSame
				row.NotSameAria = fmt.Sprintf(dupNotSameAria, b.Title, first.Title)
				row.A, row.B = first.ID, b.ID
			}
			pg.Rows = append(pg.Rows, row)
		}
		v.Groups = append(v.Groups, pg)
	}
	s.dupRender(w, r, http.StatusOK, dupTitle, v)
}

// dupBooks loads the two books of a merge, each of which the person must be allowed to edit. Anything else has
// answered 404.
func (s *server) dupBooks(w http.ResponseWriter, r *http.Request, keepID, dropID string) (keep, drop library.Book, ok bool) {
	if keepID == "" || dropID == "" {
		s.notFound(w, r)
		return keep, drop, false
	}
	if keep, ok = s.loadBook(w, r, keepID, share.EditBook); !ok {
		return keep, drop, false
	}
	if drop, ok = s.loadBook(w, r, dropID, share.EditBook); !ok {
		return keep, drop, false
	}
	return keep, drop, true
}

// dupErrorText is the plain sentence for a refused merge; ok is false for an error that is not a refusal.
func dupErrorText(err error) (string, bool) {
	switch {
	case errors.Is(err, library.ErrMergeLibrary):
		return dupErrLibrary, true
	case errors.Is(err, library.ErrMergeBusy):
		return dupErrBusy, true
	case errors.Is(err, library.ErrMergeSame):
		return dupErrSame, true
	}
	return "", false
}

func (s *server) getDuplicateMerge(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	s.renderDupMerge(w, r, http.StatusOK, r.URL.Query().Get("keep"), r.URL.Query().Get("drop"), "")
}

// renderDupMerge draws the confirm page for keepID and dropID; msg, when set, is shown above it as a refusal.
func (s *server) renderDupMerge(w http.ResponseWriter, r *http.Request, status int, keepID, dropID, msg string) {
	if _, _, ok := s.dupBooks(w, r, keepID, dropID); !ok {
		return
	}
	p, err := s.lib.MergePreview(r.Context(), keepID, dropID)
	v := pages.AdminDupMergeView{CSRF: s.csrf.TokenFrom(r.Context()), Title: dupConfirmTitle, Lead: dupConfirmLead, Error: msg,
		MovesHead: dupMovesHead, KeepID: keepID, DropID: dropID, Cancel: dupCancel, CancelHref: "/admin/duplicates"}
	if text, refused := dupErrorText(err); refused {
		v.Error, status = text, http.StatusUnprocessableEntity
		s.dupRenderMerge(w, r, status, v)
		return
	}
	if errors.Is(err, library.ErrBookNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "merge preview", err)
		return
	}
	copyOf := func(head string, b library.DuplicateBook) pages.DupCopy {
		return pages.DupCopy{Head: head, Title: b.Title, Href: "/books/" + b.ID, Path: b.RootPath, Meta: dupBookMeta(b)}
	}
	v.Keep, v.Drop = copyOf(dupKeepHead, p.Keep), copyOf(dupDropHead, p.Drop)
	for _, m := range []struct {
		n    int
		text string
	}{{p.Listeners, dupMovePlaces}, {p.Bookmarks, dupMoveBookmarks}, {p.Highlights, dupMoveHighlights}, {p.Shelves, dupMoveShelves},
		{p.Ebooks, dupMoveEbooks}, {p.Grants, dupMoveGrants}, {p.Links, dupMoveLinks}} {
		if m.n > 0 {
			v.Moves = append(v.Moves, fmt.Sprintf(m.text, m.n))
		}
	}
	if len(v.Moves) == 0 {
		v.Moves = []string{dupMoveNothing}
	}
	v.Notes = append(v.Notes, dupFilesStay)
	if !p.SameAudio {
		v.Notes = append(v.Notes, dupNotSameAudio)
	}
	v.Notes = append(v.Notes, dupOneWay)
	if msg == "" {
		v.Merge, v.Swap = dupMergeButton, dupSwap
		v.SwapHref = "/admin/duplicates/merge?keep=" + url.QueryEscape(dropID) + "&drop=" + url.QueryEscape(keepID)
	}
	s.dupRenderMerge(w, r, status, v)
}

func (s *server) dupRenderMerge(w http.ResponseWriter, r *http.Request, status int, v pages.AdminDupMergeView) {
	s.prefsPage(w, r, status, dupConfirmTitle, "admin-libraries", "libraries", false, pages.AdminDupMerge(v), nil, adminStyles...)
}

func (s *server) postDuplicateMerge(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) || !s.parseForm(w, r) {
		return
	}
	keepID, dropID := r.PostForm.Get("keep"), r.PostForm.Get("drop")
	keep, drop, ok := s.dupBooks(w, r, keepID, dropID)
	if !ok {
		return
	}
	err := s.lib.MergeBooks(r.Context(), keep.ID, drop.ID, currentUser(r).ID)
	if text, refused := dupErrorText(err); refused {
		if r.Header.Get("HX-Request") == "true" {
			s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(text))
			return
		}
		s.renderDupMerge(w, r, http.StatusUnprocessableEntity, keepID, dropID, text)
		return
	}
	if errors.Is(err, library.ErrBookNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "merge books", err)
		return
	}
	s.answerPage(w, r, "/admin/duplicates?done=merged")
}

func (s *server) postDuplicateNotSame(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) || !s.parseForm(w, r) {
		return
	}
	a, b, ok := s.dupBooks(w, r, r.PostForm.Get("a"), r.PostForm.Get("b"))
	if !ok {
		return
	}
	err := s.lib.NotDuplicates(r.Context(), a.ID, b.ID, currentUser(r).ID)
	if errors.Is(err, library.ErrMergeSame) || errors.Is(err, library.ErrBookNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "not the same book", err)
		return
	}
	s.answerPage(w, r, "/admin/duplicates?done=notsame")
}

func (s *server) getDuplicatesMerged(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	rows, err := s.lib.MergedFolderList(r.Context(), s.scope(r))
	if err != nil {
		s.serverError(w, r, "merged copies", err)
		return
	}
	v := pages.AdminDupMergedView{CSRF: s.csrf.TokenFrom(r.Context()), Title: dupMergedTitle, Lead: dupMergedLead,
		Empty: dupMergedEmpty, Back: dupBack, BackHref: "/admin/duplicates"}
	if r.URL.Query().Get("done") == "restored" {
		v.Banner = dupRestoredBanner
	}
	for _, m := range rows {
		v.Rows = append(v.Rows, pages.DupMergedRow{Path: m.RootPath, Kept: fmt.Sprintf(dupMergedKept, m.KeptTitle) + " · " + agoText(s.now(), time.UnixMilli(m.MergedAt)),
			KeptHref: "/books/" + m.KeptID, Action: dupShowAgain, Aria: fmt.Sprintf(dupShowAgainAria, m.RootPath)})
	}
	s.prefsPage(w, r, http.StatusOK, dupMergedTitle, "admin-libraries", "libraries", false, pages.AdminDupMerged(v), nil, adminStyles...)
}

func (s *server) postDuplicateRestore(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) || !s.parseForm(w, r) {
		return
	}
	root := r.PostForm.Get("root_path")
	// Only a folder whose kept book the person can reach is theirs to bring back.
	rows, err := s.lib.MergedFolderList(r.Context(), s.scope(r))
	if err != nil {
		s.serverError(w, r, "merged copies", err)
		return
	}
	found := false
	for _, m := range rows {
		found = found || m.RootPath == root
	}
	if !found {
		s.notFound(w, r)
		return
	}
	libraryID, err := s.lib.RestoreMergedFolder(r.Context(), root, currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, "show merged copy again", err)
		return
	}
	if libraryID != "" {
		if _, err := s.startScan(r.Context(), currentUser(r).ID, libraryID); err != nil {
			s.serverError(w, r, "scan after restore", err)
			return
		}
	}
	s.answerPage(w, r, "/admin/duplicates/merged?done=restored")
}
