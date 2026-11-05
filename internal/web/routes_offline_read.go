package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Offline reading, server side (decision 0019). Two routes serve the download of a book's text: the
// manifest, which lists every address to store and a revision that changes when the stored copy is stale,
// and the reader shell, the book's reader page without chapter text, which reader-offline.js fills in.
// Both need the right to read the text and the right to download (the rule of the ebook file download).
// Neither writes anything.

func (s *server) registerOfflineRead(mux *http.ServeMux) {
	mux.HandleFunc("GET /offline/books/{id}/manifest", s.getOfflineManifest)
	mux.HandleFunc("GET /offline/books/{id}/read", s.getOfflineShell)
}

// offlineImageBytes is the guess of one picture's size when the download is sized for the person.
const offlineImageBytes = 150_000

// offlineReadCtx is the gate of both routes: the reader's own checks, then the download right.
func (s *server) offlineReadCtx(w http.ResponseWriter, r *http.Request) (readerCtx, bool) {
	rc, ok := s.readerCtx(w, r)
	if !ok {
		return rc, false
	}
	if !s.mayDownloadText(r, rc.book) {
		s.notFound(w, r)
		return rc, false
	}
	return rc, true
}

// offlineManifest is the answer of GET /offline/books/{id}/manifest.
type offlineManifest struct {
	BookID     string   `json:"bookId"`
	EditionID  string   `json:"editionId"`
	Revision   string   `json:"revision"`
	Title      string   `json:"title"`
	Author     string   `json:"author"`
	Cover      string   `json:"cover"`
	Bytes      int      `json:"bytes"`
	FinishedAt int64    `json:"finishedAt"`
	Parts      []string `json:"parts"`
	Images     []string `json:"images"`
}

func (s *server) getOfflineManifest(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.offlineReadCtx(w, r)
	if !ok {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	m := offlineManifest{BookID: rc.book.ID, EditionID: rc.ed.ID, Title: rc.book.Title, Parts: []string{}, Images: []string{}}
	for i, ch := range rc.doc.Chapters {
		for p := range rsParts(ch) {
			m.Parts = append(m.Parts, rc.rsFragURL(i+1, p+1))
		}
		for _, b := range ch.Blocks {
			// The image route sends no SVG, so an SVG picture is not worth a request.
			if b.Kind == ebook.KindImg && rc.doc.Format == "epub" && b.Img != "" && !strings.HasSuffix(strings.ToLower(b.Img), ".svg") {
				m.Images = append(m.Images, rsImageURL(rc, b))
			}
		}
	}
	m.Bytes = 3*rc.ed.CharCount + offlineImageBytes*len(m.Images)
	cards, err := s.lib.CardsByID(ctx, s.scope(r), []string{rc.book.ID})
	if err != nil {
		s.serverError(w, r, "offline manifest card", err)
		return
	}
	if len(cards) > 0 {
		cv := cardView(cards[0])
		m.Cover, m.Author = cv.CoverSrc, cv.Byline
	}
	if pos, found, err := s.progress.Reading(ctx, u.ID, rc.book.ID); err != nil {
		s.serverError(w, r, "offline manifest place", err)
		return
	} else if found {
		m.FinishedAt = pos.FinishedAt
	}
	hls, err := s.progress.Highlights(ctx, u.ID, rc.book.ID)
	if err != nil {
		s.serverError(w, r, "offline manifest highlights", err)
		return
	}
	bms, err := s.progress.TextBookmarks(ctx, u.ID, rc.book.ID)
	if err != nil {
		s.serverError(w, r, "offline manifest bookmarks", err)
		return
	}
	// The revision changes when what the stored chapter parts show changes: the edition, the person's
	// highlights (and their edits) and the text bookmarks.
	h := sha256.New()
	h.Write([]byte(rc.ed.ID))
	for _, hl := range hls {
		h.Write([]byte("\nh " + hl.ID + ":" + strconv.FormatInt(hl.UpdatedAt, 10)))
	}
	for _, b := range bms {
		h.Write([]byte("\nb " + b.ID))
	}
	m.Revision = hex.EncodeToString(h.Sum(nil))[:16]
	writeJSON(w, http.StatusOK, m)
}

// getOfflineShell answers GET /offline/books/{id}/read[?sheet=contents]: the reader page with no chapter
// text and none of the parts that need the server (search, marks, bookmark, settings, speaker, highlight
// colours). The worker keeps it under /books/{id}/read.
func (s *server) getOfflineShell(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.offlineReadCtx(w, r)
	if !ok {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	sheet := r.URL.Query().Get("sheet")
	if sheet != "" && sheet != "contents" {
		s.notFound(w, r)
		return
	}
	pl, ok, err := s.rsStartPlace(ctx, u, rc, url.Values{})
	if err != nil {
		s.serverError(w, r, "offline shell place", err)
		return
	}
	if !ok {
		s.notFound(w, r)
		return
	}
	chap, err := s.readerChapter(ctx, u, rc, 1, 1)
	if err != nil {
		s.serverError(w, r, "offline shell chapter", err)
		return
	}
	v, err := s.readerView(r, rc, chap, pl)
	if err != nil {
		s.serverError(w, r, "offline shell view", err)
		return
	}
	var placed int64
	if pos, found, err := s.progress.Reading(ctx, u.ID, rc.book.ID); err != nil {
		s.serverError(w, r, "offline shell reading", err)
		return
	} else if found {
		placed = pos.ClientUpdatedAt
	}
	v.Offline = &pages.ReaderOfflineView{
		Module: s.assetOr("js/reader-offline.js"), ReaderSrc: s.assetOr("js/reader.js"), Shell: true, Placed: placed,
		StartCh: pl.Ch, StartPart: pl.Part,
		Frag:       "/hx/books/" + rc.book.ID + "/read/chapters/{c}?" + rc.edq(),
		PartStarts: offlinePartStarts(rc),
	}
	v.ReaderModule, v.SettingsModule = "", ""
	v.SearchHref, v.MarksHref, v.SettingsHref, v.BookmarkURL = "", "", "", ""
	v.Speaker = nil
	v.Marks.Swatches = nil
	if sheet == "contents" {
		sv, err := s.readerSheet(r, rc, "contents", 0)
		if err != nil {
			s.serverError(w, r, "offline shell contents", err)
			return
		}
		sv.CloseHref = "/books/" + rc.book.ID + "/read"
		s.pageCached(w, r, http.StatusOK, "private, no-store", v.Title, "", pages.Reader(v), pages.ReaderSheet(sv), pages.ReaderStyles...)
		return
	}
	s.pageCached(w, r, http.StatusOK, "private, no-store", v.Title, "", pages.Reader(v), nil, pages.ReaderStyles...)
}

// offlinePartStarts is {"<chapter>":[1,121,...]}: the 1-based number of the first block of each part, for the
// chapters that have more than one part. The shell turns a sentence id into a part with it.
func offlinePartStarts(rc readerCtx) string {
	out := map[string][]int{}
	for i, ch := range rc.doc.Chapters {
		ps := rsParts(ch)
		if len(ps) < 2 {
			continue
		}
		starts := make([]int, len(ps))
		for k, p := range ps {
			starts[k] = p.From + 1
		}
		out[strconv.Itoa(i+1)] = starts
	}
	b, _ := json.Marshal(out)
	return string(b)
}
