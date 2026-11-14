package web

import (
	"context"
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// rshChapters gives the lab's audiobook chapters named like the ebook's (document chapters 2 to 4 are
// "Chapter 1" to "Chapter 3"), after an opening chapter that maps to nothing.
func rshChapters(t *testing.T, l *rdrLab) {
	t.Helper()
	if _, err := l.a.st.Writer().Exec(`UPDATE audio_files SET duration_ms = ? WHERE book_id = ?`, sweepHourMs, l.book.ID); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		idx        int
		start, end int64
		title      string
	}{
		{0, 0, 60000, "Opening Credits"}, {1, 60000, 600000, "Chapter 1"},
		{2, 600000, 1200000, "Chapter 2"}, {3, 1200000, sweepHourMs, "Chapter 3"},
	}
	for _, r := range rows {
		if _, err := l.a.st.Writer().Exec(`INSERT INTO chapters(book_id, idx, start_ms, end_ms, title, source) VALUES (?, ?, ?, ?, ?, 'file')`,
			l.book.ID, r.idx, r.start, r.end, r.title); err != nil {
			t.Fatal(err)
		}
	}
}

func rshListened(t *testing.T, l *rdrLab, ms, at int64) {
	t.Helper()
	if _, err := l.a.srv.progress.Save(context.Background(), progress.Update{UserID: l.ownerID, BookID: l.book.ID,
		PositionMs: ms, DurationMs: sweepHourMs, ClientUpdatedAt: at, DeviceID: "dev-test-1"}); err != nil {
		t.Fatal(err)
	}
}

func TestReaderSheetClosesInPlace(t *testing.T) {
	l := rdrSeed(t)
	body := l.get(l.owner, l.readPath("ch=3&sheet=contents"), false).Body.String()
	if n := strings.Count(body, "data-ts-close "); n != 2 {
		t.Errorf("the scrim and the close button carry data-ts-close, found %d", n)
	}
	if !strings.Contains(body, `href="/books/`+l.book.ID+`/read?ch=3"`) {
		t.Error("close goes back to the chapter being read")
	}
	if n := strings.Count(body, "data-ts-sheet-go"); n != 4 {
		t.Errorf("each of the 4 contents links carries data-ts-sheet-go, found %d", n)
	}
	// The offline shell keeps its bare close address and closes by navigation.
	off := l.get(l.owner, "/offline/books/"+l.book.ID+"/read?sheet=contents", false).Body.String()
	if strings.Contains(off, "data-ts-close ") || !strings.Contains(off, `href="/books/`+l.book.ID+`/read"`) {
		t.Error("the offline sheet closes by its plain address")
	}
}

func TestReaderMovedFlag(t *testing.T) {
	l := rdrSeed(t)
	sid, _ := l.sidIn(0)
	for q, want := range map[string]bool{
		"ch=2": true, "at=" + sid: true, "bp=3000": true,
		"": false, "ch=2&sheet=contents": false, "edition=" + l.ed.ID: false,
	} {
		body := l.get(l.owner, l.readPath(q), false).Body.String()
		if got := strings.Contains(body, `data-moved="1"`); got != want {
			t.Errorf("%q: moved %v, want %v", q, got, want)
		}
	}
}

func TestReaderListenSheet(t *testing.T) {
	l := rdrSeed(t)
	rshChapters(t, l)
	path := l.readPath("ch=3&sheet=listen")
	rows := func(body string) []string {
		var out []string
		for _, part := range strings.Split(body, `data-ts-play-at="`)[1:] {
			out = append(out, part[:strings.Index(part, `"`)])
		}
		return out
	}
	// No listening place: reading chapter 2 (document chapter 3) offers its audio chapter, then the start.
	body := l.get(l.owner, path, false).Body.String()
	if got := rows(body); strings.Join(got, ",") != "600000,0" {
		t.Errorf("without a place: %v", got)
	}
	sheet := body[strings.Index(body, `id="format-choice-title"`):]
	if !strings.Contains(sheet, html.EscapeString(listenFromReading)) || !strings.Contains(sheet, listenFromStart) || strings.Contains(sheet, listenContinue) {
		t.Error("labels without a place")
	}
	if !strings.Contains(body, `data-ts-sheet-go`) || !strings.Contains(body, "Chapter 2") {
		t.Error("rows carry the go marker and the chapter title")
	}
	sid, _ := l.sidIn(0)
	// A listening place in Chapter 3, older than a reading place: the reading row first, then continue, then start.
	rshListened(t, l, 1300000, 5)
	if _, _, err := l.a.srv.progress.SaveReading(context.Background(), progress.ReadingSave{UserID: l.ownerID, BookID: l.book.ID,
		EditionID: l.ed.ID, ChapterIdx: rdrCh, SID: sid, CharPos: 5, PercentBP: 100, ClientUpdatedAt: 10, DeviceID: "dev-test-1"}); err != nil {
		t.Fatal(err)
	}
	body = l.get(l.owner, path, false).Body.String()
	if got := rows(body); strings.Join(got, ",") != "600000,1300000,0" {
		t.Errorf("with a place: %v", got)
	}
	// The same audio chapter as the listening place: the reading row is dropped.
	rshListened(t, l, 700000, 20)
	body = l.get(l.owner, path, false).Body.String()
	if got := rows(body); strings.Join(got, ",") != "700000,0" {
		t.Errorf("same chapter: %v", got)
	}
	// The speaker asks only when there is something to choose.
	page := l.get(l.owner, l.readPath("ch=3"), false).Body.String()
	if !strings.Contains(page, "data-ts-choose=") {
		t.Error("the speaker should open the choice")
	}
	// A member who may read but not listen, and a member without access, get nothing.
	for name, ck := range map[string]*http.Cookie{"no read right": l.noRead} {
		if rec := l.get(ck, path, false); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

func TestReaderSpeakerStraight(t *testing.T) {
	l := rdrSeed(t)
	// Audio chapters that map to nothing and no place: no choice, the speaker stays a plain play link.
	if _, err := l.a.st.Writer().Exec(`UPDATE audio_files SET duration_ms = ? WHERE book_id = ?`, sweepHourMs, l.book.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.a.st.Writer().Exec(`INSERT INTO chapters(book_id, idx, start_ms, end_ms, title, source) VALUES (?, 0, 0, 1000, 'Track 01', 'file')`, l.book.ID); err != nil {
		t.Fatal(err)
	}
	page := l.get(l.owner, l.readPath("ch=3"), false).Body.String()
	if !strings.Contains(page, "data-ts-play=") || strings.Contains(page, "data-ts-choose") {
		t.Error("a speaker with nothing to choose plays straight")
	}
	if rec := l.get(l.owner, l.readPath("ch=3&sheet=listen"), false); rec.Code != http.StatusOK {
		t.Errorf("the sheet itself still answers: %d", rec.Code)
	}
}
