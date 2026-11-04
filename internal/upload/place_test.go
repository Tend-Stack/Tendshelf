package upload

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// greatBook is a folder pick of two tracks and a cover, tagged with an author and a title.
func greatBook(t *testing.T) book {
	tags := map[string]string{"album": "Great Title", "artist": "Ann Author"}
	return book{
		"My Book/01.mp3":    mp3(t, tags),
		"My Book/02.mp3":    mp3(t, tags),
		"My Book/cover.jpg": []byte("not really a jpeg"),
	}
}

func TestPlaceLayoutAndLedger(t *testing.T) {
	e := newEnv(t, nil)
	b := greatBook(t)
	v := e.send("My Book", b)
	res := e.mustPlace(v.ID)
	if res.State != StatePlaced || res.ScanJobID == "" {
		t.Fatalf("result = %+v", res)
	}
	want := []string{"Ann Author/", "Ann Author/Great Title/", "Ann Author/Great Title/01.mp3", "Ann Author/Great Title/02.mp3", "Ann Author/Great Title/cover.jpg"}
	if got := tree(t, e.root); !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %v\nwant %v", got, want)
	}
	book := filepath.Join(e.root, "Ann Author", "Great Title")
	for name, data := range map[string][]byte{"01.mp3": b["My Book/01.mp3"], "cover.jpg": b["My Book/cover.jpg"]} {
		got, _ := os.ReadFile(filepath.Join(book, name))
		if !bytes.Equal(got, data) {
			t.Errorf("%s differs from what was uploaded", name)
		}
	}
	u := e.upload(v.ID)
	if u.State != StatePlaced || u.BookPath != book || u.TempPath != "" || u.ScanJobID != res.ScanJobID {
		t.Errorf("upload = %+v", u)
	}
	if _, err := os.Stat(e.svc.stagingDir(v.ID)); !os.IsNotExist(err) {
		t.Errorf("staging not removed: %v", err)
	}
	live := map[string]pathRow{}
	for _, r := range e.ledger(v.ID) {
		if r.State == pathPresent {
			live[r.Path] = r
		}
		if r.State == pathPlanned || r.State == pathStaged {
			t.Errorf("row left %s: %s", r.State, r.Path)
		}
	}
	for _, p := range []string{filepath.Join(e.root, "Ann Author"), book, filepath.Join(book, "01.mp3"), filepath.Join(book, "cover.jpg")} {
		if _, ok := live[p]; !ok {
			t.Errorf("no present row for %s", p)
		}
	}
	f := live[filepath.Join(book, "01.mp3")]
	if f.Kind != kindFile || f.Size != int64(len(b["My Book/01.mp3"])) || f.QuickHash == "" || f.MtimeNs == 0 {
		t.Errorf("file row = %+v", f)
	}
	if f.SHA256 == "" {
		t.Errorf("a copied file records its SHA-256")
	}
	var audits int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'upload.place' AND user_id = ?`, me).Scan(&audits); err != nil || audits != 1 {
		t.Errorf("place audits = %d, %v", audits, err)
	}
	// The import is the existing scan job, scoped to the new folder.
	q := e.enq.of(scan.KindScan)
	if len(q) != 1 {
		t.Fatalf("scan jobs = %v", q)
	}
	var p scan.ScanPayload
	if err := json.Unmarshal(q[0].Payload, &p); err != nil || p.LibraryID != e.lib.ID || p.FolderID != e.folder.ID || p.Subdir != "Ann Author/Great Title" {
		t.Errorf("payload = %+v, %v", p, err)
	}
	// A rerun of the finished job changes nothing and queues nothing more.
	if res2 := e.mustPlace(v.ID); res2.State != StatePlaced || len(e.enq.of(scan.KindScan)) != 1 {
		t.Errorf("rerun = %+v", res2)
	}
}

func TestPlaceCopyRecordsSHA256(t *testing.T) {
	e := newEnv(t, nil)
	v := e.send("My Book", greatBook(t))
	e.mustPlace(v.ID)
	n := 0
	for _, r := range e.ledger(v.ID) {
		if r.Kind == kindFile {
			n++
			if r.SHA256 == "" {
				t.Errorf("copied file %s has no SHA-256", r.Path)
			}
		}
	}
	if n != 3 {
		t.Errorf("%d file rows", n)
	}
}

func TestPlaceSingleFilesUseTheLabel(t *testing.T) {
	e := newEnv(t, nil)
	v := e.send("Some Author - A Title", book{"x.mp3": mp3(t, nil)})
	e.mustPlace(v.ID)
	want := []string{"Some Author/", "Some Author/Some Author - A Title/", "Some Author/Some Author - A Title/x.mp3"}
	got := tree(t, e.root)
	// The label has no "by" or series words, so it is the title; the author is unknown.
	if len(got) != 3 || !strings.HasSuffix(got[2], "/x.mp3") {
		t.Errorf("tree = %v (like %v)", got, want)
	}
}

func TestPlaceDiscFoldersKeepTheirNames(t *testing.T) {
	e := newEnv(t, nil)
	tags := map[string]string{"album": "Big", "artist": "Writer"}
	b := book{"Big/CD1/01.mp3": mp3(t, tags), "Big/CD2/01.mp3": mp3(t, tags), "Big/CD1/../x.mp3": nil}
	delete(b, "Big/CD1/../x.mp3")
	v := e.send("Big", b)
	e.mustPlace(v.ID)
	got := tree(t, e.root)
	want := []string{"Writer/", "Writer/Big/", "Writer/Big/CD1/", "Writer/Big/CD1/01.mp3", "Writer/Big/CD2/", "Writer/Big/CD2/01.mp3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tree = %v", got)
	}
	// The scanner reads it back as one book.
	sc := e.scanner()
	res, err := sc.ScanSubtree(bg, e.lib.ID, e.folder.ID, "Writer/Big", nil)
	if err != nil || res.BooksAdded != 1 || res.FilesAdded != 2 {
		t.Errorf("scan = %+v, %v", res, err)
	}
}

// snapshot records the bytes and mtime of everything under dir.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, _ := d.Info()
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			out[rel] = "dir"
			return nil
		}
		b, _ := os.ReadFile(p)
		out[rel] = string(b) + "@" + fi.ModTime().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPlaceNeverOverwrites(t *testing.T) {
	tags := map[string]string{"album": "Great Title", "artist": "Ann Author"}
	for _, c := range []struct {
		name string
		pre  []string
		at   string // where the book must end up
	}{
		{"file at the name", []string{"Ann Author/Great Title"}, "Ann Author/Great Title (2)"},
		{"non-empty folder", []string{"Ann Author/Great Title/keep.mp3"}, "Ann Author/Great Title (2)"},
		{"empty folder", []string{"Ann Author/Great Title/"}, "Ann Author/Great Title (2)"},
		{"several taken", []string{"Ann Author/Great Title/", "Ann Author/Great Title (2)/"}, "Ann Author/Great Title (3)"},
		{"file as the author", []string{"Ann Author"}, "Ann Author - Great Title"},
		{"audio in the author folder", []string{"Ann Author/loose.m4b"}, "Ann Author - Great Title"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, nil)
			mk(t, e.root, c.pre...)
			before := snapshot(t, e.root)
			v := e.send("Book", book{"01.mp3": mp3(t, tags)})
			e.mustPlace(v.ID)
			after := snapshot(t, e.root)
			for p, s := range before {
				if after[p] != s {
					t.Errorf("%s changed: %q -> %q", p, s, after[p])
				}
			}
			if _, ok := after[filepath.FromSlash(c.at+"/01.mp3")]; !ok {
				t.Errorf("book not at %s: %v", c.at, tree(t, e.root))
			}
			for _, r := range e.ledger(v.ID) {
				for _, p := range c.pre {
					if r.Path == filepath.Join(e.root, filepath.FromSlash(strings.TrimSuffix(p, "/"))) {
						t.Errorf("the ledger claims %s, which existed before", r.Path)
					}
				}
			}
		})
	}
}

func TestPlaceLinkAncestorIsNotUsed(t *testing.T) {
	e := newEnv(t, nil)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(e.root, "Ann Author")); err != nil {
		t.Fatal(err)
	}
	v := e.send("Book", book{"01.mp3": mp3(t, map[string]string{"album": "T", "artist": "Ann Author"})})
	e.mustPlace(v.ID)
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("something was written through the link: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(e.root, "Ann Author - T", "01.mp3")); err != nil {
		t.Errorf("book not placed at the top: %v", tree(t, e.root))
	}
}

func TestPlaceRefusesEncryptedAudio(t *testing.T) {
	e := newEnv(t, nil)
	enc := fakeAudio(t, mediatest.FakeFile{Container: "mp4", Encrypted: true})
	v := e.send("Book", book{"B/01.mp3": mp3(t, map[string]string{"album": "T"}), "B/02.m4a": enc})
	res := e.mustPlace(v.ID)
	if res.State != StateRefused {
		t.Fatalf("state = %s", res.State)
	}
	if got := tree(t, e.root); len(got) != 0 {
		t.Errorf("something reached the library folder: %v", got)
	}
	if _, err := os.Stat(e.svc.stagingDir(v.ID)); !os.IsNotExist(err) {
		t.Errorf("staged bytes kept: %v", err)
	}
	u := e.upload(v.ID)
	if u.State != StateRefused || u.RefusedFile != "B/02.m4a" || u.ErrorCode != "drm" {
		t.Errorf("upload = %+v", u)
	}
	if rows := e.ledger(v.ID); len(rows) != 0 {
		t.Errorf("ledger rows for a refused upload: %v", rows)
	}
	var n int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'upload.refuse'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("refuse audits = %d, %v", n, err)
	}
	if len(e.enq.of(scan.KindScan)) != 0 {
		t.Error("a refused upload queued an import")
	}
}

func TestPlaceNoSpaceKeepsStaging(t *testing.T) {
	e := newEnv(t, nil)
	v := e.send("Book", book{"01.mp3": mp3(t, map[string]string{"album": "T", "artist": "A"})})
	e.svc.free = func(dir string) (uint64, error) { return 10, nil }
	_, err := e.place(v.ID)
	var je *jobs.Error
	if !errors.As(err, &je) || je.Code != "no_space" || !je.Permanent {
		t.Fatalf("err = %v", err)
	}
	if got := tree(t, e.root); len(got) != 0 {
		t.Errorf("library folder not empty: %v", got)
	}
	if u := e.upload(v.ID); u.State != StateFailed || u.ErrorCode != "no_space" {
		t.Errorf("upload = %+v", u)
	}
	if _, err := os.Stat(e.svc.partPath(v.ID, v.Files[0].ID)); err != nil {
		t.Errorf("staged bytes lost: %v", err)
	}
	// Space comes back; Finish restarts it from the staged bytes.
	e.svc.free = func(string) (uint64, error) { return 1 << 40, nil }
	if _, err := e.svc.Finish(bg, me, v.ID); err != nil {
		t.Fatal(err)
	}
	if res := e.mustPlace(v.ID); res.State != StatePlaced {
		t.Errorf("state = %s", res.State)
	}
}

func TestPlaceIncompleteStagingFails(t *testing.T) {
	e := newEnv(t, nil)
	v := e.send("Book", book{"01.mp3": mp3(t, nil)})
	if err := os.Truncate(e.svc.partPath(v.ID, v.Files[0].ID), 5); err != nil {
		t.Fatal(err)
	}
	_, err := e.place(v.ID)
	var je *jobs.Error
	if !errors.As(err, &je) || je.Code != "incomplete" {
		t.Fatalf("err = %v", err)
	}
	if got := tree(t, e.root); len(got) != 0 {
		t.Errorf("library folder not empty: %v", got)
	}
}

func TestPlaceCrashAndRecover(t *testing.T) {
	tags := map[string]string{"album": "Great Title", "artist": "Ann Author"}
	for _, point := range []string{"after_dirs", "after_files", "after_rename"} {
		t.Run(point, func(t *testing.T) {
			e := newEnv(t, nil)
			b := book{"A/01.mp3": mp3(t, tags), "A/02.mp3": mp3(t, tags)}
			v := e.send("A", b)
			boom := errors.New("simulated crash")
			e.svc.crash = func(p string) error {
				if p == point {
					return boom
				}
				return nil
			}
			if _, err := e.place(v.ID); !errors.Is(err, boom) {
				t.Fatalf("first run: %v", err)
			}
			// What a crash leaves must be only what the ledger names.
			temp := filepath.Join(e.root, "Ann Author", tempPrefix+v.ID)
			_, terr := os.Stat(temp)
			switch point {
			case "after_dirs":
				if !os.IsNotExist(terr) {
					t.Errorf("temp exists before it was made")
				}
			case "after_files":
				if terr != nil {
					t.Errorf("temp missing after files: %v", terr)
				}
			case "after_rename":
				if !os.IsNotExist(terr) {
					t.Errorf("temp survived the rename")
				}
			}
			e.svc.crash = func(string) error { return nil }
			res := e.mustPlace(v.ID)
			if res.State != StatePlaced {
				t.Fatalf("state = %s", res.State)
			}
			want := []string{"Ann Author/", "Ann Author/Great Title/", "Ann Author/Great Title/01.mp3", "Ann Author/Great Title/02.mp3"}
			if got := tree(t, e.root); !reflect.DeepEqual(got, want) {
				t.Errorf("tree = %v", got)
			}
			for _, r := range e.ledger(v.ID) {
				if r.State == pathPlanned || r.State == pathStaged {
					t.Errorf("row left %s: %s", r.State, r.Path)
				}
				if r.State == pathPresent && !strings.HasPrefix(r.Path, e.root) {
					t.Errorf("row outside the folder: %s", r.Path)
				}
			}
			if len(e.enq.of(scan.KindScan)) != 1 {
				t.Errorf("scan jobs = %d", len(e.enq.of(scan.KindScan)))
			}
			if u := e.upload(v.ID); u.TempPath != "" || u.State != StatePlaced {
				t.Errorf("upload = %+v", u)
			}
		})
	}
}

// Someone creates the book's name between the layout and the rename: nothing of theirs changes and
// the book takes the next name.
func TestPlaceNameAppearsBeforeRename(t *testing.T) {
	e := newEnv(t, nil)
	v := e.send("A", book{"A/01.mp3": mp3(t, map[string]string{"album": "T", "artist": "X"})})
	intruder := filepath.Join(e.root, "X", "T")
	e.svc.crash = func(p string) error {
		if p == "after_files" {
			mk(t, e.root, "X/T/theirs.mp3")
		}
		return nil
	}
	e.mustPlace(v.ID)
	if b, _ := os.ReadFile(filepath.Join(intruder, "theirs.mp3")); string(b) != "x" {
		t.Error("their file changed")
	}
	if _, err := os.Stat(filepath.Join(e.root, "X", "T (2)", "01.mp3")); err != nil {
		t.Errorf("book not at (2): %v", tree(t, e.root))
	}
	for _, p := range tree(t, e.root) {
		if strings.Contains(p, tempPrefix) {
			t.Errorf("build folder left: %s", p)
		}
	}
	for _, r := range e.ledger(v.ID) {
		if r.Path == intruder && r.State != pathRemoved {
			t.Errorf("ledger claims their folder: %+v", r)
		}
	}
}

func TestPlaceScopedScanLeavesSiblingsAlone(t *testing.T) {
	e := newEnv(t, nil)
	sc := e.scanner()
	// An existing book that is then renamed away on disk: a full scan would mark it missing.
	mediatest.WriteFake(t, filepath.Join(mustMkdir(t, e.root, "Old Author", "Old Book"), "a.m4b"), mediatest.FakeFile{})
	if _, err := sc.ScanLibrary(bg, e.lib.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(e.root, "Old Author", "Old Book"), filepath.Join(t.TempDir(), "moved")); err != nil {
		t.Fatal(err)
	}
	v := e.send("A", book{"A/01.mp3": mp3(t, map[string]string{"album": "T", "artist": "New"})})
	e.mustPlace(v.ID)
	q := e.enq.of(scan.KindScan)
	var p scan.ScanPayload
	if err := json.Unmarshal(q[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	res, err := sc.ScanSubtree(bg, p.LibraryID, p.FolderID, p.Subdir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.BooksAdded != 1 || res.FilesMissing != 0 || res.BooksMissing != 0 {
		t.Errorf("scan = %+v", res)
	}
	var live, missing int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FILTER (WHERE missing_since IS NULL), count(*) FILTER (WHERE missing_since IS NOT NULL) FROM books`).Scan(&live, &missing); err != nil {
		t.Fatal(err)
	}
	if live != 2 || missing != 0 {
		t.Errorf("live %d missing %d, want 2 and 0", live, missing)
	}
}

func mustMkdir(t *testing.T, root string, parts ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBookItems(t *testing.T) {
	rows := func(paths ...string) []fileRow {
		var out []fileRow
		for _, p := range paths {
			out = append(out, fileRow{RelPath: p})
		}
		return out
	}
	rels := func(its []item) []string {
		var out []string
		for _, it := range its {
			out = append(out, it.rel)
		}
		return out
	}
	for name, c := range map[string]struct {
		in   []fileRow
		want []string
	}{
		"folder pick drops its name": {rows("Book/01.mp3", "Book/CD2/02.mp3"), []string{"01.mp3", "CD2/02.mp3"}},
		"loose files stay":           {rows("a.mp3", "b.mp3"), []string{"a.mp3", "b.mp3"}},
		"mixed first segments":       {rows("A/1.mp3", "B/2.mp3"), []string{"A/1.mp3", "B/2.mp3"}},
		"unsafe names":               {rows("a/con.mp3", "a/x:y.mp3"), []string{"con_.mp3", "x-y.mp3"}},
		"case-insensitive clash":     {rows("a/X.mp3", "a/x.mp3"), []string{"X.mp3", "x (2).mp3"}},
		"three-way clash":            {rows("a/x.mp3", "a/X.mp3", "a/x:.mp3"), []string{"X.mp3", "x (2).mp3", "x-.mp3"}},
		"dir case folds":             {rows("a/CD1/1.mp3", "a/cd1/2.mp3"), []string{"CD1/1.mp3", "CD1/2.mp3"}},
		"traversal never survives":   {rows("a/.hidden/..x/f.mp3"), []string{"hidden/x/f.mp3"}},
	} {
		got := rels(bookItems(c.in))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
