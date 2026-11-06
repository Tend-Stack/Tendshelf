package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
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
	mux.HandleFunc("POST /books/{id}/chapters/lookup", s.postChaptersLookup)
}

// chedRow is one row of a draft as typed.
type chedRow struct{ start, title, err string }

// chedDraft is the form as the person has it: rows, the list-level error and the move box.
type chedDraft struct {
	rev, origin                     string
	rows                            []chedRow
	err, saved                      string
	shiftBy, shiftFrom, shiftErrMsg string
	// The lookup (L3b): the job whose answer the page shows, the typed ASIN, its error, and what
	// the last "use" step did.
	lookupJob, lookupASIN, lookupErr, notice string
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
	if job := r.URL.Query().Get("lookup"); job != "" && s.details != nil {
		// A job that is not a chapter lookup of this book is no job at all.
		if _, _, err := s.details.ChapterLookupFor(r.Context(), job, d.Book.ID); err != nil {
			if errors.Is(err, metadata.ErrNotFound) {
				s.notFound(w, r)
				return
			}
			s.serverError(w, r, "chapter lookup", err)
			return
		}
		dr.lookupJob = job
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
		ShiftBy: dr.shiftBy, ShiftFrom: dr.shiftFrom, ShiftError: dr.shiftErrMsg, Notice: dr.notice,
	}
	if on, err := s.lookupsOn(r); err == nil && on {
		asin := dr.lookupASIN
		if asin == "" {
			asin = d.Book.ASIN
		}
		v.Lookup = &pages.ChaptersLookupForm{Action: "/books/" + id + "/chapters/lookup", CSRF: v.CSRF, ASIN: asin,
			Summary: chlkSummary, Label: chlkAsk, Hint: chlkHint, Go: chlkGo, Error: dr.lookupErr, Open: dr.lookupErr != "" || dr.lookupJob != ""}
		if dr.lookupJob != "" {
			if j, l, err := s.details.ChapterLookupFor(r.Context(), dr.lookupJob, id); err == nil {
				v.Result = chaptersLookupBox(j, l, id, d.Book.DurationMs, len(dr.rows))
			}
		}
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
	case "lookup_all", "lookup_titles":
		if !s.useChapterLookup(w, r, d, &dr, kind == "lookup_titles") {
			return
		}
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

var asinForm = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// postChaptersLookup starts a chapter lookup for the typed ASIN and goes to the page that shows its
// answer. It is the only place a chapter lookup begins, and only from a press of the button.
func (s *server) postChaptersLookup(w http.ResponseWriter, r *http.Request) {
	d, ok := s.chaptersBook(w, r)
	if !ok {
		return
	}
	if on, err := s.lookupsOn(r); err != nil {
		s.serverError(w, r, "online setting", err)
		return
	} else if !on {
		s.notFound(w, r)
		return
	}
	asin := strings.ToUpper(strings.TrimSpace(r.PostFormValue("asin")))
	if !asinForm.MatchString(asin) {
		dr := storedDraft(d)
		dr.lookupASIN, dr.lookupErr = r.PostFormValue("asin"), chlkBadASIN
		s.renderChapters(w, r, d, dr)
		return
	}
	job, err := s.details.StartChapterLookup(r.Context(), d.Book.ID, asin, currentUser(r).ID)
	switch {
	case errors.Is(err, metadata.ErrOnlineOff), errors.Is(err, metadata.ErrNotFound):
		s.notFound(w, r)
	case errors.Is(err, online.ErrEmptyQuery):
		dr := storedDraft(d)
		dr.lookupASIN, dr.lookupErr = asin, chlkBadASIN
		s.renderChapters(w, r, d, dr)
	case err != nil:
		s.serverError(w, r, "start chapter lookup", err)
	default:
		s.answerPage(w, r, "/books/"+d.Book.ID+"/chapters/edit?lookup="+job)
	}
}

// chaptersLookupBox is the box for a job: waiting, failed with Audnexus's words, or the comparison.
// n is how many rows the draft has now; titles alone fit only when the counts are equal.
func chaptersLookupBox(j jobs.Job, l online.ChapterList, bookID string, durationMs int64, n int) *pages.ChaptersLookupResult {
	switch j.State {
	case jobs.StateDone:
	case jobs.StateFailed, jobs.StateCancelled:
		msg := j.ErrorText
		if msg == "" {
			msg = chlkFailed
		}
		return &pages.ChaptersLookupResult{State: "failed", Message: msg}
	default:
		return &pages.ChaptersLookupResult{State: "wait", Message: chlkWait, PollHref: "/hx/jobs/" + j.ID + "?book=" + bookID}
	}
	res := &pages.ChaptersLookupResult{State: "done", Job: j.ID, UseAll: chlkUseAll, UseTitles: chlkUseTitles, TitlesNote: chlkTitlesNote,
		CanTitles: len(l.Chapters) == n,
		Found:     fmt.Sprintf(chlkFound, len(l.Chapters), clockText(l.RuntimeMs), clockText(durationMs))}
	if !l.Accurate || absInt64(l.RuntimeMs-durationMs) > 5000 {
		res.Warn = chlkWarn
	}
	return res
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// getChapterLookupJob is GET /hx/jobs/{id} for a chapter lookup: the result box again. The page that
// polls names the book (?book=) and the draft's row count (?n=); the job must be that book's.
func (s *server) getChapterLookupJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	book, ok := s.lookupBook(w, r, r.URL.Query().Get("book"))
	if !ok {
		return
	}
	j, l, err := s.details.ChapterLookupFor(r.Context(), j.ID, book.ID)
	if errors.Is(err, metadata.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "chapter lookup", err)
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	s.fragment(w, r, http.StatusOK, pages.ChaptersLookupBox(*chaptersLookupBox(j, l, book.ID, book.DurationMs, n)))
}

// useChapterLookup fills the draft from the stored result of the job the form names, never from
// values in the form. All rows replace the draft (rows at or past the end of the book are left out
// and counted); titles-only keeps the draft's starts and needs equal counts. It returns false when
// it has already answered.
func (s *server) useChapterLookup(w http.ResponseWriter, r *http.Request, d library.BookDetail, dr *chedDraft, titlesOnly bool) bool {
	if on, err := s.lookupsOn(r); err != nil {
		s.serverError(w, r, "online setting", err)
		return false
	} else if !on {
		s.notFound(w, r)
		return false
	}
	j, l, err := s.details.ChapterLookupFor(r.Context(), r.PostFormValue("job"), d.Book.ID)
	if errors.Is(err, metadata.ErrNotFound) {
		s.notFound(w, r)
		return false
	}
	if err != nil {
		s.serverError(w, r, "chapter lookup", err)
		return false
	}
	if j.State != jobs.StateDone || len(l.Chapters) == 0 {
		dr.err = chlkNotReady
		return true
	}
	title := func(i int) string {
		t := strings.TrimSpace(l.Chapters[i].Title)
		if r := []rune(t); len(r) > library.MaxChapterTitle {
			t = strings.TrimSpace(string(r[:library.MaxChapterTitle]))
		}
		if t == "" {
			t = fmt.Sprintf(bookChapterN, i+1)
		}
		return t
	}
	if titlesOnly {
		if len(l.Chapters) != len(dr.rows) {
			dr.err = chlkCountsDiff
			return true
		}
		for i := range dr.rows {
			dr.rows[i].title, dr.rows[i].err = title(i), ""
		}
		dr.notice = chlkFilledNames
		return true
	}
	dr.rows, dr.origin = nil, "online"
	dropped := 0
	for i, c := range l.Chapters {
		if c.StartMs >= d.Book.DurationMs {
			dropped++
			continue
		}
		dr.rows = append(dr.rows, chedRow{start: clockMsText(max(c.StartMs, 0)), title: title(i)})
	}
	dr.notice = chlkFilledAll
	switch {
	case dropped == 1:
		dr.notice += chlkDropOne
	case dropped > 1:
		dr.notice += fmt.Sprintf(chlkDropMany, dropped)
	}
	if len(dr.rows) == 0 {
		dr.rows = []chedRow{{start: clockMsText(0), title: d.Book.Title}}
	}
	return true
}
