// Package librarytest seeds a library database for tests. It is a normal package so several test
// packages can share it; production code never imports it.
package librarytest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// BookSpec describes one seeded book. Zero values mean "none"; Files defaults to one part.
type BookSpec struct {
	Title, SortTitle, Subtitle, Description string
	Authors, Narrators                      []string
	Series, Sequence                        string
	Genres                                  []string
	DurationMs                              int64
	AddedAt                                 int64
	PublishedYear                           int
	Refused                                 string
	MissingSince                            int64
	Files                                   int
	// Unsettled leaves the audio files without probe data, as before the first probe.
	Unsettled bool
}

var bookCounter atomic.Int64

// NewLibrary creates a library with one folder.
func NewLibrary(t testing.TB, r *library.Repo, name, path string) (library.Library, library.Folder) {
	t.Helper()
	l, err := r.CreateLibrary(context.Background(), name, 0)
	if err != nil {
		t.Fatal(err)
	}
	f, err := r.AddFolder(context.Background(), l.ID, path)
	if err != nil {
		t.Fatal(err)
	}
	return l, f
}

func slug(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "book"
	}
	return b.String()
}

// AddBook inserts a book with its audio files, people, series and genres in one transaction.
func AddBook(t testing.TB, r *library.Repo, f library.Folder, s BookSpec) library.Book {
	t.Helper()
	b := library.Book{
		LibraryID: f.LibraryID, FolderID: f.ID,
		RootPath: filepath.Join(f.Path, fmt.Sprintf("%s-%d", slug(s.Title), bookCounter.Add(1))),
		Title:    s.Title, SortTitle: s.SortTitle, Subtitle: s.Subtitle, Description: s.Description,
		PublishedYear: s.PublishedYear, DurationMs: s.DurationMs, AddedAt: s.AddedAt,
		RefusedReason: s.Refused,
	}
	if b.SortTitle == "" {
		b.SortTitle = b.Title
	}
	files := max(s.Files, 1)
	roles := []struct {
		role  string
		names []string
	}{{library.RoleAuthor, s.Authors}, {library.RoleNarrator, s.Narrators}}
	err := r.Update(context.Background(), func(tx *library.Tx) error {
		if err := tx.InsertBook(&b); err != nil {
			return err
		}
		for i := 0; i < files; i++ {
			af := library.AudioFile{
				BookID: b.ID, Idx: i, Size: int64(1000 * i),
				Path: filepath.Join(b.RootPath, fmt.Sprintf("part-%d.mp3", i)),
			}
			sum := sha256.Sum256([]byte(af.Path))
			af.QuickHash = hex.EncodeToString(sum[:])
			if !s.Unsettled {
				af.ProbeJSON = []byte(`{}`)
			}
			if err := tx.PutAudioFile(&af); err != nil {
				return err
			}
		}
		for _, rl := range roles {
			var ids []string
			for _, n := range rl.names {
				id, err := tx.EnsurePerson(n, "")
				if err != nil {
					return err
				}
				ids = append(ids, id)
			}
			if err := tx.SetBookPeople(b.ID, rl.role, ids); err != nil {
				return err
			}
		}
		if s.Series != "" {
			id, err := tx.EnsureSeries(s.Series, "")
			if err != nil {
				return err
			}
			if err := tx.SetBookSeries(b.ID, []library.BookSeriesLink{{SeriesID: id, Sequence: s.Sequence}}); err != nil {
				return err
			}
		}
		if err := tx.SetBookLabels(b.ID, library.LabelGenre, s.Genres); err != nil {
			return err
		}
		if s.MissingSince != 0 {
			if err := tx.SetBookMissing(b.ID, s.MissingSince); err != nil {
				return err
			}
			b.MissingSince = s.MissingSince
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
