package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// Finished is one state per book across formats: marking it anywhere marks both formats the person may
// use; "Mark as not started" clears both; the continue lists read either mark.

func finBothMark(t *testing.T, l *fmt2Lab, ck *http.Cookie, path string, finished string) int {
	t.Helper()
	rec := l.a.do(http.MethodPost, path, url.Values{"finished": {finished}, "_csrf": {l.a.tokenFor(t, ck)}},
		func(r *http.Request) { r.Header.Set("HX-Request", "true") }, ck)
	return rec.Code
}

func finBothState(t *testing.T, l *fmt2Lab, ck *http.Cookie, bookID string) (listen, read bool) {
	t.Helper()
	ctx, uid := context.Background(), fmt2Uid(t, l, ck)
	p, err := l.a.srv.progress.Get(ctx, uid, bookID)
	if err != nil && !errors.Is(err, progress.ErrNotFound) {
		t.Fatal(err)
	}
	rp, _, err := l.a.srv.progress.Reading(ctx, uid, bookID)
	if err != nil {
		t.Fatal(err)
	}
	return p.Finished(), rp.Finished()
}

func finBothContinue(t *testing.T, l *fmt2Lab, ck *http.Cookie) []string {
	t.Helper()
	list, err := l.a.lib.ContinueBooks(context.Background(), fmt2All, fmt2Uid(t, l, ck), 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, x := range list {
		ids = append(ids, x.Book.ID)
	}
	return ids
}

func TestFinishedBothFromBookPage(t *testing.T) {
	l := fmt2Seed(t)
	fmt2Listened(t, l, l.owner, l.book.ID, 5000)
	fmt2Reading(t, l, l.owner, l.book.ID, 4200, 6000)
	if got := finBothContinue(t, l, l.owner); len(got) != 1 {
		t.Fatalf("under way: %v", got)
	}
	path := "/hx/books/" + l.book.ID + "/finished"
	if c := finBothMark(t, l, l.owner, path, "1"); c != http.StatusNoContent {
		t.Fatalf("mark: %d", c)
	}
	if li, re := finBothState(t, l, l.owner, l.book.ID); !li || !re {
		t.Errorf("finished: listen %v read %v", li, re)
	}
	if got := finBothContinue(t, l, l.owner); len(got) != 0 {
		t.Errorf("a finished book is still continued: %v", got)
	}
	if body := l.get(l.owner, "/", false).Body.String(); strings.Contains(body, "Continue reading") || strings.Contains(body, "Continue listening") {
		t.Error("Home still offers the finished book")
	}
	// Undo clears both.
	if c := finBothMark(t, l, l.owner, path, "0"); c != http.StatusNoContent {
		t.Fatalf("undo: %d", c)
	}
	if li, re := finBothState(t, l, l.owner, l.book.ID); li || re {
		t.Errorf("not started: listen %v read %v", li, re)
	}
}

func TestFinishedBothFromReader(t *testing.T) {
	l := fmt2Seed(t)
	fmt2Listened(t, l, l.owner, l.book.ID, 5000)
	fmt2Reading(t, l, l.owner, l.book.ID, 4200, 6000)
	path := "/reading/" + l.book.ID + "/finished"
	if c := finBothMark(t, l, l.owner, path, "1"); c != http.StatusOK {
		t.Fatalf("mark: %d", c)
	}
	if li, re := finBothState(t, l, l.owner, l.book.ID); !li || !re {
		t.Errorf("finished: listen %v read %v", li, re)
	}
	if got := finBothContinue(t, l, l.owner); len(got) != 0 {
		t.Errorf("continued: %v", got)
	}
	finBothMark(t, l, l.owner, path, "0")
	if li, re := finBothState(t, l, l.owner, l.book.ID); li || re {
		t.Errorf("not started: listen %v read %v", li, re)
	}
}

// Existing data: finished in one format only hides the book from every continue list.
func TestFinishedBothEitherFormatHides(t *testing.T) {
	for _, audioDone := range []bool{true, false} {
		l := fmt2Seed(t)
		ctx, uid := context.Background(), fmt2Uid(t, l, l.owner)
		fmt2Listened(t, l, l.owner, l.book.ID, 5000)
		fmt2Reading(t, l, l.owner, l.book.ID, 4200, 6000)
		var err error
		if audioDone {
			_, err = l.a.srv.progress.SetFinished(ctx, uid, l.book.ID, true, sweepHourMs)
		} else {
			_, err = l.a.srv.progress.SetReadingFinished(ctx, uid, l.book.ID, true)
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := finBothContinue(t, l, l.owner); len(got) != 0 {
			t.Errorf("audioDone=%v: continued %v", audioDone, got)
		}
		if got, err := l.a.lib.ContinueListening(ctx, fmt2All, uid, 5); err != nil || len(got) != 0 {
			t.Errorf("audioDone=%v: ContinueListening %v %v", audioDone, got, err)
		}
	}
}

func TestFinishedBothTextOnlyBook(t *testing.T) {
	l := fmt2Seed(t)
	fmt2Reading(t, l, l.owner, l.tbook.ID, 4200, 5000)
	path := "/hx/books/" + l.tbook.ID + "/finished"
	if c := finBothMark(t, l, l.owner, path, "1"); c != http.StatusNoContent {
		t.Fatalf("mark: %d", c)
	}
	li, re := finBothState(t, l, l.owner, l.tbook.ID)
	if li || !re {
		t.Errorf("text-only: listen %v read %v", li, re)
	}
	if _, err := l.a.srv.progress.Get(context.Background(), fmt2Uid(t, l, l.owner), l.tbook.ID); !errors.Is(err, progress.ErrNotFound) {
		t.Errorf("a listening row was made for a text book: %v", err)
	}
	if got := finBothContinue(t, l, l.owner); len(got) != 0 {
		t.Errorf("continued: %v", got)
	}
}

func TestFinishedBothAudioOnlyBook(t *testing.T) {
	l := fmt2Seed(t)
	folder, err := l.a.lib.Folder(context.Background(), l.book.FolderID)
	if err != nil {
		t.Fatal(err)
	}
	solo := librarytest.AddBook(t, l.a.lib, folder, librarytest.BookSpec{Title: "Solo Voice", Authors: []string{"Imogen Textauthor"}, DurationMs: sweepHourMs, AddedAt: 6, Files: 1})
	fmt2Listened(t, l, l.owner, solo.ID, 5000)
	if c := finBothMark(t, l, l.owner, "/hx/books/"+solo.ID+"/finished", "1"); c != http.StatusNoContent {
		t.Fatalf("mark: %d", c)
	}
	li, re := finBothState(t, l, l.owner, solo.ID)
	if !li || re {
		t.Errorf("audio-only: listen %v read %v", li, re)
	}
	if _, found, _ := l.a.srv.progress.Reading(context.Background(), fmt2Uid(t, l, l.owner), solo.ID); found {
		t.Error("a reading row was made for an audio-only book")
	}
	finBothMark(t, l, l.owner, "/hx/books/"+solo.ID+"/finished", "0")
	if li, _ = finBothState(t, l, l.owner, solo.ID); li {
		t.Error("undo did not clear listening")
	}
}

// A person with listen but not read changes listening only.
func TestFinishedBothListenWithoutRead(t *testing.T) {
	l := fmt2Seed(t)
	ctx, uid := context.Background(), fmt2Uid(t, l, l.noRead)
	fmt2Listened(t, l, l.noRead, l.book.ID, 5000)
	if _, _, err := l.a.srv.progress.SaveReading(ctx, progress.ReadingSave{UserID: uid, BookID: l.book.ID, ChapterIdx: 2, CharPos: 5,
		PercentBP: 3000, ClientUpdatedAt: 6000, DeviceID: "device-test-0001"}); err != nil {
		t.Fatal(err)
	}
	if c := finBothMark(t, l, l.noRead, "/hx/books/"+l.book.ID+"/finished", "1"); c != http.StatusNoContent {
		t.Fatalf("mark: %d", c)
	}
	li, re := finBothState(t, l, l.noRead, l.book.ID)
	if !li || re {
		t.Errorf("listen-only person: listen %v read %v", li, re)
	}
	if c := finBothMark(t, l, l.noRead, "/reading/"+l.book.ID+"/finished", "1"); c != http.StatusNotFound {
		t.Errorf("the reader route for a person without the right: %d", c)
	}
}

func TestFinishedBothNeverAnotherPerson(t *testing.T) {
	l := fmt2Seed(t)
	fmt2Listened(t, l, l.reader, l.book.ID, 5000)
	fmt2Reading(t, l, l.reader, l.book.ID, 4200, 6000)
	finBothMark(t, l, l.owner, "/hx/books/"+l.book.ID+"/finished", "1")
	if li, re := finBothState(t, l, l.reader, l.book.ID); li || re {
		t.Errorf("the owner's mark reached the reader: listen %v read %v", li, re)
	}
	if got := finBothContinue(t, l, l.reader); len(got) != 1 {
		t.Errorf("the reader lost the book: %v", got)
	}
}
