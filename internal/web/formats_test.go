package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// B2 tests: books in two formats (decision 0018). The lab is the Text tab lab with an audiobook that has an ebook
// (l.book, ready file af) and a text book with one ebook (tbook, ready file tf).

var (
	fmt2All  = library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "1=1"}}
	fmt2None = library.Scope{Books: share.Filter{SQL: "0=1"}, Libraries: share.Filter{SQL: "0=1"}}
)

type fmt2Lab struct {
	*textLab
	tbook library.Book
	af    string // ebook file of the audiobook
	tf    string // ebook file of the text book
}

func fmt2Seed(t *testing.T) *fmt2Lab {
	t.Helper()
	l := &fmt2Lab{textLab: newTextLab(t)}
	l.run(t)
	ctx := context.Background()
	// The seeded audio files have no length; give them one so the book has a play button.
	if _, err := l.a.st.Writer().Exec(`UPDATE audio_files SET duration_ms = ?, container = 'mp3', codec = 'mp3' WHERE book_id = ?`, sweepHourMs, l.book.ID); err != nil {
		t.Fatal(err)
	}
	id, err := l.a.lib.CreateTextBook(ctx, library.TextBookIn{LibraryID: l.book.LibraryID, FolderID: l.book.FolderID,
		RootPath: l.a.dir + "/prose.epub", Title: "Plain Prose"})
	if err != nil {
		t.Fatal(err)
	}
	if l.tbook, err = l.a.lib.Book(ctx, id); err != nil {
		t.Fatal(err)
	}
	l.af = l.addReady(t, "audio-side.epub", "Textword Lantern", 3).ID
	f, err := l.svc.AddUpload(ctx, l.tbook, l.ownerID, "prose.epub", bytes.NewReader(textEPUB(t, "Plain Prose", 3)))
	if err != nil {
		t.Fatal(err)
	}
	l.tf = l.waitState(t, f.ID, "ready").ID
	return l
}

// fmt2Uid is the id of the person behind a session cookie.
func fmt2Uid(t *testing.T, l *fmt2Lab, ck *http.Cookie) string {
	t.Helper()
	return sweepUserID(t, l.a, ck)
}

func fmt2Reading(t *testing.T, l *fmt2Lab, ck *http.Cookie, bookID string, bp int, at int64) {
	t.Helper()
	_, _, err := l.a.srv.progress.SaveReading(context.Background(), progress.ReadingSave{UserID: fmt2Uid(t, l, ck), BookID: bookID,
		ChapterIdx: 2, CharPos: 100, PercentBP: bp, ClientUpdatedAt: at, DeviceID: "device-test-0001"})
	if err != nil {
		t.Fatal(err)
	}
}

func fmt2Listened(t *testing.T, l *fmt2Lab, ck *http.Cookie, bookID string, at int64) {
	t.Helper()
	_, err := l.a.srv.progress.Save(context.Background(), progress.Update{UserID: fmt2Uid(t, l, ck), BookID: bookID,
		PositionMs: 600_000, DurationMs: sweepHourMs, ClientUpdatedAt: at, DeviceID: "device-test-0001"})
	if err != nil {
		t.Fatal(err)
	}
}

// fmt2Region cuts the #book-play region out of a page.
func fmt2Region(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `id="book-play"`)
	if i < 0 {
		t.Fatalf("no #book-play region in %.200s", body)
	}
	body = body[i:]
	if j := strings.Index(body, `id="book-panel"`); j > 0 {
		body = body[:j]
	}
	return body
}

func TestFormatsResumeRule(t *testing.T) {
	older, newer := int64(1000), int64(2000)
	p := func(at int64) *progress.Progress { return &progress.Progress{ClientUpdatedAt: at} }
	r := func(at int64) *progress.ReadingPosition { return &progress.ReadingPosition{ClientUpdatedAt: at} }
	for _, c := range []struct {
		name               string
		p                  *progress.Progress
		rp                 *progress.ReadingPosition
		mayListen, mayRead bool
		want               string
	}{
		{"no rows, both rights", nil, nil, true, true, formatAudio},
		{"no rows, read only", nil, nil, false, true, formatText},
		{"listened last", p(newer), r(older), true, true, formatAudio},
		{"read last", p(older), r(newer), true, true, formatText},
		{"only a reading place", nil, r(older), true, true, formatText},
		{"only a listening place", p(older), nil, true, true, formatAudio},
		{"read last but no ReadText", p(older), r(newer), true, false, formatAudio},
		{"listened last but no ListenBook", p(newer), r(older), false, true, formatText},
		{"neither right", nil, nil, false, false, ""},
	} {
		if got := lastFormat(c.p, c.rp, c.mayListen, c.mayRead); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// The primary button's words.
	for _, c := range []struct {
		rp    *progress.ReadingPosition
		label string
	}{
		{nil, readStart},
		{&progress.ReadingPosition{PercentBP: 4200, ChapterIdx: 3}, readContinue},
		{&progress.ReadingPosition{PercentBP: 10000, FinishedAt: 5}, readAgain},
	} {
		if label, _ := readState(c.rp); label != c.label {
			t.Errorf("%+v: %q, want %q", c.rp, label, c.label)
		}
	}
	if _, where := readState(&progress.ReadingPosition{PercentBP: 4250, ChapterIdx: 3}); where != "Chapter 3 · 42 %" {
		t.Errorf("where %q", where)
	}
}

func TestFormatsMixedPage(t *testing.T) {
	l := fmt2Seed(t)
	page := func(ck *http.Cookie) string {
		rec := l.get(ck, "/books/"+l.book.ID, false)
		if rec.Code != http.StatusOK {
			t.Fatalf("page: %d", rec.Code)
		}
		return fmt2Region(t, rec.Body.String())
	}
	// No place in either format: listening comes first, Read is the plain secondary button.
	r := page(l.owner)
	for _, w := range []string{playStart, `data-ts-read`, ">Read<", "ts-split"} {
		if !strings.Contains(r, w) {
			t.Errorf("fresh: missing %q", w)
		}
	}
	if strings.Contains(r, readStart) || strings.Contains(r, readContinue) {
		t.Error("fresh: reading words on the secondary button")
	}
	// Read last: reading is primary, Listen is plain and the queue menu goes with the primary.
	fmt2Listened(t, l, l.owner, l.book.ID, 1000)
	fmt2Reading(t, l, l.owner, l.book.ID, 4200, 2000)
	r = page(l.owner)
	for _, w := range []string{readContinue, ">Listen<", "Chapter 2 · 42 %"} {
		if !strings.Contains(r, w) {
			t.Errorf("read last: missing %q", w)
		}
	}
	for _, w := range []string{playResume, playStart, "ts-split"} {
		if strings.Contains(r, w) {
			t.Errorf("read last: %q still shown", w)
		}
	}
	if strings.Count(r, `class="ts-btn ts-btn--primary ts-btn--lg"`) != 1 {
		t.Errorf("read last: %d primary buttons, want 1", strings.Count(r, `class="ts-btn ts-btn--primary ts-btn--lg"`))
	}
	// Listened last: back to Resume with Read secondary.
	fmt2Listened(t, l, l.owner, l.book.ID, 3000)
	r = page(l.owner)
	if !strings.Contains(r, playResume) || strings.Contains(r, readContinue) || strings.Count(r, `class="ts-btn ts-btn--primary ts-btn--lg"`) != 1 {
		t.Errorf("listened last: %s", r)
	}
	// Without the right to read the text, only listening is drawn.
	if r = page(l.noRead); strings.Contains(r, "data-ts-read ") {
		t.Error("a person without can_read sees the Read button")
	}
}

func TestFormatsTextBookPage(t *testing.T) {
	l := fmt2Seed(t)
	path := "/books/" + l.tbook.ID
	for name, ck := range map[string]*http.Cookie{"owner": l.owner, "admin": l.admin, "member with can_read": l.reader, "guest with can_read": l.guest} {
		rec := l.get(ck, path, false)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", name, rec.Code)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Plain Prose") || !strings.Contains(body, readStart) || !strings.Contains(body, "/books/"+l.tbook.ID+"/read") {
			t.Errorf("%s: no Start reading button", name)
		}
		region := fmt2Region(t, body)
		for _, w := range []string{"data-ts-play", "ts-split", "data-ts-download", "Add to queue"} {
			if strings.Contains(region, w) {
				t.Errorf("%s: %q on a text book", name, w)
			}
		}
		for _, w := range []string{path + "/chapters", path + "/bookmarks", "sheet=write"} {
			if strings.Contains(body, w) {
				t.Errorf("%s: %q on a text book", name, w)
			}
		}
		if !strings.Contains(body, "Ebook") {
			t.Errorf("%s: no format chip", name)
		}
	}
	// The Text tab comes right after About.
	body := l.get(l.owner, path, false).Body.String()
	about, text, details := strings.Index(body, `href="`+path+`"`), strings.Index(body, `href="`+path+`/text"`), strings.Index(body, `href="`+path+`/details"`)
	if !(about >= 0 && about < text && text < details) {
		t.Errorf("tab order: about %d, text %d, details %d", about, text, details)
	}
	for _, tab := range []string{"/chapters", "/bookmarks"} {
		if rec := l.get(l.owner, path+tab, false); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", tab, rec.Code)
		}
	}
	// A person who may see the book but not read it: the sentence and no button.
	for name, ck := range map[string]*http.Cookie{"member without can_read": l.noRead, "guest without can_read": l.guestNoR} {
		rec := l.get(ck, path, false)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", name, rec.Code)
			continue
		}
		b := rec.Body.String()
		if !strings.Contains(b, readNeedsRight) || strings.Contains(b, "data-ts-read ") || strings.Contains(b, readStart) {
			t.Errorf("%s: wrong region", name)
		}
	}
	// Marking finished goes to the reading side, and never writes listening progress.
	form := url.Values{"finished": {"1"}, "_csrf": {l.a.tokenFor(t, l.owner)}}
	if rec := l.a.do(http.MethodPost, "/hx/books/"+l.tbook.ID+"/finished", form, func(r *http.Request) { r.Header.Set("HX-Request", "true") }, l.owner); rec.Code != http.StatusNoContent {
		t.Fatalf("finished: %d", rec.Code)
	}
	rp, ok, err := l.a.srv.progress.Reading(context.Background(), fmt2Uid(t, l, l.owner), l.tbook.ID)
	if err != nil || !ok || !rp.Finished() {
		t.Errorf("reading place after finish: %+v %v %v", rp, ok, err)
	}
	if n := l.a.count(t, `SELECT count(*) FROM progress WHERE book_id = ?`, l.tbook.ID); n != 0 {
		t.Errorf("%d listening rows for a text book", n)
	}
	if body = l.get(l.owner, path, false).Body.String(); !strings.Contains(body, readAgain) {
		t.Error("a finished text book does not offer Read again")
	}
	form = url.Values{"finished": {"1"}, "_csrf": {l.a.tokenFor(t, l.noRead)}}
	if rec := l.a.do(http.MethodPost, "/hx/books/"+l.tbook.ID+"/finished", form, nil, l.noRead); rec.Code != http.StatusNotFound {
		t.Errorf("finished without can_read: %d", rec.Code)
	}
}

func TestFormatsTextNotPlayable(t *testing.T) {
	l := fmt2Seed(t)
	id := l.tbook.ID
	tok := l.a.tokenFor(t, l.owner)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"progress get":  l.get(l.owner, "/progress/"+id, false),
		"player":        l.get(l.owner, "/hx/player/"+id, true),
		"progress post": l.a.do(http.MethodPost, "/progress/"+id, url.Values{"position_ms": {"5"}, "client_updated_at": {"5"}, "device_id": {"device-test-0001"}}, func(r *http.Request) { r.Header.Set("X-CSRF-Token", tok) }, l.owner),
		"queue":         l.a.do(http.MethodPost, "/queue", url.Values{"book_id": {id}, "where": {"end"}, "_csrf": {tok}}, nil, l.owner),
	} {
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

func TestFormatsHomeContinue(t *testing.T) {
	l := fmt2Seed(t)
	home := func() string { return l.get(l.owner, "/", false).Body.String() }
	if strings.Contains(home(), "Continue reading") {
		t.Error("hero without a reading place")
	}
	fmt2Reading(t, l, l.owner, l.tbook.ID, 4200, 5000)
	body := home()
	for _, w := range []string{"Continue reading", "Plain Prose", `href="/books/` + l.tbook.ID + `/read"`, "Chapter 2"} {
		if !strings.Contains(body, w) {
			t.Errorf("hero: missing %q", w)
		}
	}
	// One row per book with its last format: a later listening place takes the hero back.
	fmt2Listened(t, l, l.owner, l.book.ID, 9000)
	if body = home(); strings.Contains(body, "Continue reading") || !strings.Contains(body, "Continue listening") {
		t.Error("the newer place (listening) is not the hero")
	}
	// A text book not under way (percent 0) or finished is not offered.
	list, err := l.a.lib.ContinueBooks(context.Background(), fmt2All, fmt2Uid(t, l, l.owner), 5)
	if err != nil || len(list) != 2 || list[0].Format != library.BookAudio || list[1].Format != library.BookText {
		t.Errorf("ContinueBooks: %+v %v", list, err)
	}
	// Another person sees nothing of this reading place.
	if body = l.get(l.admin, "/", false).Body.String(); strings.Contains(body, "Continue reading") {
		t.Error("the admin sees the owner's reading place")
	}
}

func TestFormatsFilters(t *testing.T) {
	l := fmt2Seed(t)
	ctx := context.Background()
	owner := fmt2Uid(t, l, l.owner)
	fmt2Reading(t, l, l.owner, l.tbook.ID, 3000, 5000)
	ids := func(q library.ListQuery) map[string]bool {
		t.Helper()
		q.UserID = owner
		page, err := l.a.lib.ListBooks(ctx, fmt2All, q)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		out := map[string]bool{}
		for _, b := range page.Books {
			out[b.ID] = true
			if b.ID == l.tbook.ID && b.Kind != library.BookText {
				t.Errorf("card kind %q", b.Kind)
			}
		}
		return out
	}
	text, audio := l.tbook.ID, l.book.ID
	for _, c := range []struct {
		name      string
		q         library.ListQuery
		in, notIn string
	}{
		{"format text", library.ListQuery{Format: library.BookText}, text, audio},
		{"format audio", library.ListQuery{Format: library.BookAudio}, audio, text},
		{"unfinished reads the reading place", library.ListQuery{Show: library.ShowUnfinished}, text, audio},
		{"not started leaves a begun text book out", library.ListQuery{Show: library.ShowNotStarted}, audio, text},
		{"not finished", library.ListQuery{Show: library.ShowNotFinished}, text, ""},
		{"length leaves text books out", library.ListQuery{MinDurationMs: 1}, audio, text},
		{"short", library.ListQuery{MaxDurationMs: 10 * sweepHourMs}, audio, text},
	} {
		got := ids(c.q)
		if !got[c.in] || (c.notIn != "" && got[c.notIn]) {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	if _, err := l.a.srv.progress.SetReadingFinished(ctx, owner, text, true); err != nil {
		t.Fatal(err)
	}
	if got := ids(library.ListQuery{Show: library.ShowFinished}); !got[text] || got[audio] {
		t.Errorf("finished: %v", got)
	}
	if got := ids(library.ListQuery{Show: library.ShowNotFinished}); got[text] {
		t.Errorf("not finished still lists the finished text book: %v", got)
	}
	// The page: the Format filter and the Ebook slot of a row.
	body := l.get(l.owner, "/libraries?view=list&format=text", false).Body.String()
	if !strings.Contains(body, "Plain Prose") || strings.Contains(body, "Textword Lantern") || !strings.Contains(body, "Ebook") {
		t.Error("format=text page")
	}
	if !strings.Contains(l.get(l.owner, "/libraries", false).Body.String(), "Format") {
		t.Error("no Format filter once an ebook exists")
	}
	// A member's list never counts a book they cannot see: a guest-scoped query keeps both books out.
	none, err := l.a.lib.ListBooks(ctx, fmt2None, library.ListQuery{Format: library.BookText})
	if err != nil || len(none.Books) != 0 || none.Total != 0 {
		t.Errorf("empty scope: %+v %v", none, err)
	}
}

func fmt2Token(t *testing.T, l *fmt2Lab, ck *http.Cookie) string {
	t.Helper()
	var tok string
	err := l.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, _, err = auth.NewDevices(l.a.st, nil).Issue(context.Background(), tx, fmt2Uid(t, l, ck), "API test", auth.KindMobile)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestFormatsAPI(t *testing.T) {
	l := fmt2Seed(t)
	fmt2Reading(t, l, l.owner, l.tbook.ID, 4200, 5000)
	get := func(ck *http.Cookie, id string) (int, map[string]any) {
		rec := deviceBearerReq(l.a, http.MethodGet, "/api/v1/books/"+id, fmt2Token(t, l, ck), nil)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}
	code, m := get(l.owner, l.tbook.ID)
	if code != http.StatusOK || m["kind"] != "text" {
		t.Fatalf("text book: %d %v", code, m["kind"])
	}
	if f, _ := m["formats"].([]any); len(f) != 1 || f[0] != "text" {
		t.Errorf("formats %v", m["formats"])
	}
	if rd, _ := m["reading"].(map[string]any); rd == nil || rd["percentBp"] != float64(4200) || rd["updatedAt"] != float64(5000) {
		t.Errorf("reading %v", m["reading"])
	}
	if files, _ := m["files"].([]any); len(files) != 0 {
		t.Errorf("files %v", files)
	}
	code, m = get(l.owner, l.book.ID)
	if f, _ := m["formats"].([]any); code != http.StatusOK || m["kind"] != "audio" || len(f) != 2 || f[0] != "audio" || f[1] != "text" || m["reading"] != nil {
		t.Errorf("audio book with an ebook: %d %v %v %v", code, m["kind"], m["formats"], m["reading"])
	}
	// Without can_read there is no text format and no reading place.
	code, m = get(l.noRead, l.book.ID)
	if f, _ := m["formats"].([]any); code != http.StatusOK || len(f) != 1 || f[0] != "audio" || m["reading"] != nil {
		t.Errorf("no can_read: %d %v %v", code, m["formats"], m["reading"])
	}
}

func TestFormatsDownload(t *testing.T) {
	l := fmt2Seed(t)
	// The members of the lab hold can_download = 0; give one reader the right.
	dl := l.reader
	if _, err := l.a.st.Writer().Exec(`UPDATE grants SET can_download = 1 WHERE user_id = ?`, fmt2Uid(t, l, dl)); err != nil {
		t.Fatal(err)
	}
	file := func(book, fid string) string { return "/books/" + book + "/text/" + fid + "/file" }
	f, ok, err := l.svc.Store().File(context.Background(), l.tf)
	if err != nil || !ok {
		t.Fatalf("file: %+v %v %v", f, ok, err)
	}
	want, err := os.ReadFile(l.svc.FilePath(f))
	if err != nil {
		t.Fatal(err)
	}
	for name, ck := range map[string]*http.Cookie{"owner": l.owner, "admin": l.admin, "member with can_download": dl} {
		rec := l.get(ck, file(l.tbook.ID, l.tf), false)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), want) {
			t.Errorf("%s: %d", name, rec.Code)
			continue
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || rec.Header().Get("Cache-Control") != "no-store" ||
			rec.Header().Get("Content-Type") != "application/epub+zip" {
			t.Errorf("%s: headers %q %q", name, cd, rec.Header())
		}
	}
	// Reading is not enough, and neither is a missing right to read.
	for name, ck := range map[string]*http.Cookie{"member without can_download": l.guest, "member without can_read": l.noRead, "guest without can_read": l.guestNoR} {
		if rec := l.get(ck, file(l.tbook.ID, l.tf), false); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// A file of another book, a refused file and an unknown id are never served.
	refused := l.addPlaced(t, "bad.epub", "refused", "drm")
	if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = ? WHERE id = ?`, l.tbook.ID, refused); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{"another book's file": file(l.tbook.ID, l.af), "refused": file(l.tbook.ID, refused), "unknown": file(l.tbook.ID, "nope")} {
		if rec := l.get(l.owner, p, false); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// The Text tab offers it to those who may take it, and only to them.
	if tab := l.get(l.owner, "/books/"+l.tbook.ID+"/text", false).Body.String(); !strings.Contains(tab, "Download this ebook") {
		t.Error("owner: no download link on the Text tab")
	}
	if tab := l.get(l.guest, "/books/"+l.tbook.ID+"/text", false).Body.String(); strings.Contains(tab, "Download this ebook") {
		t.Error("a reader without can_download is offered the download")
	}
}

func TestPlayerReadIcon(t *testing.T) {
	l := fmt2Seed(t)
	has := func(ck *http.Cookie, book string) bool {
		rec := l.get(ck, "/hx/player/"+book, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("player %s: %d", book, rec.Code)
		}
		return strings.Contains(rec.Body.String(), `data-read-href="/books/`+book+`/read"`)
	}
	if !has(l.owner, l.book.ID) {
		t.Error("owner: no data-read-href on an audiobook with an ebook")
	}
	if has(l.noRead, l.book.ID) {
		t.Error("data-read-href without can_read")
	}
	// An audiobook without an ebook has none, and neither has a text book (it has no player at all).
	bare := librarytest.AddBook(t, l.a.lib, library.Folder{ID: l.book.FolderID, LibraryID: l.book.LibraryID, Path: l.a.dir},
		librarytest.BookSpec{Title: "Bare Audio", DurationMs: sweepHourMs})
	if has(l.owner, bare.ID) {
		t.Error("data-read-href on an audiobook with no ebook")
	}
	// A visitor of a share link never gets the icon.
	e := linkNewEnv(t)
	_, tok := e.make(e.book.ID, 7, "")
	if body := e.get("/s/" + tok).Body.String(); !strings.Contains(body, `id="player-data"`) || strings.Contains(body, "data-read-href") {
		t.Error("share link page: player data wrong")
	}
}
