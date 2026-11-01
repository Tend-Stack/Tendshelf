package library_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

func put(t *testing.T, r *library.Repo, f *library.AudioFile) {
	t.Helper()
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.PutAudioFile(f) }); err != nil {
		t.Fatal(err)
	}
}

func TestProbeErrorRoundTrip(t *testing.T) {
	r, st := newRepo(t)
	lib, f := fixture(t, r)
	b := addBook(t, r, lib, f, "/audio/b")
	a := file(b.ID, "/audio/b/1.mp3", 0)
	put(t, r, a)
	got, err := r.AudioFile(ctx, a.ID)
	if err != nil || got.ProbeError != "" || got.ProbeJSON != nil {
		t.Fatalf("fresh file = %+v, %v", got, err)
	}
	a.ProbeError = "not_audio"
	put(t, r, a)
	got, _ = r.AudioFile(ctx, a.ID)
	if got.ProbeError != "not_audio" {
		t.Fatalf("ProbeError = %q", got.ProbeError)
	}
	a.ProbeError = ""
	put(t, r, a)
	if got, _ = r.AudioFile(ctx, a.ID); got.ProbeError != "" {
		t.Fatalf("cleared ProbeError = %q", got.ProbeError)
	}
	var null int
	if err := st.Reader().QueryRow(`SELECT probe_error IS NULL FROM audio_files WHERE id = ?`, a.ID).Scan(&null); err != nil || null != 1 {
		t.Fatalf("probe_error is NULL = %d, %v", null, err)
	}
	if _, err := r.AudioFile(ctx, "af_nope"); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("missing file err = %v", err)
	}
}

func TestUnsettledAudioFileIDs(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	b := addBook(t, r, lib, f, "/audio/b")
	other, err := r.CreateLibrary(ctx, "Other", 0)
	if err != nil {
		t.Fatal(err)
	}
	of, err := r.AddFolder(ctx, other.ID, "/other")
	if err != nil {
		t.Fatal(err)
	}
	ob := addBook(t, r, other, of, "/other/b")

	probed := file(b.ID, "/audio/b/1.mp3", 0)
	probed.ProbeJSON = []byte(`{"v":1}`)
	failed := file(b.ID, "/audio/b/2.mp3", 1)
	failed.ProbeError = "not_audio"
	missing := file(b.ID, "/audio/b/3.mp3", 2)
	missing.MissingSince = 5
	late := file(b.ID, "/audio/b/z.mp3", 3)
	early := file(b.ID, "/audio/b/a.mp3", 4)
	foreign := file(ob.ID, "/other/b/1.mp3", 0)
	for _, a := range []*library.AudioFile{probed, failed, missing, late, early, foreign} {
		put(t, r, a)
	}
	got, err := r.UnsettledAudioFileIDs(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{early.ID, late.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unsettled = %v, want %v", got, want)
	}
}

func TestRefusedBooks(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	for _, c := range []struct{ root, reason string }{
		{"/audio/z", library.RefusedDRM}, {"/audio/a", library.RefusedDamaged}, {"/audio/ok", ""},
	} {
		b := library.Book{LibraryID: lib.ID, FolderID: f.ID, RootPath: c.root, Title: "T", RefusedReason: c.reason}
		if err := r.Update(ctx, func(tx *library.Tx) error { return tx.InsertBook(&b) }); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.RefusedBooks(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].RootPath != "/audio/a" || got[1].RootPath != "/audio/z" || got[1].RefusedReason != "drm" {
		t.Fatalf("refused = %+v", got)
	}
}

func TestUpdateGuarded(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	boom := errors.New("boom")
	called := false
	b := library.Book{LibraryID: lib.ID, FolderID: f.ID, RootPath: "/audio/g", Title: "T"}

	err := r.UpdateGuarded(ctx, func(context.Context, *sql.Tx) error { return boom }, func(tx *library.Tx) error {
		called = true
		return tx.InsertBook(&b)
	})
	if !errors.Is(err, boom) || called {
		t.Fatalf("guard failure: err=%v called=%v", err, called)
	}
	if _, err := r.BookByRootPath(ctx, "/audio/g"); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("book written despite the guard: %v", err)
	}
	// A guard that passes runs before fn and in the same transaction.
	var order []string
	err = r.UpdateGuarded(ctx, func(context.Context, *sql.Tx) error { order = append(order, "guard"); return nil },
		func(tx *library.Tx) error { order = append(order, "fn"); return tx.InsertBook(&b) })
	if err != nil || !reflect.DeepEqual(order, []string{"guard", "fn"}) {
		t.Fatalf("err=%v order=%v", err, order)
	}
	// fn failing rolls back what the guard did too.
	err = r.UpdateGuarded(ctx, func(_ context.Context, tx *sql.Tx) error {
		_, e := tx.Exec(`UPDATE libraries SET name='changed' WHERE id=?`, lib.ID)
		return e
	}, func(*library.Tx) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if got, _ := r.Library(ctx, lib.ID); got.Name != "Books" {
		t.Fatalf("guard write survived rollback: %q", got.Name)
	}
	if err := r.UpdateGuarded(ctx, nil, func(*library.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestTxReads(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	b1 := addBook(t, r, lib, f, "/audio/b1")
	b2 := addBook(t, r, lib, f, "/audio/b2")
	empty := addBook(t, r, lib, f, "/audio/empty")
	a2 := file(b1.ID, "/audio/b1/2.mp3", 1)
	a1 := file(b1.ID, "/audio/b1/1.mp3", 0)
	a3 := file(b2.ID, "/audio/b2/1.mp3", 0)
	a3.MissingSince = 9
	for _, a := range []*library.AudioFile{a2, a1, a3} {
		put(t, r, a)
	}
	err := r.Update(ctx, func(tx *library.Tx) error {
		if err := tx.ReplaceChapters(b1.ID, []library.Chapter{{StartMs: 0, EndMs: 5, Title: "x", Source: library.ChapterFile}}); err != nil {
			return err
		}
		// Reads see the transaction's own writes.
		if _, err := tx.Book(b1.ID); err != nil {
			return err
		}
		if _, err := tx.Book("bk_nope"); !errors.Is(err, library.ErrNotFound) {
			t.Errorf("Book missing err = %v", err)
		}
		bs, err := tx.BooksInLibrary(lib.ID)
		if err != nil || len(bs) != 3 || bs[0].RootPath != "/audio/b1" {
			t.Errorf("BooksInLibrary = %v, %v", bs, err)
		}
		fs, err := tx.AudioFiles(b1.ID)
		if err != nil || len(fs) != 2 || fs[0].ID != a1.ID || fs[1].ID != a2.ID {
			t.Errorf("AudioFiles = %v, %v", fs, err)
		}
		all, err := tx.AudioFilesInLibrary(lib.ID)
		if err != nil || len(all) != 3 {
			t.Errorf("AudioFilesInLibrary = %d, %v", len(all), err)
		}
		for i := 1; i < len(all); i++ {
			p, q := all[i-1], all[i]
			if p.BookID > q.BookID || (p.BookID == q.BookID && p.Idx > q.Idx) {
				t.Errorf("AudioFilesInLibrary out of order at %d", i)
			}
		}
		one, err := tx.AudioFile(a3.ID)
		if err != nil || one.MissingSince != 9 {
			t.Errorf("AudioFile = %+v, %v", one, err)
		}
		chs, err := tx.Chapters(b1.ID)
		if err != nil || len(chs) != 1 || chs[0].Title != "x" {
			t.Errorf("Chapters = %v, %v", chs, err)
		}
		// DeleteEmptyBook only deletes a book without file rows (a missing file still counts).
		for id, want := range map[string]bool{b1.ID: false, b2.ID: false, empty.ID: true} {
			got, err := tx.DeleteEmptyBook(id)
			if err != nil || got != want {
				t.Errorf("DeleteEmptyBook(%s) = %v, %v; want %v", id, got, err, want)
			}
		}
		if got, err := tx.DeleteEmptyBook(empty.ID); err != nil || got {
			t.Errorf("second DeleteEmptyBook = %v, %v", got, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Book(ctx, empty.ID); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("empty book survived: %v", err)
	}
}
