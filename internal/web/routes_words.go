package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The word list (M5 G1): "Your word list", private to the signed-in person. Nothing in a request names a person;
// the list is joined through the person's scope in SQL, so a book that was revoked lists nothing and its title never
// leaves the database. The words stay stored.

const (
	wordsRoute    = "/me/words"
	wordsPageSize = 50
)

func (s *server) registerWords(mux *http.ServeMux) {
	mux.HandleFunc("GET /me/words", s.getWords)
	mux.HandleFunc("POST /me/words/{id}/delete", s.postWordDelete)
	mux.HandleFunc("GET /me/words.md", s.getWordsExport)
	mux.HandleFunc("GET /me/words.json", s.getWordsExport)
}

func wordDay(ms int64) string { return time.UnixMilli(ms).UTC().Format("2 Jan 2006") }

// wordJump is the address of the page a word was saved on, "" when the place is gone.
func wordJump(x library.WordListed) string {
	ch, _, _, ok := ebook.ParseSID(x.SID)
	if x.EditionID == "" || x.SID == "" || !ok {
		return ""
	}
	q := url.Values{"ch": {strconv.Itoa(ch)}, "at": {x.SID}}
	return "/books/" + x.BookID + "/read?" + q.Encode() + "#s-" + x.SID
}

func (s *server) getWords(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	book := r.URL.Query().Get("book")
	words, next, err := s.lib.WordList(r.Context(), s.scope(r), u.ID, book, r.URL.Query().Get("cursor"), wordsPageSize)
	if err != nil {
		if errors.Is(err, library.ErrInvalid) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, "word list", err)
		return
	}
	v := pages.WordsView{
		Back: wordsBack, BackHref: "/me", Title: wordsTitle, Lead: wordsLead, CSRF: s.csrf.TokenFrom(r.Context()),
		EmptyTitle: wordsEmptyTitle, EmptyBody: wordsEmptyBody, RemoveLabel: wordsDelete,
		ExportMD: wordsRoute + ".md", ExportMDLabel: wordsExportMD, ExportJSON: wordsRoute + ".json", ExportJSONLabel: wordsExportJSON,
	}
	if book != "" {
		v.FilterHref, v.FilterLabel = wordsRoute, wordsAllBooks
		v.Filter = fmt.Sprintf(wordsOneBook, "")
		if len(words) > 0 {
			v.Filter = fmt.Sprintf(wordsOneBook, words[0].BookTitle)
		}
	}
	for _, x := range words {
		row := pages.WordRow{Word: x.Word, Quote: x.Quote, Book: x.BookTitle, BookHref: "/books/" + x.BookID, When: wordDay(x.CreatedAt),
			DeleteAction: wordsRoute + "/" + x.ID + "/delete", DeleteLabel: fmt.Sprintf(wordsRemoveFmt, x.Word)}
		if row.JumpHref = wordJump(x); row.JumpHref != "" {
			row.JumpLabel = wordsGoTo
		}
		v.Rows = append(v.Rows, row)
	}
	if next != "" {
		q := url.Values{"cursor": {next}}
		if book != "" {
			q.Set("book", book)
		}
		v.MoreHref, v.MoreLabel = wordsRoute+"?"+q.Encode(), wordsMore
	}
	s.pageStyled(w, r, http.StatusOK, wordsTitle, "profile", pages.Words(v), nil, pages.WordsStyles...)
}

func (s *server) postWordDelete(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	err := s.progress.DeleteWord(r.Context(), currentUser(r).ID, r.PathValue("id"))
	if errors.Is(err, progress.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "delete word", err)
		return
	}
	http.Redirect(w, r, wordsRoute, http.StatusSeeOther)
}

// wordExport is one word of the download.
type wordExport struct {
	Word  string `json:"word"`
	Quote string `json:"quote"`
	Book  string `json:"book"`
	Saved string `json:"saved"`
}

// getWordsExport sends every word the person can still see, as Markdown or JSON, as a download.
func (s *server) getWordsExport(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var list []wordExport
	for cursor := ""; ; {
		page, next, err := s.lib.WordList(r.Context(), s.scope(r), u.ID, "", cursor, library.WordListMax)
		if err != nil {
			s.serverError(w, r, "export words", err)
			return
		}
		for _, x := range page {
			list = append(list, wordExport{Word: x.Word, Quote: x.Quote, Book: x.BookTitle, Saved: time.UnixMilli(x.CreatedAt).UTC().Format("2006-01-02")})
		}
		if next == "" {
			break
		}
		cursor = next
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	if strings.HasSuffix(r.URL.Path, ".json") {
		h.Set("Content-Type", "application/json")
		h.Set("Content-Disposition", `attachment; filename="tendshelf-words.json"`)
		if list == nil {
			list = []wordExport{}
		}
		b, _ := json.MarshalIndent(struct {
			Words []wordExport `json:"words"`
		}{list}, "", "  ")
		_, _ = w.Write(append(b, '\n'))
		return
	}
	h.Set("Content-Type", "text/markdown; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="tendshelf-words.md"`)
	var b strings.Builder
	b.WriteString("# " + wordsTitle + "\n")
	book := ""
	for i, x := range list {
		if i == 0 || x.Book != book {
			book = x.Book
			b.WriteString("\n## " + x.Book + "\n\n")
		}
		b.WriteString("- **" + x.Word + "**")
		if x.Quote != "" {
			b.WriteString(" - " + x.Quote)
		}
		b.WriteString(" (" + x.Saved + ")\n")
	}
	_, _ = w.Write([]byte(b.String()))
}
