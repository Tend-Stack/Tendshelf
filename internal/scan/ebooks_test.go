package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

func writeEbookFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanEbookSplit(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	var got []EbookScan
	e.sc.OnEbooks(func(_ context.Context, es EbookScan) { got = append(got, es) })

	writeEbookFixture(t, filepath.Join(e.root, "Alpha", "01.mp3"), "audio")
	writeEbookFixture(t, filepath.Join(e.root, "Alpha", "alpha.epub"), "epub bytes")
	writeEbookFixture(t, filepath.Join(e.root, "Alpha", "notes.pdf"), "pdf is upload only")
	writeEbookFixture(t, filepath.Join(e.root, "Alpha", "alpha.kfx"), "kfx")
	writeEbookFixture(t, filepath.Join(e.root, "Only Ebook", "solo.azw3"), "azw3")

	res := e.scan()
	if res.BooksAdded != 1 {
		t.Fatalf("books added = %d, want 1 (a folder with only an ebook makes no book)", res.BooksAdded)
	}
	if _, err := e.repo.BookByRootPath(bg, filepath.Join(e.root, "Only Ebook")); err == nil {
		t.Fatal("a book was made from an ebook-only folder")
	}
	if res.FilesSeen != 1 || res.ProbesEnqueued != 1 {
		t.Fatalf("files seen %d, probes %d; want 1 and 1 (ebooks are never probed)", res.FilesSeen, res.ProbesEnqueued)
	}
	if len(got) != 1 || len(got[0].Folders) != 1 {
		t.Fatalf("sink calls = %+v", got)
	}
	var names []string
	for _, f := range got[0].Folders[0].Files {
		names = append(names, f.RelDir+"/"+f.Name)
	}
	want := []string{"Alpha/alpha.epub", "Alpha/alpha.kfx", "Only Ebook/solo.azw3"}
	if len(names) != len(want) {
		t.Fatalf("ebooks = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("ebooks = %v, want %v", names, want)
		}
	}
	if got[0].LibraryID != e.lib.ID || got[0].Folders[0].FolderID != e.folder.ID {
		t.Fatalf("sink scan = %+v", got[0])
	}
}

// A PDF beside audio is a booklet and stays ignored; alone in its folder it is an ebook.
func TestAddEbookPDFRule(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	var got []EbookScan
	e.sc.OnEbooks(func(_ context.Context, es EbookScan) { got = append(got, es) })
	writeEbookFixture(t, filepath.Join(e.root, "Alpha", "01.mp3"), "audio")
	writeEbookFixture(t, filepath.Join(e.root, "Alpha", "booklet.pdf"), "pdf")
	writeEbookFixture(t, filepath.Join(e.root, "Beta", "paper.pdf"), "pdf")
	writeEbookFixture(t, filepath.Join(e.root, "Beta", "sub", "01.mp3"), "audio")
	e.scan()
	var names []string
	for _, es := range got {
		for _, fo := range es.Folders {
			for _, f := range fo.Files {
				names = append(names, f.RelDir+"/"+f.Name)
			}
		}
	}
	if len(names) != 1 || names[0] != "Beta/paper.pdf" {
		t.Fatalf("ebooks = %v, want only Beta/paper.pdf", names)
	}
}
