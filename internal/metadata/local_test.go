package metadata_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

var bg = context.Background()

func TestMain(m *testing.M) {
	mediatest.RunFakeIfRequested()
	os.Exit(m.Run())
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// nopEnq stands in for the job runner: the scanner's probes are run by the test itself.
type nopEnq struct{}

func (nopEnq) Enqueue(context.Context, string, any, jobs.EnqueueOptions) (string, error) {
	return "jb_nop", nil
}
func (nopEnq) ActiveKeys(context.Context, string, string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

// env is a scanned library of fake files and a details service over it.
type env struct {
	t    *testing.T
	st   *db.Store
	repo *library.Repo
	sc   *scan.Scanner
	svc  *metadata.Service
	lib  library.Library
	root string
}

func newEnv(t *testing.T, tools media.Tools) *env {
	t.Helper()
	st, err := db.Open(bg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	repo := library.New(st)
	lib, err := repo.CreateLibrary(bg, "Books", 0)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddFolder(bg, lib.ID, root); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, st: st, repo: repo, sc: scan.New(repo, tools, nopEnq{}, quiet()),
		svc: metadata.New(repo, nopEnq{}, quiet()), lib: lib, root: root}
}

func (e *env) write(rel string, f mediatest.FakeFile) string {
	e.t.Helper()
	p := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	mediatest.WriteFake(e.t, p, f)
	return p
}

// scanAndProbe scans the library and probes every file that waits for it.
func (e *env) scanAndProbe() {
	e.t.Helper()
	if _, err := e.sc.ScanLibrary(bg, e.lib.ID, nil); err != nil {
		e.t.Fatal(err)
	}
	ids, err := e.repo.UnsettledAudioFileIDs(bg, e.lib.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := e.sc.ProbeFile(bg, id, nil); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *env) book(rel string) library.Book {
	e.t.Helper()
	b, err := e.repo.BookByRootPath(bg, filepath.Join(e.root, filepath.FromSlash(rel)))
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) local(id string) bool {
	e.t.Helper()
	changed, err := e.svc.Local(bg, id, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return changed
}

func (e *env) changes() int64 {
	e.t.Helper()
	var n int64
	if err := e.st.Writer().QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.st.Reader().QueryRow(query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func names(ps []library.BookPerson, role string) []string {
	var out []string
	for _, p := range ps {
		if p.Role == role {
			out = append(out, p.Name)
		}
	}
	return out
}

func TestLocalWritesDetailsAndIsIdempotent(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	tags := map[string]string{"album": "The Tide", "genre": "Fantasy; Drama", "date": "2020-05-01", "comment": "An unabridged recording", "composer": "Eve Reader"}
	for _, n := range []string{"01.mp3", "02.mp3"} {
		e.write("Ann Author/Moon Series/Book 3 - The Tide {Dee Narr}/"+n, mediatest.FakeFile{Container: "mp3", Codec: "mp3", BitRate: 64000, Tags: tags})
	}
	e.scanAndProbe()
	b := e.book("Ann Author/Moon Series/Book 3 - The Tide {Dee Narr}")

	if !e.local(b.ID) {
		t.Fatal("first Local reported no change")
	}
	got, err := e.repo.Book(bg, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "The Tide" || got.SortTitle != "Tide" || got.PublishedYear != 2020 || got.Abridged || got.Dramatized {
		t.Fatalf("book = %+v", got)
	}
	people, _ := e.repo.BookPeople(bg, b.ID)
	// composer is a narrator fallback in tags, and tags outrank the folder's {Dee Narr}
	if !reflect.DeepEqual(names(people, "narrator"), []string{"Eve Reader"}) || len(names(people, "author")) != 1 {
		t.Fatalf("people = %+v", people)
	}
	series, _ := e.repo.BookSeries(bg, b.ID)
	if len(series) != 1 || series[0].Name != "Moon Series" || series[0].Sequence != "3" {
		t.Fatalf("series = %+v", series)
	}
	labels, _ := e.repo.BookLabels(bg, b.ID)
	if !reflect.DeepEqual(labels["genre"], []string{"Drama", "Fantasy"}) {
		t.Fatalf("labels = %v", labels)
	}
	vals, _ := e.repo.MetadataValues(bg, b.ID)
	sources := map[string]bool{}
	for _, v := range vals {
		sources[v.Source] = true
		if v.ObservedAt == 0 {
			t.Errorf("%s/%s has no observed_at", v.Field, v.Source)
		}
	}
	if !reflect.DeepEqual(sources, map[string]bool{"folder": true, "tags": true}) {
		t.Fatalf("sources = %v", sources)
	}

	before := e.changes()
	if e.local(b.ID) {
		t.Fatal("second Local reported a change")
	}
	if after := e.changes(); after != before {
		t.Fatalf("second Local made %d row changes", after-before)
	}
}

func TestLocalNeverWritesUserValues(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	e.write("Ann Author/Folder Title/x.m4b", mediatest.FakeFile{Tags: map[string]string{"album": "Tag Title", "publisher": "Tag Pub"}})
	e.scanAndProbe()
	b := e.book("Ann Author/Folder Title")
	for _, f := range [][2]string{{"title", `"My Title"`}, {"publisher", `""`}} {
		if _, err := e.st.Writer().Exec(`INSERT INTO metadata_values(book_id, field, source, value_json, observed_at) VALUES (?,?,?,?,1)`,
			b.ID, f[0], "user", f[1]); err != nil {
			t.Fatal(err)
		}
	}
	if !e.local(b.ID) {
		t.Fatal("Local changed nothing")
	}
	got, _ := e.repo.Book(bg, b.ID)
	if got.Title != "My Title" || got.Publisher != "" {
		t.Fatalf("user values lost: title %q publisher %q", got.Title, got.Publisher)
	}
	vals, _ := e.repo.MetadataValues(bg, b.ID)
	var user, tagsPub int
	for _, v := range vals {
		if v.Source == "user" {
			user++
			if v.ObservedAt != 1 {
				t.Errorf("user row %s rewritten", v.Field)
			}
		}
		if v.Source == "tags" && v.Field == "publisher" {
			tagsPub++
		}
	}
	if user != 2 || tagsPub != 1 {
		t.Fatalf("user rows %d, tags publisher rows %d", user, tagsPub)
	}
	if e.local(b.ID) {
		t.Fatal("second Local reported a change")
	}
}

func TestLocalRemovesStaleRows(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	e.write("Au/Book/x.m4b", mediatest.FakeFile{})
	e.scanAndProbe()
	b := e.book("Au/Book")
	for _, src := range []string{"tags", "folder", "nfo"} {
		if _, err := e.st.Writer().Exec(`INSERT INTO metadata_values(book_id, field, source, value_json, observed_at) VALUES (?,?,?,?,1)`,
			b.ID, "subtitle", src, `"Old"`); err != nil {
			t.Fatal(err)
		}
	}
	e.local(b.ID)
	if n := e.count(`SELECT count(*) FROM metadata_values WHERE field = 'subtitle' AND source IN ('tags','folder')`); n != 0 {
		t.Fatalf("%d stale rows left", n)
	}
	if n := e.count(`SELECT count(*) FROM metadata_values WHERE field = 'subtitle' AND source = 'nfo'`); n != 1 {
		t.Fatal("a source this package does not own was deleted")
	}
	got, _ := e.repo.Book(bg, b.ID)
	if got.Subtitle != "Old" {
		t.Fatalf("subtitle = %q, want the nfo value", got.Subtitle)
	}
}

func TestLocalSharesPeopleByName(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	e.write("ann lee/One/x.m4b", mediatest.FakeFile{})
	e.write("Ann Lee/Two/y.m4b", mediatest.FakeFile{})
	e.scanAndProbe()
	for _, rel := range []string{"ann lee/One", "Ann Lee/Two"} {
		e.local(e.book(rel).ID)
	}
	if n := e.count(`SELECT count(*) FROM people`); n != 1 {
		t.Fatalf("%d people rows, want 1", n)
	}
	if n := e.count(`SELECT count(DISTINCT person_id) FROM book_people WHERE role = 'author'`); n != 1 {
		t.Fatalf("%d distinct authors", n)
	}
}

func TestLocalLeavesBooksWithoutLiveFilesAlone(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	p := e.write("Au/Book/x.m4b", mediatest.FakeFile{Tags: map[string]string{"album": "Tagged"}})
	e.write("Other/Keep/k.m4b", mediatest.FakeFile{}) // a scan that finds no files at all changes nothing
	e.scanAndProbe()
	b := e.book("Au/Book")
	e.local(b.ID)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	e.scanAndProbe()
	if got := e.book("Au/Book"); got.MissingSince == 0 {
		t.Fatalf("book not marked missing: %+v", got)
	}
	rows := e.count(`SELECT count(*) FROM metadata_values WHERE book_id = ?`, b.ID)
	before := e.changes()
	if e.local(b.ID) || e.changes() != before {
		t.Fatal("Local touched a book whose files are all missing")
	}
	if got := e.book("Au/Book"); got.Title != "Tagged" || e.count(`SELECT count(*) FROM metadata_values WHERE book_id = ?`, b.ID) != rows {
		t.Fatalf("values lost: %+v", got)
	}
}

func TestLocalGoneBookAndGuard(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	if changed, err := e.svc.Local(bg, "bk_gone", nil); err != nil || changed {
		t.Fatalf("gone book: %v, %v", changed, err)
	}
	e.write("Au/Book/x.m4b", mediatest.FakeFile{Tags: map[string]string{"album": "Tagged"}})
	e.scanAndProbe()
	b := e.book("Au/Book")
	lost := errors.New("lease lost")
	before := e.changes()
	_, err := e.svc.Local(bg, b.ID, func(context.Context, *sql.Tx) error { return lost })
	if !errors.Is(err, lost) || e.changes() != before {
		t.Fatalf("guard error = %v, changes %d", err, e.changes()-before)
	}
	if got, _ := e.repo.Book(bg, b.ID); got.Title == "Tagged" {
		t.Fatal("a fenced-out run wrote")
	}
}
