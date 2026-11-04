package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/upload"
)

// Adding books by upload (decision 0015, package U1). The JSON routes are used by js/upload.js: a
// session and the X-CSRF-Token header, errors through api. Every route needs share.AddBooks (owner
// and admin, never by grant) and, for an upload, that it is the signed-in person's own. A refusal,
// an unknown id and somebody else's upload all answer 404. Turning uploads on is the owner's.
// Nothing here touches a library folder: internal/upload does, and only below the chosen folder.

const (
	maxManifestBody = 1 << 20
	chunkReadWindow = 120 * time.Second
)

func (s *server) registerUploads(mux *http.ServeMux) {
	mux.HandleFunc("POST /libraries/{id}/uploads", s.postUploadCreate)
	mux.HandleFunc("GET /uploads/{id}", s.getUpload)
	mux.HandleFunc("PUT /uploads/{id}/files/{fileID}", s.putUploadChunk)
	mux.HandleFunc("POST /uploads/{id}/finish", s.postUploadFinish)
	mux.HandleFunc("POST /uploads/{id}/cancel", s.postUploadCancel)
	mux.HandleFunc("POST /admin/libraries/{id}/uploads", s.postUploadSetting)
	mux.HandleFunc("POST /books/{id}/remove", s.postBookRemove)
}

// uploadRefusal is the JSON error body of an upload route; offset is set only for wrong_offset.
type uploadRefusal struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field,omitempty"`
	} `json:"error"`
	Offset *int64 `json:"offset,omitempty"`
}

// uploadError answers an error of the upload service: its own status and words, or a 500.
func (s *server) uploadError(w http.ResponseWriter, r *http.Request, what string, err error) {
	var ue *upload.Error
	if !errors.As(err, &ue) {
		s.serverError(w, r, what, err)
		return
	}
	if ue.Status == http.StatusNotFound {
		s.notFound(w, r)
		return
	}
	var body uploadRefusal
	body.Error.Code, body.Error.Message, body.Error.Field = ue.Code, ue.Message, ue.Field
	if ue.Code == "wrong_offset" {
		body.Offset = &ue.Offset
	}
	api.Write(w, ue.Status, body)
}

// uploadsReady answers 404 unless uploads are set up and the person may add books at all.
func (s *server) uploadsReady(w http.ResponseWriter, r *http.Request) bool {
	if s.uploads == nil {
		s.notFound(w, r)
		return false
	}
	return s.allow(w, r, share.AddBooks, share.Target{Kind: share.TargetServer})
}

// ownUpload checks the person may add books and that the upload is theirs; it returns the library.
func (s *server) ownUpload(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.uploadsReady(w, r) {
		return "", false
	}
	libID, err := s.uploads.LibraryOf(r.Context(), currentUser(r).ID, r.PathValue("id"))
	if err != nil {
		s.uploadError(w, r, "upload", err)
		return "", false
	}
	if !s.allow(w, r, share.AddBooks, share.Target{Kind: share.TargetLibrary, ID: libID}) {
		return "", false
	}
	return libID, true
}

func (s *server) postUploadCreate(w http.ResponseWriter, r *http.Request) {
	if !s.uploadsReady(w, r) {
		return
	}
	libID := r.PathValue("id")
	if !s.allow(w, r, share.AddBooks, share.Target{Kind: share.TargetLibrary, ID: libID}) {
		return
	}
	var m upload.Manifest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxManifestBody))
	if err := dec.Decode(&m); err != nil {
		api.Error(w, http.StatusBadRequest, "bad_json", "Tendshelf could not read that list of files.")
		return
	}
	v, err := s.uploads.Create(r.Context(), currentUser(r).ID, libID, m)
	if err != nil {
		s.uploadError(w, r, "start upload", err)
		return
	}
	api.Write(w, http.StatusCreated, v)
}

func (s *server) getUpload(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.ownUpload(w, r); !ok {
		return
	}
	v, err := s.uploads.Get(r.Context(), currentUser(r).ID, r.PathValue("id"))
	if err != nil {
		s.uploadError(w, r, "get upload", err)
		return
	}
	api.Write(w, http.StatusOK, v)
}

func (s *server) putUploadChunk(w http.ResponseWriter, r *http.Request) {
	if !s.uploadsReady(w, r) {
		return
	}
	// The body is raw bytes, never a form: anything else is refused before it is read.
	if ct := strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]); !strings.EqualFold(ct, "application/octet-stream") {
		api.Error(w, http.StatusUnsupportedMediaType, "bad_type", "Pieces of a file are sent as raw bytes.")
		return
	}
	if _, ok := s.ownUpload(w, r); !ok {
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		api.ErrorField(w, http.StatusBadRequest, "bad_offset", "offset", "That position is not a number.")
		return
	}
	// A slow link may take a while for 8 MiB; the deadline is per request, not for the server.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(chunkReadWindow))
	body := http.MaxBytesReader(w, r.Body, s.uploads.ChunkSize()+1)
	n, err := s.uploads.WriteChunk(r.Context(), currentUser(r).ID, r.PathValue("id"), r.PathValue("fileID"),
		offset, strings.TrimSpace(r.Header.Get("X-Chunk-SHA256")), body)
	if err != nil {
		s.uploadError(w, r, "upload chunk", err)
		return
	}
	api.Write(w, http.StatusOK, map[string]int64{"offset": n})
}

func (s *server) postUploadFinish(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.ownUpload(w, r); !ok {
		return
	}
	job, err := s.uploads.Finish(r.Context(), currentUser(r).ID, r.PathValue("id"))
	if err != nil {
		s.uploadError(w, r, "finish upload", err)
		return
	}
	api.Write(w, http.StatusAccepted, map[string]string{"job_id": job})
}

func (s *server) postUploadCancel(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.ownUpload(w, r); !ok {
		return
	}
	if err := s.uploads.Cancel(r.Context(), currentUser(r).ID, r.PathValue("id")); err != nil {
		s.uploadError(w, r, "cancel upload", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// uploadsCannotWrite is shown when the chosen folder fails the write check.
const uploadsCannotWrite = "Tendshelf cannot write in this folder. Check its permissions."

// postUploadSetting turns uploads on for a library (folder_id) or off (empty). Only the owner may:
// it lets the server write to the owner's disk.
func (s *server) postUploadSetting(w http.ResponseWriter, r *http.Request) {
	if s.uploads == nil {
		s.notFound(w, r)
		return
	}
	if !s.allow(w, r, share.ManageServer, share.Target{Kind: share.TargetServer}) {
		return
	}
	ctx := r.Context()
	libID := r.PathValue("id")
	lib, err := s.lib.Library(ctx, libID)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	} else if err != nil {
		s.serverError(w, r, "upload setting", err)
		return
	}
	folderID := strings.TrimSpace(r.PostFormValue("folder_id"))
	if folderID != "" {
		f, err := s.lib.Folder(ctx, folderID)
		if err != nil || f.LibraryID != lib.ID {
			s.renderAdminLibraries(w, r, http.StatusUnprocessableEntity, uploadsCannotWrite)
			return
		}
		if err := s.uploads.Writable(ctx, folderID); err != nil {
			s.renderAdminLibraries(w, r, http.StatusUnprocessableEntity, uploadsCannotWrite)
			return
		}
	}
	if err := s.lib.SetUploadFolder(ctx, lib.ID, folderID); err != nil {
		if errors.Is(err, library.ErrNotFound) {
			s.renderAdminLibraries(w, r, http.StatusUnprocessableEntity, uploadsCannotWrite)
			return
		}
		s.serverError(w, r, "upload setting", err)
		return
	}
	s.audit(ctx, currentUser(r).ID, "library.uploads", "library:"+lib.ID, map[string]string{"folder_id": folderID})
	s.answerPage(w, r, "/admin/libraries#uploads-h")
}

// postBookRemove removes a book Tendshelf added by upload: its unchanged files and the book. The
// page asks for a confirmation first; the service refuses a book it did not add.
func (s *server) postBookRemove(w http.ResponseWriter, r *http.Request) {
	if s.uploads == nil {
		s.notFound(w, r)
		return
	}
	b, ok := s.loadBook(w, r, r.PathValue("id"), share.AddBooks)
	if !ok {
		return
	}
	if _, err := s.uploads.Remove(r.Context(), currentUser(r).ID, b.ID); err != nil {
		var ue *upload.Error
		if errors.As(err, &ue) && ue.Status != http.StatusNotFound {
			http.Error(w, ue.Message, ue.Status)
			return
		}
		if errors.As(err, &ue) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, "remove book", err)
		return
	}
	s.answerPage(w, r, "/libraries/"+b.LibraryID)
}
