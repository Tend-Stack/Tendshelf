package upload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

var everyone = library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "1=1"}}

// dupEnv is an env whose uploads are checked against the library; the person may list everything.
func dupEnv(t *testing.T, place Placement) *env {
	t.Helper()
	return newEnv(t, func(c *Config) {
		c.ScopeFor = func(context.Context, string) (library.Scope, bool, error) { return everyone, true, nil }
		if place != PlaceTop {
			c.MayPlace = func(context.Context, string, string) Placement { return place }
		}
	})
}

var bookTags = map[string]string{"album": "The Long Road", "artist": "Ann Author", "narrator": "Nat Voice"}

// twoParts is a fixed two-file book; pad changes the bytes (so the files differ) but not the tags or length.
func twoParts(t *testing.T, pad int) book {
	t.Helper()
	f := func(pad int) []byte {
		return fakeAudio(t, mediatest.FakeFile{Container: "mp3", Codec: "mp3", Tags: bookTags, PadBytes: pad})
	}
	return book{"B/01.mp3": f(pad), "B/02.mp3": f(pad + 7)}
}

// handCopy puts a book in the library folder by hand and scans it, as the owner did in the incident.
func (e *env) handCopy(dir string, b book, only ...string) string {
	e.t.Helper()
	for p, data := range b {
		if len(only) > 0 && !strings.HasSuffix(p, only[0]) {
			continue
		}
		dst := filepath.Join(e.root, dir, filepath.Base(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	if _, err := e.scanner().ScanLibrary(bg, e.lib.ID, nil); err != nil {
		e.t.Fatal(err)
	}
	var id string
	if err := e.st.Reader().QueryRow(`SELECT id FROM books WHERE root_path LIKE ?`, "%/"+dir).Scan(&id); err != nil {
		e.t.Fatalf("%s: %v", dir, err)
	}
	// The scanner stores files; the details are read by the metadata job, so the test gives the book the
	// ones its tags carry (and the length of its two 6 s files; the probe job is not run here).
	if _, err := e.st.Writer().Exec(`UPDATE books SET title = ?, duration_ms = 12000 WHERE id = ?`, bookTags["album"], id); err != nil {
		e.t.Fatal(err)
	}
	err := e.repo.Update(bg, func(tx *library.Tx) error {
		for role, name := range map[string]string{library.RoleAuthor: bookTags["artist"], library.RoleNarrator: bookTags["narrator"]} {
			pid, err := tx.EnsurePerson(name, "")
			if err != nil {
				return err
			}
			if err := tx.SetBookPeople(id, role, []string{pid}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// held places an upload and expects it to stop as already_in_library.
func (e *env) held(b book) (View, uploadRow) {
	e.t.Helper()
	v := e.send("B", b)
	if _, err := e.place(v.ID); err == nil {
		e.t.Fatal("placement went ahead; want already_in_library")
	}
	u := e.upload(v.ID)
	if u.State != StateFailed || u.ErrorCode != CodeAlreadyInLibrary {
		e.t.Fatalf("state %s code %q, want failed already_in_library", u.State, u.ErrorCode)
	}
	return v, u
}

func (e *env) libraryFiles() []string {
	var out []string
	for _, p := range tree(e.t, e.root) {
		if !strings.HasSuffix(p, "/") {
			out = append(out, p)
		}
	}
	return out
}

func TestHeldWhenHandCopiedFirst(t *testing.T) {
	e := dupEnv(t, PlaceTop)
	b := twoParts(t, 100)
	handID := e.handCopy("Hand Copy", b)
	before := e.libraryFiles()
	v, u := e.held(b)
	if u.DupTier != library.MatchSameFiles || u.DupBookID != handID {
		t.Errorf("dup = %q %q", u.DupTier, u.DupBookID)
	}
	if got := e.libraryFiles(); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Errorf("the library folder changed: %v", got)
	}
	got, err := e.svc.Get(bg, me, v.ID)
	if err != nil || got.Match == nil || got.Match.Tier != "same_files" || got.Match.Title != "The Long Road" ||
		!got.Match.MayAddAnyway || got.Match.Href != "/books/"+u.DupBookID {
		t.Errorf("view match = %+v, %v", got.Match, err)
	}
}

func TestHeldWhenUploadedTwice(t *testing.T) {
	e := dupEnv(t, PlaceTop)
	b := twoParts(t, 100)
	// No scan between the two uploads: the first book is in upload_paths only, not in audio_files yet.
	e.mustPlace(e.send("B", b).ID)
	before := e.libraryFiles()
	v, u := e.held(b)
	if u.DupTier != library.MatchSameFiles {
		t.Errorf("tier = %q", u.DupTier)
	}
	// What the screens get while the first book is not scanned: the person's own upload label, no book id, no link.
	got, err := e.svc.Get(bg, me, v.ID)
	if err != nil || got.Match == nil || got.Match.BookID != "" || got.Match.Href != "" || got.Match.Title != "B" || got.Match.Tier != "same_files" || !got.Match.MayAddAnyway {
		t.Errorf("unscanned own match = %+v, %v", got.Match, err)
	}
	// Once the scan has run, the same held upload names the real book.
	if _, err := e.scanner().ScanLibrary(bg, e.lib.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.svc.Get(bg, me, v.ID); got.Match == nil || got.Match.BookID == "" || got.Match.Href != "/books/"+got.Match.BookID {
		t.Errorf("scanned own match = %+v", got.Match)
	}
	if got := e.libraryFiles(); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Errorf("a second copy reached the library: %v", got)
	}
}

func TestLookAlikeAndSomeFiles(t *testing.T) {
	e := dupEnv(t, PlaceTop)
	e.handCopy("Hand Copy", twoParts(t, 100))
	// Other bytes, same title, author, narrator and length: a look-alike.
	_, u := e.held(twoParts(t, 150))
	if u.DupTier != library.MatchSameDetails {
		t.Errorf("tier = %q, want same_details", u.DupTier)
	}
	// Only one of the two files is in the library: some_files, never same_files.
	e2 := dupEnv(t, PlaceTop)
	full := twoParts(t, 100)
	e2.handCopy("Part", full, "02.mp3")
	_, u = e2.held(full)
	if u.DupTier != library.MatchSomeFiles {
		t.Errorf("tier = %q, want some_files", u.DupTier)
	}
}

func TestNoMatchAcrossLibrariesOrScope(t *testing.T) {
	b := twoParts(t, 100)
	t.Run("another library", func(t *testing.T) {
		e := dupEnv(t, PlaceTop)
		other, err := e.repo.CreateLibrary(bg, "Other", 0)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if _, err := e.repo.AddFolder(bg, other.ID, dir); err != nil {
			t.Fatal(err)
		}
		for p, data := range b {
			if err := os.WriteFile(filepath.Join(dir, filepath.Base(p)), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.scanner().ScanLibrary(bg, other.ID, nil); err != nil {
			t.Fatal(err)
		}
		if res := e.mustPlace(e.send("B", b).ID); res.State != StatePlaced {
			t.Errorf("state = %q, want placed", res.State)
		}
	})
	t.Run("a book the person cannot list", func(t *testing.T) {
		e := dupEnv(t, PlaceTop)
		handID := e.handCopy("Hand Copy", b)
		secret := handID
		e.svc.cfg.ScopeFor = func(context.Context, string) (library.Scope, bool, error) {
			return library.Scope{Books: share.Filter{SQL: "b.id <> ?", Args: []any{secret}}, Libraries: share.Filter{SQL: "1=1"}}, true, nil
		}
		v := e.send("B", twoParts(t, 100))
		res, err := e.place(v.ID)
		if err != nil || res.(PlaceResult).State != StatePlaced {
			t.Fatalf("place = %v, %v; want placed", res, err)
		}
		if u := e.upload(v.ID); u.DupBookID != "" || strings.Contains(u.ErrorCode, "Long Road") {
			t.Errorf("the answer leaks: %+v", u)
		}
	})
	t.Run("a held upload names nothing once the book is not listable", func(t *testing.T) {
		e := dupEnv(t, PlaceTop)
		e.handCopy("Hand Copy", b)
		v, _ := e.held(b)
		e.svc.cfg.ScopeFor = func(context.Context, string) (library.Scope, bool, error) {
			return library.Scope{Books: share.Filter{SQL: "0=1"}, Libraries: share.Filter{SQL: "1=1"}}, true, nil
		}
		if got, _ := e.svc.Get(bg, me, v.ID); got.Match != nil {
			t.Errorf("match = %+v, want none", got.Match)
		}
	})
}

func TestAddAnywayOwner(t *testing.T) {
	e := dupEnv(t, PlaceTop)
	b := twoParts(t, 100)
	e.handCopy("Hand Copy", b)
	v, _ := e.held(b)
	if _, err := e.svc.FinishAnyway(bg, me, v.ID); err != nil {
		t.Fatalf("FinishAnyway: %v", err)
	}
	if res := e.mustPlace(v.ID); res.State != StatePlaced {
		t.Fatalf("state = %q, want placed", res.State)
	}
	u := e.upload(v.ID)
	if !u.AddAnyway || u.DupTier != "" {
		t.Errorf("add_anyway = %v, dup tier %q", u.AddAnyway, u.DupTier)
	}
	if len(e.libraryFiles()) != 4 {
		t.Errorf("files = %v, want the hand copy and the new book", e.libraryFiles())
	}
}

func TestAddAnywayContributor(t *testing.T) {
	e := dupEnv(t, PlaceOwnFolder)
	e.setName("Ana")
	b := twoParts(t, 100)
	e.handCopy("Hand Copy", b)

	v, _ := e.held(b) // an exact copy
	if _, err := e.svc.FinishAnyway(bg, me, v.ID); err == nil {
		t.Fatal("a contributor added an exact copy")
	} else if ue := (*Error)(nil); !errors.As(err, &ue) || ue.Status != 403 {
		t.Errorf("err = %v, want 403", err)
	}
	if got, _ := e.svc.Get(bg, me, v.ID); got.Match == nil || got.Match.MayAddAnyway {
		t.Errorf("match = %+v, want may_add_anyway false", got.Match)
	}

	v2, _ := e.held(twoParts(t, 150)) // a look-alike
	if _, err := e.svc.FinishAnyway(bg, me, v2.ID); err != nil {
		t.Fatalf("look-alike: %v", err)
	}
	if res := e.mustPlace(v2.ID); res.State != StatePlaced {
		t.Errorf("state = %q, want placed", res.State)
	}

	// Setting the flag in the manifest does not skip the check for an exact copy.
	m := manifestOf("B", b)
	m.AddAnyway = true
	v3, err := e.svc.Create(bg, me, e.lib.ID, m)
	if err != nil {
		t.Fatal(err)
	}
	e.sendFrom(v3, b, nil)
	if _, err := e.svc.Finish(bg, me, v3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.place(v3.ID); err == nil || e.upload(v3.ID).ErrorCode != CodeAlreadyInLibrary {
		t.Errorf("an exact copy passed with the manifest flag: %v", err)
	}
}

func TestSharedIntroFileDoesNotHoldAContributor(t *testing.T) {
	e := dupEnv(t, PlaceOwnFolder)
	e.setName("Ana")
	mk := func(album string, pad int) []byte {
		return fakeAudio(t, mediatest.FakeFile{Container: "mp3", Codec: "mp3", PadBytes: pad,
			Tags: map[string]string{"album": album, "artist": "Ann Author", "narrator": "Nat Voice"}})
	}
	intro := mk("Series", 10)
	e.handCopy("Book One", book{"B/intro.mp3": intro, "B/01.mp3": mk("Book One", 3000)})
	// Book two shares only the intro, a small part of its bytes: not a file match, and the tags differ.
	two := book{"B/intro.mp3": intro, "B/02.mp3": mk("Book Two", 3500)}
	if res := e.mustPlace(e.send("B", two).ID); res.State != StatePlaced {
		t.Errorf("state = %q, want placed", res.State)
	}
}

func TestHeldAfterAForgedOrMissingQuick(t *testing.T) {
	e := dupEnv(t, PlaceTop)
	b := twoParts(t, 100)
	e.handCopy("Hand Copy", b)
	for name, quick := range map[string]string{"forged": strings.Repeat("a", 64), "missing": ""} {
		m := manifestOf("B", b)
		for i := range m.Files {
			m.Files[i].Quick = quick
		}
		v, err := e.svc.Create(bg, me, e.lib.ID, m)
		if err != nil {
			t.Fatalf("%s: Create: %v", name, err)
		}
		e.sendFrom(v, b, nil)
		if _, err := e.svc.Finish(bg, me, v.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.place(v.ID); err == nil || e.upload(v.ID).ErrorCode != CodeAlreadyInLibrary {
			t.Errorf("%s: not held after staging (%v)", name, err)
		}
	}
}

func TestHeldUploadStaysListedAndIsPrivate(t *testing.T) {
	e := dupEnv(t, PlaceTop)
	b := twoParts(t, 100)
	e.handCopy("Hand Copy", b)
	v, _ := e.held(b)
	e.svc.cfg.Now = func() time.Time { return time.Now().Add(72 * time.Hour) }
	list, err := e.svc.List(bg, me)
	if err != nil || len(list) != 1 || list[0].ID != v.ID || list[0].Match == nil {
		t.Errorf("a held upload must stay listed for Cancel after 24 h: %+v, %v", list, err)
	}
	// Someone else cannot finish it or read it.
	if _, err := e.svc.FinishAnyway(bg, "us_other", v.ID); err == nil {
		t.Error("FinishAnyway on someone else's upload worked")
	} else if ue := (*Error)(nil); !errors.As(err, &ue) || ue.Status != 404 {
		t.Errorf("err = %v, want 404", err)
	}
}

func TestFailingScopeLookupPlacesNothing(t *testing.T) {
	e := dupEnv(t, PlaceTop)
	b := twoParts(t, 100)
	v := e.send("B", b)
	e.svc.cfg.ScopeFor = func(context.Context, string) (library.Scope, bool, error) {
		return library.Scope{}, false, errors.New("database is busy")
	}
	if _, err := e.place(v.ID); err == nil {
		t.Fatal("placement went ahead without a scope")
	}
	if u := e.upload(v.ID); u.State != StateFailed || u.ErrorCode == CodeAlreadyInLibrary {
		t.Errorf("state %s code %q, want failed (retryable)", u.State, u.ErrorCode)
	}
	if files := e.libraryFiles(); len(files) != 0 {
		t.Errorf("files placed: %v", files)
	}
}

func TestCancelDeletesStagedBytes(t *testing.T) {
	e := dupEnv(t, PlaceTop)
	b := twoParts(t, 100)
	e.handCopy("Hand Copy", b)
	v, _ := e.held(b)
	if _, err := os.Stat(e.svc.stagingDir(v.ID)); err != nil {
		t.Fatalf("a held upload keeps its staged bytes: %v", err)
	}
	if err := e.svc.Cancel(bg, me, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.svc.stagingDir(v.ID)); !os.IsNotExist(err) {
		t.Errorf("staged bytes remain: %v", err)
	}
}

func TestManifestQuickCheck(t *testing.T) {
	e := dupEnv(t, PlaceTop)
	b := twoParts(t, 100)
	e.handCopy("Hand Copy", b)
	quick := func(data []byte) string {
		p := filepath.Join(t.TempDir(), "q")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		h, _, err := media.QuickHash(p)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	withQuick := func() Manifest {
		m := manifestOf("B", b)
		for i := range m.Files {
			m.Files[i].Quick = quick(b[m.Files[i].Path])
		}
		return m
	}
	_, err := e.svc.Create(bg, me, e.lib.ID, withQuick())
	var ue *Error
	if !errors.As(err, &ue) || ue.Status != 409 || ue.Code != CodeAlreadyInLibrary || ue.Match == nil || ue.Match.Tier != "same_files" || !ue.Match.MayAddAnyway {
		t.Fatalf("Create = %v, want 409 already_in_library with a match", err)
	}
	m := withQuick()
	m.AddAnyway = true
	if _, err := e.svc.Create(bg, me, e.lib.ID, m); err != nil {
		t.Errorf("owner add anyway: %v", err)
	}
	bad := withQuick()
	bad.Files[0].Quick = "xyz"
	if _, err := e.svc.Create(bg, me, e.lib.ID, bad); !errors.As(err, &ue) || ue.Status != 422 || ue.Code != "bad_checksum" {
		t.Errorf("bad quick = %v", err)
	}
	// A .zip hides its files: no check before the bytes are sent.
	zm := Manifest{Label: "Z", Files: []FileIn{{Path: "Z.zip", Size: 100, ModifiedMs: 1, Quick: quick(b["B/01.mp3"])}}}
	if _, err := e.svc.Create(bg, me, e.lib.ID, zm); err != nil {
		t.Errorf("zip: %v", err)
	}
	// A contributor is told no on an exact copy, even with the flag.
	c := dupEnv(t, PlaceOwnFolder)
	c.setName("Ana")
	c.handCopy("Hand Copy", b)
	cm := withQuick()
	cm.AddAnyway = true
	if _, err := c.svc.Create(bg, me, c.lib.ID, cm); !errors.As(err, &ue) || ue.Status != 403 || ue.Code != "not_allowed" {
		t.Errorf("contributor = %v, want 403 not_allowed", err)
	}
}
