package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The chapter editor (L3a). It changes `chapters` rows only: no code here opens a library file, and
// the file changes only through "Write details into the file". Every route needs share.EditBook on
// an audio book the person can list. Add, delete and move round-trip through the server as an
// unsaved draft (200 with the page, so it swaps safely and works as a plain POST); only Save stores.

const chedBodyLimit = 1 << 20

func (s *server) registerChapters(mux *http.ServeMux) {
	// The page is /chapters/edit: GET /books/{id}/chapters is the book page's Chapters tab.
	mux.HandleFunc("GET /books/{id}/chapters/edit", s.getChapters)
	mux.HandleFunc("POST /books/{id}/chapters", s.postChapters)
	mux.HandleFunc("GET /books/{id}/chapters/reset", s.getChaptersReset)
	mux.HandleFunc("POST /books/{id}/chapters/reset", s.postChaptersReset)
}

// chedRow is one row of a draft as typed.
type chedRow struct{ start, title, err string }

// chedDraft is the form as the person has it: rows, the list-level error and the move box.
type chedDraft struct {
	rev, origin                     string
	rows                            []chedRow
	err, saved                      string
	shiftBy, shiftFrom, shiftErrMsg string
}

// chaptersBook loads the book of a chapter route: 404 unless the person may edit it, it is an
// audio book they can list, and it has a length.
func (s *server) chaptersBook(w http.ResponseWriter, r *http.Request) (library.BookDetail, bool) {
	b, ok := s.loadBook(w, r, r.PathValue("id"), share.EditBook)
	if !ok {
		return library.BookDetail{}, false
	}
	d, err := s.lib.BookDetail(r.Context(), s.scope(r), b.ID)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return d, false
	}
	if err != nil {
		s.serverError(w, r, "chapters", err)
		return d, false
	}
	if d.Book.Kind == library.BookText || d.Book.DurationMs <= 0 {
		s.notFound(w, r)
		return d, false
	}
	return d, true
}

func (s *server) getChapters(w http.ResponseWriter, r *http.Request) {
	d, ok := s.chaptersBook(w, r)
	if !ok {
		return
	}
	dr := storedDraft(d)
	if r.URL.Query().Get("saved") == "1" {
		dr.saved = chedSaved
	}
	s.renderChapters(w, r, d, dr)
}

// storedDraft is the form for the rows as stored.
func storedDraft(d library.BookDetail) chedDraft {
	dr := chedDraft{rev: library.ChaptersRev(d.Chapters), shiftFrom: "2"}
	for i, c := range d.Chapters {
		title := strings.TrimSpace(c.Title)
		if title == "" {
			title = fmt.Sprintf(bookChapterN, i+1) // the name the book page shows; every chapter needs one to be saved
		}
		dr.rows = append(dr.rows, chedRow{start: clockMsText(c.StartMs), title: title})
	}
	if len(dr.rows) == 0 {
		dr.rows = []chedRow{{start: clockMsText(0), title: d.Book.Title}}
	}
	return dr
}

func (s *server) renderChapters(w http.ResponseWriter, r *http.Request, d library.BookDetail, dr chedDraft) {
	id := d.Book.ID
	v := pages.ChaptersEditData{
		CSRF: s.csrf.TokenFrom(r.Context()), Action: "/books/" + id + "/chapters", BookID: id, BookTitle: d.Book.Title,
		BackHref: bookTabHref(id, "chapters"), Title: chedTitle, Lead: chedLead, BackLabel: chedBack,
		Rev: dr.rev, Origin: dr.origin, Saved: dr.saved, Error: dr.err,
		StartHead: chedStart, NameHead: chedName, ListenWord: chedListen, DeleteWord: chedDelete, AddWord: chedAdd, SaveLabel: chedSave,
		MoveTitle: chedMoveTitle, MoveBy: chedMoveBy, MoveByHint: chedMoveByHint, MoveFrom: chedMoveFrom, MoveGo: chedMoveGo,
		ShiftBy: dr.shiftBy, ShiftFrom: dr.shiftFrom, ShiftError: dr.shiftErrMsg,
	}
	if v.ShiftFrom == "" {
		v.ShiftFrom = "2"
	}
	if files, err := s.lib.AudioFiles(r.Context(), id); err == nil {
		live, flac := 0, false
		for _, f := range files {
			if f.MissingSince == 0 {
				live++
				format, _ := media.WriteFormat(f.Container, f.Path)
				flac = flac || format == "flac"
			}
		}
		if live != 1 || flac {
			v.Note = chedStays
		}
	}
	for i, row := range dr.rows {
		play := int64(-1)
		if ms, ok := parseClock(row.start); ok && ms < d.Book.DurationMs {
			play = ms
		}
		n := i + 1
		v.Rows = append(v.Rows, pages.ChaptersEditRow{
			Num: n, Start: row.start, Title: row.title, Error: row.err, PlayAtMs: play,
			StartLabel: fmt.Sprintf(chedStartFmt, n), NameLabel: fmt.Sprintf(chedNameFmt, n),
			ListenLabel: fmt.Sprintf(chedListenFmt, n), DeleteLabel: fmt.Sprintf(chedDeleteFmt, n), AddLabel: fmt.Sprintf(chedAddFmt, n),
			DeleteOp: "del:" + strconv.Itoa(i), AddOp: "add:" + strconv.Itoa(i),
		})
	}
	if library.ChaptersOverridden(d.Chapters) {
		v.ResetHref, v.ResetLabel = "/books/"+id+"/chapters/reset", chedReset
	}
	s.pageStyled(w, r, http.StatusOK, chedTitle, "library", pages.ChaptersEditView(v), nil, pages.ChaptersStyles...)
}

func (s *server) postChapters(w http.ResponseWriter, r *http.Request) {
	d, ok := s.chaptersBook(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, chedBodyLimit)
	if err := r.ParseForm(); err != nil {
		s.bookmarkRefuse(w, r, http.StatusRequestEntityTooLarge, chedErrTooMany)
		return
	}
	n, err := strconv.Atoi(r.PostForm.Get("n"))
	if err != nil || n < 0 || n > library.MaxChapters {
		s.bookmarkRefuse(w, r, http.StatusBadRequest, chedErrTooMany)
		return
	}
	origin := ""
	if r.PostForm.Get("origin") == "online" {
		origin = "online"
	}
	dr := chedDraft{rev: r.PostForm.Get("rev"), origin: origin,
		shiftBy: strings.TrimSpace(r.PostForm.Get("shift_by")), shiftFrom: strings.TrimSpace(r.PostForm.Get("shift_from"))}
	for i := range n {
		dr.rows = append(dr.rows, chedRow{
			start: strings.TrimSpace(r.PostForm.Get("start_" + strconv.Itoa(i))),
			title: r.PostForm.Get("title_" + strconv.Itoa(i)),
		})
	}
	op := r.PostForm.Get("op")
	switch kind, arg, _ := strings.Cut(op, ":"); kind {
	case "save":
		s.saveChapters(w, r, d, dr)
		return
	case "del":
		if i, err := strconv.Atoi(arg); err == nil && i >= 0 && i < len(dr.rows) {
			dr.rows = append(dr.rows[:i], dr.rows[i+1:]...)
			if i == 0 && len(dr.rows) > 0 {
				dr.rows[0].start = clockMsText(0) // the next chapter now opens the book
			}
		}
	case "add":
		if i, err := strconv.Atoi(arg); err == nil && i >= 0 && i < len(dr.rows) {
			addChapterRow(&dr, i, d.Book.DurationMs)
		}
	case "shift":
		shiftChapters(&dr, d.Book.DurationMs)
	}
	s.renderChapters(w, r, d, dr)
}

// addChapterRow inserts an empty row after row i, starting halfway to the next start (or the end).
func addChapterRow(dr *chedDraft, i int, durationMs int64) {
	from, ok := parseClock(dr.rows[i].start)
	if !ok {
		dr.rows[i].err = chedErrClock
		return
	}
	next := durationMs
	if i+1 < len(dr.rows) {
		if next, ok = parseClock(dr.rows[i+1].start); !ok {
			dr.rows[i+1].err = chedErrClock
			return
		}
	}
	mid := from + (next-from)/2
	if mid <= from || mid >= next {
		dr.rows[i].err = chedErrNoRoom
		return
	}
	dr.rows = append(dr.rows[:i+1], append([]chedRow{{start: clockMsText(mid)}}, dr.rows[i+1:]...)...)
}

// shiftChapters moves the starts from the chosen chapter on by the typed seconds: all or none.
func shiftChapters(dr *chedDraft, durationMs int64) {
	secs, err := strconv.ParseFloat(strings.TrimSpace(dr.shiftBy), 64)
	if err != nil || math.IsNaN(secs) || math.IsInf(secs, 0) || math.Abs(secs) > 1e6 {
		dr.shiftErrMsg = chedErrSecs
		return
	}
	from := 2
	if dr.shiftFrom != "" {
		if from, err = strconv.Atoi(dr.shiftFrom); err != nil || from < 2 || from > len(dr.rows) {
			dr.shiftErrMsg = chedErrMoveRow
			return
		}
	}
	delta := int64(math.Round(secs * 1000))
	starts := make([]int64, len(dr.rows))
	for i, row := range dr.rows {
		ms, ok := parseClock(row.start)
		if !ok {
			dr.rows[i].err = chedErrClock
			dr.shiftErrMsg = chedErrMoveBad
			return
		}
		starts[i] = ms
	}
	for i := from - 1; i < len(starts); i++ {
		starts[i] += delta
		if starts[i] <= starts[i-1] && i == from-1 || starts[i] >= durationMs {
			dr.shiftErrMsg = chedErrMoveBad
			return
		}
	}
	for i := from - 1; i < len(starts); i++ {
		dr.rows[i].start = clockMsText(starts[i])
	}
	dr.shiftBy, dr.shiftFrom = "", "2"
}

var chedRowErrors = map[string]string{
	library.ChapterFirstNotZero: chedErrFirst, library.ChapterOutOfOrder: chedErrOrder, library.ChapterPastEnd: chedErrPastEnd,
	library.ChapterNoTitle: chedErrNoName, library.ChapterTitleLong: chedErrLong,
}

func (s *server) saveChapters(w http.ResponseWriter, r *http.Request, d library.BookDetail, dr chedDraft) {
	drafts := make([]library.ChapterDraft, len(dr.rows))
	bad := false
	for i, row := range dr.rows {
		ms, ok := parseClock(row.start)
		if !ok {
			dr.rows[i].err, bad = chedErrClock, true
		}
		drafts[i] = library.ChapterDraft{StartMs: ms, Title: row.title}
	}
	if bad {
		s.renderChapters(w, r, d, dr)
		return
	}
	source := "user"
	if dr.origin == "online" {
		source = "online"
	}
	id := d.Book.ID
	var (
		stale         bool
		cerr          *library.ChapterError
		before, after int
	)
	err := s.lib.Update(r.Context(), func(tx *library.Tx) error {
		cur, err := tx.Chapters(id)
		if err != nil {
			return err
		}
		if library.ChaptersRev(cur) != dr.rev {
			stale = true
			return nil
		}
		bk, err := tx.Book(id)
		if err != nil {
			return err
		}
		chs, ce := library.BuildChapters(drafts, bk.DurationMs, source)
		if ce != nil {
			cerr = ce
			return nil
		}
		before, after = len(cur), len(chs)
		return tx.ReplaceChapters(id, chs)
	})
	switch {
	case err != nil:
		s.serverError(w, r, "save chapters", err)
	case stale:
		dr.err = chedErrStale
		s.renderChapters(w, r, d, dr)
	case cerr != nil:
		switch {
		case cerr.Row >= 0 && cerr.Row < len(dr.rows):
			dr.rows[cerr.Row].err = chedRowErrors[cerr.Code]
		case cerr.Code == library.ChapterTooMany:
			dr.err = chedErrTooMany
		default:
			dr.err = chedErrEmpty
		}
		s.renderChapters(w, r, d, dr)
	default:
		s.audit(r.Context(), currentUser(r).ID, "chapters.edit", id, map[string]any{"before": before, "after": after, "source": source})
		s.answerPage(w, r, "/books/"+id+"/chapters/edit?saved=1")
	}
}

func (s *server) getChaptersReset(w http.ResponseWriter, r *http.Request) {
	d, ok := s.chaptersBook(w, r)
	if !ok {
		return
	}
	if !library.ChaptersOverridden(d.Chapters) {
		s.notFound(w, r)
		return
	}
	id := d.Book.ID
	v := pages.ChaptersResetView{CSRF: s.csrf.TokenFrom(r.Context()), Title: chedResetTitle, Body: chedResetBody,
		Action: "/books/" + id + "/chapters/reset", Confirm: chedResetDo, Keep: chedResetKeep, Back: "/books/" + id + "/chapters/edit"}
	s.pageStyled(w, r, http.StatusOK, chedResetTitle, "library", pages.ChaptersResetConfirm(v), nil, pages.ChaptersStyles...)
}

func (s *server) postChaptersReset(w http.ResponseWriter, r *http.Request) {
	d, ok := s.chaptersBook(w, r)
	if !ok {
		return
	}
	id := d.Book.ID
	switch err := scan.ResetChapters(r.Context(), s.lib, id); {
	case errors.Is(err, scan.ErrChaptersBusy):
		dr := storedDraft(d)
		dr.err = chedErrBusy
		s.renderChapters(w, r, d, dr)
	case err != nil:
		s.serverError(w, r, "reset chapters", err)
	default:
		s.audit(r.Context(), currentUser(r).ID, "chapters.reset", id, map[string]any{"before": len(d.Chapters)})
		s.answerPage(w, r, "/books/"+id+"/chapters/edit")
	}
}

// parseClock reads "h:mm:ss" or "m:ss", either with ".mmm" after the seconds, into milliseconds.
func parseClock(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	secPart, frac, hasFrac := strings.Cut(s, ".")
	parts := strings.Split(secPart, ":")
	if len(parts) < 2 || len(parts) > 3 || (hasFrac && (frac == "" || len(frac) > 3)) {
		return 0, false
	}
	var ms int64
	for i, p := range parts {
		if p == "" || len(p) > 5 {
			return 0, false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		v, _ := strconv.ParseInt(p, 10, 64)
		if i > 0 && v > 59 {
			return 0, false
		}
		ms = ms*60 + v
	}
	ms *= 1000
	if hasFrac {
		for _, c := range frac {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		f, _ := strconv.ParseInt((frac + "00")[:3], 10, 64)
		ms += f
	}
	return ms, true
}

// clockMsText writes a start as a clock, with milliseconds only when there are some.
func clockMsText(ms int64) string {
	t := clockText(ms)
	if f := ms % 1000; ms > 0 && f != 0 {
		t += fmt.Sprintf(".%03d", f)
	}
	return t
}

// chaptersEditLink adds "Edit chapters" under the chapter list for people who may edit the book.
func (s *server) chaptersEditLink(r *http.Request, v *pages.BookView, d library.BookDetail) {
	t := share.Target{Kind: share.TargetBook, ID: d.Book.ID, LibraryID: d.Book.LibraryID}
	if d.Book.DurationMs > 0 && s.can.Can(r.Context(), currentUser(r), share.EditBook, t) {
		v.Chapters.EditHref, v.Chapters.EditLabel = "/books/"+d.Book.ID+"/chapters/edit", chedEditLink
	}
}
