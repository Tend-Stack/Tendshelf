package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// grantsLeakEnv is the granted sweep app plus a second library the grant does not cover.
type grantsLeakEnv struct {
	*sweepEnv
	hiddenLib, hiddenAuthor, hiddenSeries string
	hidden                                library.Book
	hiddenAudio                           string
}

func grantsLeakSeed(t *testing.T) *grantsLeakEnv {
	t.Helper()
	e := &grantsLeakEnv{sweepEnv: sweepSeedGranted(t)}
	l, f := librarytest.NewLibrary(t, e.a.lib, "Closed library", adminAudioDir(t, true))
	e.hiddenLib = l.ID
	e.hidden = librarytest.AddBook(t, e.a.lib, f, librarytest.BookSpec{
		Title: "Hiddenword Quill", Authors: []string{"Hiddenauthor Penn"}, Series: "Hiddenseries Saga", Sequence: "1",
		DurationMs: 3 * sweepHourMs, AddedAt: 6,
	})
	ctx := context.Background()
	people, err := e.a.lib.BookPeople(ctx, e.hidden.ID)
	if err != nil || len(people) == 0 {
		t.Fatalf("people: %v %v", people, err)
	}
	e.hiddenAuthor = people[0].PersonID
	series, err := e.a.lib.BookSeries(ctx, e.hidden.ID)
	if err != nil || len(series) == 0 {
		t.Fatalf("series: %v %v", series, err)
	}
	e.hiddenSeries = series[0].SeriesID
	files, err := e.a.lib.AudioFiles(ctx, e.hidden.ID)
	if err != nil || len(files) == 0 {
		t.Fatalf("audio files: %v %v", files, err)
	}
	e.hiddenAudio = files[0].ID
	if err := e.a.lib.RebuildSearch(ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

// hiddenWords must appear nowhere in what a person without access receives.
func (e *grantsLeakEnv) hiddenWords() []string {
	return []string{"Hiddenword", "Hiddenauthor", "Hiddenseries", "Quill", "Penn", "Closed library", e.hidden.ID, e.hiddenLib}
}

// get answers a request as a role. The search page prints the typed query back, which is the person's
// own text and not a leak, so that echo is cut out before the caller scans the body.
func (e *grantsLeakEnv) get(role int, path string, htmx bool) (int, string) {
	code, body := e.fetch(role, path, htmx)
	if _, q, ok := strings.Cut(path, "?q="); ok && q != "" {
		body = strings.ReplaceAll(body, q, "")
	}
	return code, body
}

func (e *grantsLeakEnv) fetch(role int, path string, htmx bool) (int, string) {
	rec := e.a.do(http.MethodGet, path, nil, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, e.ck[role])
	return rec.Code, rec.Body.String()
}

// TestGrantLeaks gives the member and the guest a library grant on the sweep library and checks that
// nothing of a second library leaks through any page, fragment, search or typo suggestion; then that
// revoking the grant, or letting it end, closes the granted book too.
func TestGrantLeaks(t *testing.T) {
	for _, role := range []int{sweepMember, sweepGuest} {
		t.Run(sweepRoleNames[role], func(t *testing.T) {
			e := grantsLeakSeed(t)
			closed := []string{
				"/books/" + e.hidden.ID, "/books/" + e.hidden.ID + "/about", "/books/" + e.hidden.ID + "/chapters",
				"/books/" + e.hidden.ID + "/details", "/books/" + e.hidden.ID + "/bookmarks", "/covers/" + e.hidden.ID + "?w=320",
				"/stream/" + e.hiddenAudio, "/hx/player/" + e.hidden.ID, "/hx/books/" + e.hidden.ID + "/play",
				"/progress/" + e.hidden.ID, "/authors/" + e.hiddenAuthor, "/series/" + e.hiddenSeries,
				"/libraries/" + e.hiddenLib, "/hx/libraries/" + e.hiddenLib + "/items",
			}
			for _, p := range closed {
				if code, body := e.get(role, p, false); code != http.StatusNotFound {
					t.Errorf("GET %s: %d, want 404", p, code)
				} else {
					e.noHidden(t, p, body)
				}
			}
			open := []string{
				"/search?q=Hiddenword", "/search?q=Hiddenwrd", "/search?q=Hiddenauthor", "/", "/libraries", "/hx/libraries/items",
				"/libraries?sheet=filters", "/hx/home/hero",
			}
			if role == sweepMember {
				open = append(open, "/shelves") // the guest has no shelves
			}
			for _, p := range open {
				for _, htmx := range []bool{false, true} {
					code, body := e.get(role, p, htmx)
					if code != http.StatusOK {
						t.Errorf("GET %s (htmx %v): %d, want 200", p, htmx, code)
						continue
					}
					e.noHidden(t, p, body)
				}
			}
			// Control: the owner does see the closed library's book, so the scans above could have failed.
			if _, body := e.get(sweepOwner, "/search?q=Hiddenword", false); !strings.Contains(body, "Quill") {
				t.Error("the owner's search does not show the closed library's book: the leak scans prove nothing")
			}
			// The granted library is open and counted alone.
			if code, body := e.get(role, "/libraries", false); code != http.StatusOK || !strings.Contains(body, "1 book ·") {
				t.Errorf("/libraries with the grant: %d, header counts wrong", code)
			}
			if code, _ := e.get(role, "/books/"+e.book.ID, false); code != http.StatusOK {
				t.Errorf("the granted book: %d, want 200", code)
			}

			// Revoked, and ended: the granted book closes too and the library counts nothing.
			for _, c := range []struct{ name, stmt string }{
				{"revoked", `DELETE FROM grants WHERE user_id = ?`},
				{"expired", `UPDATE grants SET expires_at = 2 WHERE user_id = ?`},
			} {
				e := grantsLeakSeed(t)
				if _, err := e.a.st.Writer().Exec(c.stmt, e.uid[role]); err != nil {
					t.Fatal(err)
				}
				for _, p := range []string{"/books/" + e.book.ID, "/books/" + e.book.ID + "/about", "/covers/" + e.book.ID + "?w=320",
					"/stream/" + e.audio[0], "/hx/player/" + e.book.ID, "/libraries/" + e.libID, "/authors/" + e.author, "/series/" + e.series} {
					if code, _ := e.get(role, p, false); code != http.StatusNotFound {
						t.Errorf("%s: GET %s: %d, want 404", c.name, p, code)
					}
				}
				code, body := e.get(role, "/libraries", false)
				if code != http.StatusOK || !strings.Contains(body, "0 books ·") {
					t.Errorf("%s: /libraries: %d, header should count 0 books", c.name, code)
				}
				for _, w := range append(e.leakWords(), e.hiddenWords()...) {
					if strings.Contains(body, w) {
						t.Errorf("%s: /libraries contains %q", c.name, w)
					}
				}
				for _, p := range []string{"/search?q=Sweepword", "/search?q=Hiddenword", "/", "/hx/libraries/items"} {
					_, body := e.get(role, p, false)
					for _, w := range append(e.leakWords(), e.hiddenWords()...) {
						if strings.Contains(body, w) {
							t.Errorf("%s: %s contains %q", c.name, p, w)
						}
					}
				}
			}
		})
	}
}

func (e *grantsLeakEnv) noHidden(t *testing.T, path, body string) {
	t.Helper()
	for _, w := range e.hiddenWords() {
		if strings.Contains(body, w) {
			t.Errorf("GET %s contains %q", path, w)
		}
	}
}
