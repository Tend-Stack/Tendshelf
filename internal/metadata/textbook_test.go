package metadata_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// tbTextBook makes a text book inside the library folder with a ready file whose edition says fileTitle.
func tbTextBook(e *env, rel, fileTitle, metaJSON string) library.Book {
	e.t.Helper()
	root := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("epub bytes"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	folders, err := e.repo.Folders(bg, e.lib.ID)
	if err != nil || len(folders) != 1 {
		e.t.Fatalf("folders: %v %v", folders, err)
	}
	id, err := e.repo.CreateTextBook(bg, library.TextBookIn{LibraryID: e.lib.ID, FolderID: folders[0].ID, RootPath: root, Title: "placeholder"})
	if err != nil {
		e.t.Fatal(err)
	}
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, q := range []string{
		`INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, book_default, ext, size, quick_hash, sha256, format, state, created_at, updated_at)
VALUES ('ef_1','library','` + e.lib.ID + `','` + folders[0].ID + `','` + root + `','` + id + `',1,'epub',10,'` + sha + `','` + sha + `','epub','ready',1,1)`,
		`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, authors_json, isbn, chapter_count, word_count, char_count, meta_json, created_at)
VALUES ('ed_1','ef_1','` + sha + `','` + ebook.Recipe + `','` + sha + `',5,'epub','` + fileTitle + `','["Ann Author"]','9780306406157',1,1,1,'` + metaJSON + `',1)`,
	} {
		if _, err := e.st.Writer().Exec(q); err != nil {
			e.t.Fatalf("%s: %v", q, err)
		}
	}
	b, err := e.repo.Book(bg, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func TestTextBookMetadataPrecedence(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	b := tbTextBook(e, "Author Name/The Folder Title.epub", "The File Title", `{"publisher":"Acme","year":1999,"series":"Saga","series_index":2}`)

	// file beats folder
	if _, err := e.svc.Local(bg, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := e.repo.Book(bg, b.ID)
	if got.Title != "The File Title" || got.ISBN != "9780306406157" || got.Publisher != "Acme" || got.PublishedYear != 1999 || got.Kind != library.BookText {
		t.Fatalf("file did not beat folder: %+v", got)
	}
	people, _ := e.repo.BookPeople(bg, b.ID)
	if len(people) != 1 || people[0].Name != "Ann Author" {
		t.Errorf("people = %+v", people)
	}
	series, _ := e.repo.BookSeries(bg, b.ID)
	if len(series) != 1 || series[0].Name != "Saga" || series[0].Sequence != "2" {
		t.Errorf("series = %+v", series)
	}

	// opf beats file (a sidecar that shares the file's name)
	opf := filepath.Join(e.root, "Author Name", "The Folder Title.opf")
	if err := os.WriteFile(opf, []byte(`<package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>The Opf Title</dc:title></metadata></package>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Local(bg, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.repo.Book(bg, b.ID); got.Title != "The Opf Title" {
		t.Fatalf("opf did not beat file: %q", got.Title)
	}

	// user beats opf
	err := e.repo.Update(bg, func(tx *library.Tx) error {
		v, _ := json.Marshal("The User Title")
		return tx.PutUserMetadataValue(b.ID, library.MetadataValue{Field: "title", Value: v, ObservedAt: 1})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Local(bg, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.repo.Book(bg, b.ID); got.Title != "The User Title" {
		t.Fatalf("user did not beat opf: %q", got.Title)
	}

	// A sidecar in a flat folder of several books is nobody's: metadata.opf must not be lent.
	b2 := tbTextBook2(e, "Flat/one.epub")
	flatOPF := filepath.Join(e.root, "Flat", "metadata.opf")
	if err := os.WriteFile(flatOPF, []byte(`<package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Borrowed</dc:title></metadata></package>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, "Flat", "two.epub"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Local(bg, b2.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.repo.Book(bg, b2.ID); got.Title == "Borrowed" {
		t.Error("a sidecar of a flat folder was lent to one of its books")
	}
}

// tbTextBook2 adds a second text book (file ef_2, edition ed_2) under the same library.
func tbTextBook2(e *env, rel string) library.Book {
	e.t.Helper()
	root := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("epub bytes 2"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	folders, _ := e.repo.Folders(bg, e.lib.ID)
	id, err := e.repo.CreateTextBook(bg, library.TextBookIn{LibraryID: e.lib.ID, FolderID: folders[0].ID, RootPath: root, Title: "two"})
	if err != nil {
		e.t.Fatal(err)
	}
	sha := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, q := range []string{
		`INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, book_default, ext, size, quick_hash, sha256, format, state, created_at, updated_at)
VALUES ('ef_2','library','` + e.lib.ID + `','` + folders[0].ID + `','` + root + `','` + id + `',1,'epub',10,'` + sha + `','` + sha + `','epub','ready',1,1)`,
		`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, chapter_count, word_count, char_count, created_at)
VALUES ('ed_2','ef_2','` + sha + `','` + ebook.Recipe + `','` + sha + `',5,'epub','Second',1,1,1,1)`,
	} {
		if _, err := e.st.Writer().Exec(q); err != nil {
			e.t.Fatalf("%s: %v", q, err)
		}
	}
	b, _ := e.repo.Book(bg, id)
	return b
}
