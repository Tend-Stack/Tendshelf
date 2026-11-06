package scan

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// chedBook scans and probes a one-file book with two chapters in the file, and returns its id and file.
func chedBook(t *testing.T) (*env, string, string) {
	t.Helper()
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	path := fx.file("Au/One/b.m4b", mediatest.FakeFile{DurationMs: 6000, Chapters: []mediatest.FakeChapter{
		{StartMs: 0, EndMs: 3000, Title: "First"}, {StartMs: 3000, EndMs: 6000, Title: "Second"}}})
	e.scan()
	e.probeAll()
	return e, e.bookAt(filepath.Dir(path)).ID, path
}

func chedRows(t *testing.T, e *env, id string) []library.Chapter {
	t.Helper()
	rows, err := e.repo.Chapters(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func chedEdit(t *testing.T, e *env, id, source string) []library.Chapter {
	t.Helper()
	want := []library.Chapter{
		{Idx: 0, StartMs: 0, EndMs: 2000, Title: "Mine", Source: source},
		{Idx: 1, StartMs: 2000, EndMs: 6000, Title: "Mine two", Source: source},
	}
	if err := e.repo.Update(bg, func(tx *library.Tx) error { return tx.ReplaceChapters(id, want) }); err != nil {
		t.Fatal(err)
	}
	return want
}

func TestChapterEditSurvivesRescan(t *testing.T) {
	for _, source := range []string{"user", "online"} {
		t.Run(source, func(t *testing.T) {
			e, id, path := chedBook(t)
			if got := chedRows(t, e, id); len(got) != 2 || got[0].Source != "file" {
				t.Fatalf("file chapters = %+v", got)
			}
			want := chedEdit(t, e, id, source)

			e.scan()
			e.probeAll()
			if got := chedRows(t, e, id); !reflect.DeepEqual(got, want) {
				t.Errorf("after a rescan: %+v", got)
			}

			// A changed, shorter file: the edit stays verbatim, the book's length follows the file.
			fx := newFixture(t, e.root)
			fx.file("Au/One/b.m4b", mediatest.FakeFile{DurationMs: 3000})
			e.scan()
			e.probeAll()
			if got := chedRows(t, e, id); !reflect.DeepEqual(got, want) {
				t.Errorf("after the file shrank: %+v", got)
			}
			if b, _ := e.repo.Book(bg, id); b.DurationMs != 3000 {
				t.Errorf("duration = %d, want 3000", b.DurationMs)
			}

			// A missing file.
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			e.scan()
			e.probeAll()
			if got := chedRows(t, e, id); !reflect.DeepEqual(got, want) {
				t.Errorf("after the file vanished: %+v", got)
			}
		})
	}
}

func TestChapterEditResetRestoresFileList(t *testing.T) {
	e, id, path := chedBook(t)
	file := chedRows(t, e, id)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	chedEdit(t, e, id, "user")
	if err := ResetChapters(bg, e.repo, id); err != nil {
		t.Fatal(err)
	}
	if got := chedRows(t, e, id); !reflect.DeepEqual(got, file) {
		t.Errorf("after reset: %+v, want %+v", got, file)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("a library file changed")
	}
}

func TestChapterEditResetBusyKeepsRows(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	path := fx.file("Au/One/b.m4b", mediatest.FakeFile{DurationMs: 6000})
	e.scan() // not probed yet: the file waits for its answer
	id := e.bookAt(filepath.Dir(path)).ID
	want := chedEdit(t, e, id, "user")
	if err := ResetChapters(bg, e.repo, id); !errors.Is(err, ErrChaptersBusy) {
		t.Fatalf("err = %v, want ErrChaptersBusy", err)
	}
	if got := chedRows(t, e, id); !reflect.DeepEqual(got, want) {
		t.Errorf("rows after a busy reset: %+v", got)
	}
}
