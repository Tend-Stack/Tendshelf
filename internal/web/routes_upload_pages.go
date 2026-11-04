package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The pages and fragments of adding books by upload (decision 0015, package U2). The JSON routes
// that js/upload.js calls are in routes_upload.go. Every route here answers 404 to a person who may
// not add books, to a library that takes no uploads, and to somebody else's upload.

var uploadStyles = []string{"css/pages-upload.css"}

func (s *server) registerUploadPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /libraries/{id}/add", s.getAddBooks)
	mux.HandleFunc("GET /hx/uploads/{id}", s.getUploadRow)
	mux.HandleFunc("GET /books/{id}/remove", s.getBookRemove)
}

// getAddBooks is the Add books page of one library.
func (s *server) getAddBooks(w http.ResponseWriter, r *http.Request) {
	if !s.uploadsReady(w, r) {
		return
	}
	ctx := r.Context()
	lib, err := s.lib.Library(ctx, r.PathValue("id"))
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	} else if err != nil {
		s.serverError(w, r, "add books", err)
		return
	}
	if !s.allow(w, r, share.AddBooks, share.Target{Kind: share.TargetLibrary, ID: lib.ID, LibraryID: lib.ID}) {
		return
	}
	folder, ok := s.uploadFolder(ctx, lib)
	if !ok {
		s.notFound(w, r)
		return
	}
	before, after, _ := strings.Cut(uploadLead, "{path}")
	v := pages.AddBooksView{
		LibraryID: lib.ID, LibraryName: lib.Name, BackHref: "/libraries/" + lib.ID,
		Title: uploadTitle, LeadBefore: before, Folder: folder.Path, LeadAfter: after,
		LibrariesLabel: uploadLibraries, PickHeading: uploadPickHeading, Files: uploadFiles, FolderPick: uploadFolder,
		Hint: uploadHint, QueueHeading: uploadQueue, DropLine: uploadDrop, Several: uploadSeveral,
		NeedsJS: strings.Replace(uploadNeedsJS, "{path}", folder.Path, 1),
		Module:  s.assets.URL("js/upload.js"), Messages: uploadMessages(),
		Template: components.UploadRowView{State: "sending", Template: true, Words: uploadRowWords()},
	}
	libs, err := s.lib.Libraries(ctx)
	if err != nil {
		s.serverError(w, r, "add books", err)
		return
	}
	for _, l := range libs {
		if l.UploadFolderID != "" {
			v.Libraries = append(v.Libraries, pages.LibChip{Name: l.Name, Href: "/libraries/" + l.ID + "/add", Current: l.ID == lib.ID})
		}
	}
	views, err := s.uploads.List(ctx, currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, "add books", err)
		return
	}
	for _, u := range views {
		if u.LibraryID == lib.ID {
			v.Rows = append(v.Rows, s.uploadRow(ctx, u, lib.Name, folder.Path))
		}
	}
	s.pageStyled(w, r, http.StatusOK, uploadTitle, "library", pages.AddBooks(v), nil, uploadStyles...)
}

// uploadFolder is the folder of the library that receives uploads; ok is false when uploads are off.
func (s *server) uploadFolder(ctx context.Context, lib library.Library) (library.Folder, bool) {
	if lib.UploadFolderID == "" {
		return library.Folder{}, false
	}
	f, err := s.lib.Folder(ctx, lib.UploadFolderID)
	return f, err == nil
}

// getUploadRow is the queue row of one upload; a row that is still being worked on polls it.
func (s *server) getUploadRow(w http.ResponseWriter, r *http.Request) {
	libID, ok := s.ownUpload(w, r)
	if !ok {
		return
	}
	s.renderUploadRow(w, r, libID, r.PathValue("id"))
}

// getUploadJob is the jobViews entry of upload.place: the same row, found through the job's upload.
func (s *server) getUploadJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	var p struct {
		UploadID string `json:"upload_id"`
	}
	if s.uploads == nil || json.Unmarshal(j.Payload, &p) != nil || p.UploadID == "" {
		s.notFound(w, r)
		return
	}
	libID, err := s.uploads.LibraryOf(r.Context(), currentUser(r).ID, p.UploadID)
	if err != nil || !s.can.Can(r.Context(), currentUser(r), share.AddBooks, share.Target{Kind: share.TargetLibrary, ID: libID}) {
		s.notFound(w, r)
		return
	}
	s.renderUploadRow(w, r, libID, p.UploadID)
}

func (s *server) renderUploadRow(w http.ResponseWriter, r *http.Request, libID, id string) {
	ctx := r.Context()
	v, err := s.uploads.Get(ctx, currentUser(r).ID, id)
	if err != nil {
		s.uploadError(w, r, "upload row", err)
		return
	}
	lib, err := s.lib.Library(ctx, libID)
	if err != nil {
		s.serverError(w, r, "upload row", err)
		return
	}
	folder, _ := s.uploadFolder(ctx, lib)
	row := s.uploadRow(ctx, v, lib.Name, folder.Path)
	// An archive that was split into several books: the books this poll has not shown yet join the
	// list beside this row, and later polls ask only for the ones after them.
	var more []components.UploadRowView
	if sibs, err := s.uploads.Siblings(ctx, currentUser(r).ID, id); err == nil && len(sibs) > 0 {
		seen, _ := strconv.Atoi(r.URL.Query().Get("seen"))
		seen = max(0, min(seen, len(sibs)))
		for _, sib := range sibs[seen:] {
			m := s.uploadRow(ctx, sib, lib.Name, folder.Path)
			more = append(more, m)
		}
		row.Seen = len(sibs)
	}
	s.fragment(w, r, http.StatusOK, components.UploadRowWithBooks(row, more))
}

// uploadRow turns an upload into the words and state of its queue row.
func (s *server) uploadRow(ctx context.Context, u upload.View, libName, folderPath string) components.UploadRowView {
	var total, sent int64
	exts := map[string]int{}
	for _, f := range u.Files {
		total += f.Size
		sent += f.Offset
		exts[strings.ToLower(strings.TrimPrefix(path.Ext(f.Path), "."))]++
	}
	pct := 0
	if total > 0 {
		pct = int(sent * 100 / total)
	}
	meta := uploadFilesText(len(u.Files))
	if ext := topExt(exts); ext != "" {
		meta += " · " + ext
	}
	v := components.UploadRowView{ID: u.ID, Title: u.Label, Meta: meta, Percent: pct, Words: uploadRowWords()}
	switch u.State {
	case upload.StateReceiving:
		v.State = "stopped"
		v.Line = "Stopped at " + strconv.Itoa(pct) + " %. Choose the same files again to carry on."
	case upload.StateQueued:
		v.State, v.Line = "checking", uploadChecking
	case upload.StatePlacing:
		v.State, v.Line = "placing", "Putting it in "+folderPath
	case upload.StatePlaced:
		s.placedRow(ctx, &v, u, libName, folderPath)
	case upload.StateRefused:
		v.State, v.Title = "refused", fmt.Sprintf(uploadNotAdded, u.Label)
		v.Line = path.Base(u.Refused) + " is copy-protected, so Tendshelf will not open it. Nothing was kept."
	default:
		v.State, v.Title = "failed", fmt.Sprintf(uploadNotAddedYet, u.Label)
		v.Line = uploadPlaceFailed
		if u.ErrorCode == "no_space" {
			v.Line = uploadNoSpace
		}
	}
	return v
}

// placedRow is a published book: "Adding it to your library" until the scoped import has made the
// book, then "Added to {library}" with its folder and a link.
func (s *server) placedRow(ctx context.Context, v *components.UploadRowView, u upload.View, libName, folderPath string) {
	var bookID string
	if b, err := s.lib.BookByRootPath(ctx, u.BookPath); err == nil {
		bookID = b.ID
	}
	done := bookID != ""
	if !done && u.ScanJobID != "" {
		if j, err := s.jobs.Get(ctx, u.ScanJobID); err == nil && (j.State == jobs.StateDone || j.State == jobs.StateFailed || j.State == jobs.StateCancelled) {
			done = true
		}
	}
	if !done {
		v.State, v.Line = "adding", uploadAdding
		return
	}
	v.State, v.Meta = "added", "Added to "+libName
	if rel, err := filepath.Rel(folderPath, u.BookPath); err == nil {
		v.Path = filepath.ToSlash(rel)
	}
	if bookID != "" {
		v.OpenHref = "/books/" + bookID
	}
	if u.Skipped > 0 {
		v.Left = uploadLeftOut(u.Skipped)
	}
}

// topExt is the file extension most files of the upload have, or "".
func topExt(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// getBookRemove is the confirmation page of "Remove this book": it lists the files and posts to
// POST /books/{id}/remove. A book Tendshelf did not add answers 404.
func (s *server) getBookRemove(w http.ResponseWriter, r *http.Request) {
	if s.uploads == nil {
		s.notFound(w, r)
		return
	}
	b, ok := s.loadBook(w, r, r.PathValue("id"), share.AddBooks)
	if !ok {
		return
	}
	if !s.uploads.Removable(r.Context(), b.ID) {
		s.notFound(w, r)
		return
	}
	files, err := s.lib.AudioFiles(r.Context(), b.ID)
	if err != nil {
		s.serverError(w, r, "remove book", err)
		return
	}
	v := pages.BookRemoveView{
		Title: bookRemoveConfirm, Body: fmt.Sprintf(bookRemoveBody, len(files)), Button: bookRemoveButton, Cancel: bookRemoveCancel,
		Action: "/books/" + b.ID + "/remove", CancelHref: "/books/" + b.ID, CSRF: s.csrf.TokenFrom(r.Context()),
	}
	for _, f := range files {
		if rel, err := filepath.Rel(b.RootPath, f.Path); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			v.Files = append(v.Files, filepath.ToSlash(rel))
		} else {
			v.Files = append(v.Files, filepath.Base(f.Path))
		}
	}
	s.pageStyled(w, r, http.StatusOK, bookRemoveConfirm, "library", pages.BookRemove(v), nil, uploadStyles...)
}

// adminUploadsView is the section "Adding books from this device"; nil when uploads are not set up.
// errLib and errText put the refusal of a failed save under that library.
func (s *server) adminUploadsView(ctx context.Context, r *http.Request, errLib, errText string) (*pages.AdminUploadsView, error) {
	if s.uploads == nil {
		return nil, nil
	}
	libs, err := s.lib.Libraries(ctx)
	if err != nil {
		return nil, err
	}
	v := &pages.AdminUploadsView{
		Title: uploadAdminTitle, Lead: uploadAdminLead, Where: uploadAdminWhere, Off: uploadAdminOff, Save: uploadAdminSave,
		OwnerOnly: uploadAdminOwner,
		Owner:     s.can.Can(ctx, currentUser(r), share.ManageServer, share.Target{Kind: share.TargetServer}),
	}
	for _, l := range libs {
		folders, err := s.lib.Folders(ctx, l.ID)
		if err != nil {
			return nil, err
		}
		al := pages.AdminUploadLibrary{ID: l.ID, Name: l.Name, Chosen: l.UploadFolderID}
		for _, f := range folders {
			al.Folders = append(al.Folders, pages.AdminUploadFolder{ID: f.ID, Path: f.Path})
			if f.ID == l.UploadFolderID {
				al.ChosenPath = f.Path
			}
		}
		if al.ChosenPath != "" {
			al.AddHref, al.AddLabel = "/libraries/"+l.ID+"/add", uploadAdminOpen
		}
		if l.ID == errLib {
			al.Error = errText
		}
		v.Libraries = append(v.Libraries, al)
	}
	return v, nil
}

// addBooksHref is the library header's button: the Add books page of the library shown, or of the
// only library when all are shown. It is empty unless the person may add books there.
func (s *server) addBooksHref(r *http.Request, cur *library.LibrarySummary, libs []library.LibrarySummary) string {
	if s.uploads == nil {
		return ""
	}
	var id string
	switch {
	case cur != nil:
		id = cur.ID
	case len(libs) == 1:
		id = libs[0].ID
	default:
		return ""
	}
	// The summaries do not carry the upload folder, so the library is read for it.
	lib, err := s.lib.Library(r.Context(), id)
	if err != nil || lib.UploadFolderID == "" ||
		!s.can.Can(r.Context(), currentUser(r), share.AddBooks, share.Target{Kind: share.TargetLibrary, ID: lib.ID, LibraryID: lib.ID}) {
		return ""
	}
	return "/libraries/" + lib.ID + "/add"
}
