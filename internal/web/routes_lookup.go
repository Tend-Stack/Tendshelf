package web

import (
	"cmp"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/dict"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Look-up and saving a word (M5 G1). Definitions come only from dictionary files the owner put in
// <data>/dictionaries (internal/dict); nothing is bundled and nothing is fetched. The web search is a link the
// reader taps, and only the selected word is in it. Every route asks share.Can(ReadText) through loadBook or
// readerCtx; the word list is read and written only for the signed-in person.

const lookupMaxRunes = 64

func (s *server) registerLookup(mux *http.ServeMux) {
	mux.HandleFunc("GET /hx/books/{id}/read/lookup", s.getReaderLookup)
	mux.HandleFunc("POST /hx/books/{id}/words", s.postReaderWord)
}

// lookupEnglish is true for a book whose language says English, the only language that gets the suffix forms.
func lookupEnglish(language string) bool {
	l := strings.ToLower(strings.TrimSpace(language))
	return l == "en" || l == "eng" || strings.HasPrefix(l, "en-") || strings.HasPrefix(l, "en_")
}

// lookupWord checks the selected text: 1 to 64 runes with at least one letter or digit.
func lookupWord(q string) (string, bool) {
	q = strings.TrimSpace(q)
	n := utf8.RuneCountInString(q)
	if n < 1 || n > lookupMaxRunes || !utf8.ValidString(q) || dict.Fold(q) == "" {
		return "", false
	}
	return q, true
}

func (s *server) getReaderLookup(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.readerCtx(w, r)
	if !ok {
		return
	}
	q, ok := lookupWord(r.URL.Query().Get("q"))
	if !ok {
		s.readerToast(w, r, http.StatusUnprocessableEntity, lookupBadWord)
		return
	}
	found := s.dicts.Find(q)
	if len(found) == 0 && lookupEnglish(cmp.Or(rc.ed.Language, rc.doc.Meta.Language)) {
		for _, form := range dict.Forms(q) {
			if found = s.dicts.Find(form); len(found) > 0 {
				break
			}
		}
	}
	v := pages.ReaderLookupView{
		Title: lookupTitle, Word: q, None: lookupNone, SaveLabel: lookupSave, CloseLabel: lookupClose, CanSave: true,
		SearchLabel: strings.Replace(lookupSearch, "%s", q, 1), SearchHref: lookupSearchURL + url.QueryEscape(q), Outside: lookupOutside,
	}
	for _, e := range found {
		v.Entries = append(v.Entries, pages.ReaderLookupEntry{Dictionary: e.Dictionary, Word: e.Word, Paras: strings.Split(e.Text, "\n")})
	}
	if len(v.Entries) == 0 && s.dicts.Count() == 0 {
		v.None = lookupNoDictionary
		if s.can.Can(r.Context(), currentUser(r), share.ManageServer, share.Target{Kind: share.TargetServer}) {
			v.AdminHint = lookupAdminHint
		}
	}
	s.fragment(w, r, http.StatusOK, pages.ReaderLookup(v))
}

func (s *server) postReaderWord(w http.ResponseWriter, r *http.Request) {
	book, ok := s.loadBook(w, r, r.PathValue("id"), share.ReadText)
	if !ok || !s.readerHx(w, r) {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	off, _ := strconv.Atoi(r.PostFormValue("off"))
	word, ok := lookupWord(r.PostFormValue("word"))
	if !ok {
		s.readerToast(w, r, http.StatusUnprocessableEntity, lookupBadWord)
		return
	}
	_, _, err := s.progress.AddWord(r.Context(), progress.Word{UserID: currentUser(r).ID, BookID: book.ID, EditionID: r.PostFormValue("edition_id"),
		Word: word, Quote: r.PostFormValue("quote"), SID: r.PostFormValue("sid"), Off: off})
	var invalid *progress.InvalidError
	switch {
	case errors.Is(err, progress.ErrLimit):
		s.readerToast(w, r, http.StatusConflict, lookupFull)
	case errors.Is(err, progress.ErrNotFound):
		s.notFound(w, r)
	case errors.As(err, &invalid):
		slog.Warn("word refused", "field", invalid.Field)
		s.readerToast(w, r, http.StatusUnprocessableEntity, lookupBadWord)
	case err != nil:
		s.serverError(w, r, "save word", err)
	default:
		s.fragment(w, r, http.StatusOK, components.ToastOOB("ok", lookupSaved))
	}
}
