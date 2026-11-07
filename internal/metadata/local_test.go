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
	"time"

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
	for _, src := range []string{"tags", "folder", "online"} {
		if _, err := e.st.Writer().Exec(`INSERT INTO metadata_values(book_id, field, source, value_json, observed_at) VALUES (?,?,?,?,1)`,
			b.ID, "subtitle", src, `"Old"`); err != nil {
			t.Fatal(err)
		}
	}
	e.local(b.ID)
	if n := e.count(`SELECT count(*) FROM metadata_values WHERE field = 'subtitle' AND source IN ('tags','folder')`); n != 0 {
		t.Fatalf("%d stale rows left", n)
	}
	if n := e.count(`SELECT count(*) FROM metadata_values WHERE field = 'subtitle' AND source = 'online'`); n != 1 {
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

// writeSidecar writes a text file next to the books of a test library.
func (e *env) writeSidecar(rel, text string) {
	e.t.Helper()
	p := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// resolved returns the details the stored rows of a book resolve to, and where each field came from.
func (e *env) resolved(id string) (metadata.Details, map[string]string) {
	e.t.Helper()
	rows, err := e.repo.MetadataValues(bg, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return metadata.Resolve(rows)
}

const sidecarBookDir = "Ann Author/Moon Series/Book 3 - The Tide {Dee Narr}"

const sidecarOPF = `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf"><metadata ` +
	`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">` +
	`<dc:title>Opf Title</dc:title><dc:creator opf:role="aut">Opf Author</dc:creator>` +
	`<dc:creator opf:role="nrt">Opf Narr One</dc:creator><dc:creator opf:role="nrt">Opf Narr Two</dc:creator>` +
	`<dc:publisher>Opf Pub</dc:publisher><dc:date>2021-06-01</dc:date><dc:language>en</dc:language>` +
	`<dc:identifier opf:scheme="ISBN">9781234567897</dc:identifier><dc:subject>Romance</dc:subject>` +
	`<meta name="calibre:series" content="Opf Series"/><meta name="calibre:series_index" content="4"/>` +
	`</metadata></package>`

// newSidecarBook scans a one-file book with the Extractor's five sidecar files and tags.
func newSidecarBook(t *testing.T) (*env, library.Book) {
	e := newEnv(t, mediatest.FakeTools(t))
	e.write(sidecarBookDir+"/tide.m4b", mediatest.FakeFile{Tags: map[string]string{"album": "Tag Title", "artist": "Tag Author", "composer": "Tag Narr"}})
	e.writeSidecar(sidecarBookDir+"/desc.txt", "From desc.txt, an unabridged edition.")
	e.writeSidecar(sidecarBookDir+"/reader.txt", "Reader One\nignored second line")
	e.writeSidecar(sidecarBookDir+"/book.nfo", "Title: The Tide: A Subtitle\nAuthor: Nfo Author\nSeries Name: Nfo Series\nPosition in Series: 7\n"+
		"Abridged: no\nASIN: B0015T963C\nPublisher: Nfo Pub\nBook Description\n=====\nNfo says dramatized.\n")
	e.writeSidecar(sidecarBookDir+"/book.opf", sidecarOPF)
	e.scanAndProbe()
	return e, e.book(sidecarBookDir)
}

// TestSidecarPrecedence ports the Extractor's precedence test: folder < tags < nfo < txt < opf < json.
func TestSidecarPrecedence(t *testing.T) {
	e, b := newSidecarBook(t)
	e.local(b.ID)
	d, from := e.resolved(b.ID)
	if !reflect.DeepEqual(d.Authors, []string{"Opf Author"}) || !reflect.DeepEqual(d.Narrators, []string{"Opf Narr One", "Opf Narr Two"}) {
		t.Fatalf("people = %v / %v", d.Authors, d.Narrators)
	}
	if !reflect.DeepEqual(d.Series, []metadata.SeriesRef{{Name: "Opf Series", Sequence: "4"}}) || d.Year != 2021 || d.Publisher != "Opf Pub" {
		t.Fatalf("series %v year %d publisher %q", d.Series, d.Year, d.Publisher)
	}
	if d.ASIN != "B0015T963C" || d.ISBN != "9781234567897" || from["asin"] != "nfo" {
		t.Fatalf("asin %q (%s) isbn %q", d.ASIN, from["asin"], d.ISBN)
	}
	if from["authors"] != "opf" || from["title"] != "opf" {
		t.Fatalf("from = %v", from)
	}
	got, _ := e.repo.Book(bg, b.ID)
	if got.Title != "Opf Title" || got.Publisher != "Opf Pub" || got.PublishedYear != 2021 {
		t.Fatalf("book = %+v", got)
	}
	people, _ := e.repo.BookPeople(bg, b.ID)
	if !reflect.DeepEqual(names(people, "author"), []string{"Opf Author"}) {
		t.Fatalf("book authors = %v", names(people, "author"))
	}

	e.writeSidecar(sidecarBookDir+"/metadata.json", `{"title":"Json Title","authors":["Json Author"],"narrators":["Json N"],`+
		`"series":["Json Series #9"],"publishedYear":"2015","abridged":true,"description":"From json."}`)
	if !e.local(b.ID) {
		t.Fatal("a new metadata.json changed nothing")
	}
	d, from = e.resolved(b.ID)
	if d.Title != "Json Title" || !reflect.DeepEqual(d.Authors, []string{"Json Author"}) || !reflect.DeepEqual(d.Narrators, []string{"Json N"}) ||
		!reflect.DeepEqual(d.Series, []metadata.SeriesRef{{Name: "Json Series", Sequence: "9"}}) || d.Year != 2015 ||
		d.Abridged == nil || !*d.Abridged || d.Description != "From json." || from["authors"] != "json" {
		t.Fatalf("with json: %+v from %v", d, from)
	}
	got, _ = e.repo.Book(bg, b.ID)
	if got.Title != "Json Title" || !got.Abridged {
		t.Fatalf("book = %+v", got)
	}
	if e.local(b.ID) {
		t.Fatal("repeating Local reported a change")
	}

	// Removing the file drops its rows and the values fall back to the opf.
	if err := os.Remove(filepath.Join(e.root, filepath.FromSlash(sidecarBookDir), "metadata.json")); err != nil {
		t.Fatal(err)
	}
	if !e.local(b.ID) {
		t.Fatal("removing metadata.json changed nothing")
	}
	if n := e.count(`SELECT count(*) FROM metadata_values WHERE book_id = ? AND source = 'json'`, b.ID); n != 0 {
		t.Fatalf("%d json rows left", n)
	}
	if d, _ := e.resolved(b.ID); d.Title != "Opf Title" {
		t.Fatalf("title = %q", d.Title)
	}
}

func TestSidecarUserWins(t *testing.T) {
	e, b := newSidecarBook(t)
	e.writeSidecar(sidecarBookDir+"/metadata.json", `{"title":"Json Title","publisher":"Json Pub"}`)
	if _, err := e.st.Writer().Exec(`INSERT INTO metadata_values(book_id, field, source, value_json, observed_at) VALUES (?,?,?,?,1)`,
		b.ID, "title", "user", `"My Title"`); err != nil {
		t.Fatal(err)
	}
	e.local(b.ID)
	got, _ := e.repo.Book(bg, b.ID)
	if got.Title != "My Title" || got.Publisher != "Json Pub" {
		t.Fatalf("title %q publisher %q", got.Title, got.Publisher)
	}
	if n := e.count(`SELECT count(*) FROM metadata_values WHERE book_id = ? AND source = 'user'`, b.ID); n != 1 {
		t.Fatalf("%d user rows", n)
	}
}

// TestSidecarLooseFile: a single file reads <stem>.opf beside it and ignores other books' sidecars.
func TestSidecarLooseFile(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	e.write("Solo Book.m4b", mediatest.FakeFile{})
	e.writeSidecar("Solo Book.opf", opfWith("Loose Title"))
	e.writeSidecar("cover.opf", opfWith("Wrong Title"))
	e.scanAndProbe()
	b := e.book("Solo Book.m4b")
	e.local(b.ID)
	if got, _ := e.repo.Book(bg, b.ID); got.Title != "Loose Title" {
		t.Fatalf("title = %q", got.Title)
	}
}

func opfWith(title string) string {
	return `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">` +
		`<dc:title>` + title + `</dc:title></metadata></package>`
}

func TestSidecarFinisher(t *testing.T) {
	tools := mediatest.FakeTools(t)
	e := newEnv(t, tools)
	r, err := jobs.New(e.st, jobs.Options{
		Lease: 5 * time.Second, RenewEvery: time.Second, ProgressEvery: 50 * time.Millisecond,
		SweepEvery: time.Second, PollEvery: 10 * time.Millisecond,
		Backoff: func(int) time.Duration { return 10 * time.Millisecond }, Logger: quiet(),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = metadata.New(e.repo, r, quiet())
	if err := e.svc.Register(r); err != nil {
		t.Fatal(err)
	}
	finish := e.svc.Finisher()
	e.write("Ann Author/One/x.m4b", mediatest.FakeFile{})
	e.write("Ann Author/Two/y.m4b", mediatest.FakeFile{})
	e.writeSidecar("Ann Author/One/desc.txt", "first")
	e.scanAndProbe()
	queued := func() int {
		return e.count(`SELECT count(*) FROM jobs WHERE kind = ? AND state = 'queued'`, metadata.Kind)
	}
	// settle runs the details job for both books by hand and clears the queue.
	settle := func() {
		for _, rel := range []string{"Ann Author/One", "Ann Author/Two"} {
			e.local(e.book(rel).ID)
		}
		if _, err := e.st.Writer().Exec(`DELETE FROM jobs`); err != nil {
			t.Fatal(err)
		}
	}

	finish(bg, e.lib.ID) // nothing stored yet: both books are queued
	if n := queued(); n != 2 {
		t.Fatalf("%d jobs with no stored signature, want 2", n)
	}
	finish(bg, e.lib.ID) // active jobs are not queued twice
	if n := queued(); n != 2 {
		t.Fatalf("%d jobs after a second finish, want 2", n)
	}
	settle()
	finish(bg, e.lib.ID)
	if n := queued(); n != 0 {
		t.Fatalf("%d jobs with unchanged sidecars, want 0", n)
	}

	desc := filepath.Join(e.root, "Ann Author", "One", "desc.txt")
	later := time.Now().Add(time.Hour)
	if err := os.WriteFile(desc, []byte("second, longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(desc, later, later); err != nil {
		t.Fatal(err)
	}
	finish(bg, e.lib.ID)
	if n := queued(); n != 1 {
		t.Fatalf("%d jobs after touching desc.txt, want 1", n)
	}
	settle()
	if got, _ := e.repo.Book(bg, e.book("Ann Author/One").ID); got.Description != "second, longer" {
		t.Fatalf("description = %q", got.Description)
	}

	// A new sidecar next to the other book, and a removed one, are noticed too.
	e.writeSidecar("Ann Author/Two/book.nfo", "Title: Two")
	finish(bg, e.lib.ID)
	if n := queued(); n != 1 {
		t.Fatalf("%d jobs after adding an nfo, want 1", n)
	}
	settle()
	if err := os.Remove(desc); err != nil {
		t.Fatal(err)
	}
	finish(bg, e.lib.ID)
	if n := queued(); n != 1 {
		t.Fatalf("%d jobs after removing desc.txt, want 1", n)
	}
}

// TestLocalStartsAtTheContributorFolder (decision 0029): a book under "Added by Ana/Elias Varn/The Book" reads
// author Elias Varn and no series once the folder is a contributor folder; without it the folder name would
// be taken for the author.
func TestLocalStartsAtTheContributorFolder(t *testing.T) {
	for _, registered := range []bool{true, false} {
		e := newEnv(t, mediatest.FakeTools(t))
		e.write("Added by Ana/Elias Varn/The Book/01.mp3", mediatest.FakeFile{Container: "mp3", Codec: "mp3", BitRate: 64000})
		e.scanAndProbe()
		b := e.book("Added by Ana/Elias Varn/The Book")
		if registered {
			folders, _ := e.repo.Folders(bg, e.lib.ID)
			if _, err := e.st.Writer().Exec(`INSERT INTO users(id, username, role, created_at) VALUES ('u1', 'ana', 'owner', 1)`); err != nil {
				t.Fatal(err)
			}
			if _, err := e.st.Writer().Exec(`INSERT INTO contributor_folders(folder_id, user_id, path, created_at) VALUES (?, 'u1', ?, 1)`,
				folders[0].ID, filepath.Join(e.root, "Added by Ana")); err != nil {
				t.Fatal(err)
			}
		}
		e.local(b.ID)
		people, _ := e.repo.BookPeople(bg, b.ID)
		series, _ := e.repo.BookSeries(bg, b.ID)
		authors := names(people, "author")
		if registered {
			if !reflect.DeepEqual(authors, []string{"Elias Varn"}) || len(series) != 0 {
				t.Errorf("registered: authors %v series %+v", authors, series)
			}
		} else if !reflect.DeepEqual(authors, []string{"Added by Ana"}) {
			t.Errorf("unregistered: authors %v (the control must show the folder name leaking)", authors)
		}
	}
}
