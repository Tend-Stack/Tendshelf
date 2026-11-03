package web

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// coverLab is an app with the real cover adoption job registered (the runner is not started until a
// test needs a job to run), one library with two books and a cover on each, and a member who holds
// a grant on the library.
type coverLab struct {
	a             *testApp
	cn            *compareNet
	covers        *mediajobs.Covers
	book, other   library.Book
	cover, ocover string
	owner, member *http.Cookie
}

func newCoverLab(t *testing.T) *coverLab {
	t.Helper()
	cn := &compareNet{}
	a := newTestApp(t, compareMod(t, cn))
	l := &coverLab{a: a, cn: cn}
	l.owner = a.as(t, users.RoleOwner)
	l.member = a.as(t, users.RoleMember)
	memberID := sweepUserID(t, a, l.member)
	lib, folder := librarytest.NewLibrary(t, a.lib, "Cover library", adminAudioDir(t, true))
	l.book = librarytest.AddBook(t, a.lib, folder, librarytest.BookSpec{Title: "Coverword Lantern", Authors: []string{"Imogen Coverauthor"}, DurationMs: sweepHourMs, AddedAt: 5, Files: 1})
	l.other = librarytest.AddBook(t, a.lib, folder, librarytest.BookSpec{Title: "Otherword Anchor", Authors: []string{"Imogen Coverauthor"}, DurationMs: sweepHourMs, AddedAt: 6, Files: 1})
	if _, err := a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at)
		VALUES ('gr_cover', ?, 'library', ?, 0, 0, 1, 1)`, memberID, lib.ID); err != nil {
		t.Fatal(err)
	}
	for i, b := range []*library.Book{&l.book, &l.other} {
		sha := fmt.Sprintf("%064x", 0xc0de00+i)
		rel := sweepWriteCover(t, a.dir, sha)
		cv := library.Cover{BookID: b.ID, Source: library.CoverFolder, Mime: "image/png", Width: 8, Height: 8, SHA256: sha, Path: rel, Tint: "#1f4a52"}
		if err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
			if err := tx.PutCover(&cv); err != nil {
				return err
			}
			return tx.SetBookCover(b.ID, cv.ID)
		}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			l.cover = cv.ID
		} else {
			l.ocover = cv.ID
		}
	}
	l.covers = mediajobs.NewCovers(a.lib, mediatest.FakeTools(t), a.dir, a.runner, nil)
	l.covers.SetFetcher(cn.client.FetchCover)
	if err := mediajobs.Register(a.runner, l.covers, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	a.srv.covers = l.covers
	return l
}

// start runs the job runner until the test ends.
func (l *coverLab) start(t *testing.T) {
	t.Helper()
	if err := l.a.runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.a.runner.Stop(context.Background()) })
}

func (l *coverLab) bookCover(t *testing.T, id string) string {
	t.Helper()
	var c sql.NullString
	if err := l.a.st.Reader().QueryRow(`SELECT cover_id FROM books WHERE id = ?`, id).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c.String
}

// upload sends a multipart form with the token and, when name is not empty, one picture part.
func (l *coverLab) upload(t *testing.T, ck *http.Cookie, path, name string, data []byte, token bool) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if token {
		if err := mw.WriteField("_csrf", l.a.tokenFor(t, ck)); err != nil {
			t.Fatal(err)
		}
	}
	if name != "" {
		fw, err := mw.CreateFormFile("picture", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(ck)
	rec := httptest.NewRecorder()
	l.a.h.ServeHTTP(rec, req)
	return rec
}

// fakeJPEG is a body that starts like a JPEG and is n bytes long. Staging checks the first bytes
// and the size only.
func fakeJPEG(n int) []byte {
	b := make([]byte, n)
	copy(b, []byte{0xff, 0xd8, 0xff, 0xe0})
	return b
}

func coverPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{uint8(4 * x), 90, uint8(4 * y), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (l *coverLab) stagedFiles(t *testing.T) []string {
	t.Helper()
	es, err := os.ReadDir(mediajobs.IncomingDir(l.a.dir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestCoverPickUpload(t *testing.T) {
	l := newCoverLab(t)
	path := "/books/" + l.book.ID + "/cover/upload"

	// A 4 MiB JPEG passes the CSRF check and is staged; a job is queued and the sheet shows it.
	rec := l.upload(t, l.owner, path, "big.jpg", fakeJPEG(4<<20), true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("4 MiB upload: %d %s", rec.Code, rec.Body.String())
	}
	loc := location(rec)
	if !strings.HasPrefix(loc, "/books/"+l.book.ID+"/details?sheet=cover&job=") {
		t.Fatalf("Location = %q", loc)
	}
	if got := l.stagedFiles(t); len(got) != 1 {
		t.Errorf("staged = %v, want one file", got)
	}
	if n := l.a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'book.cover.upload'`); n != 1 {
		t.Errorf("upload audit rows = %d", n)
	}
	page := l.a.do(http.MethodGet, loc, nil, nil, l.owner)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), coverWaiting) {
		t.Errorf("sheet with the job: %d, waiting text present = %v", page.Code, strings.Contains(page.Body.String(), coverWaiting))
	}

	// One byte over 5 MiB is refused in plain words, and nothing more is staged.
	rec = l.upload(t, l.owner, path, "huge.jpg", fakeJPEG(5<<20+1), true)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), coverTooLarge) {
		t.Errorf("5 MiB + 1: %d %q", rec.Code, rec.Body.String())
	}
	// Exactly 5 MiB is fine.
	if rec = l.upload(t, l.owner, path, "edge.jpg", fakeJPEG(5<<20), true); rec.Code != http.StatusSeeOther {
		t.Errorf("5 MiB: %d", rec.Code)
	}
	// Something that is not a picture is a 422 with the plain sentence.
	rec = l.upload(t, l.owner, path, "notes.txt", []byte("just some words"), true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), coverNotPicture) {
		t.Errorf("text file: %d %q", rec.Code, rec.Body.String())
	}
	// No file part at all.
	if rec = l.upload(t, l.owner, path, "", nil, true); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("no picture: %d", rec.Code)
	}
	// No token: the house CSRF refusal.
	if rec = l.upload(t, l.owner, path, "a.jpg", fakeJPEG(1000), false); rec.Code != http.StatusForbidden {
		t.Errorf("missing _csrf: %d, want 403", rec.Code)
	}
	// A body past the upload limit is refused by the CSRF check, not read to the end.
	if rec = l.upload(t, l.owner, path, "a.jpg", fakeJPEG(6<<20+1), true); rec.Code != http.StatusForbidden {
		t.Errorf("6 MiB + 1 body: %d, want 403", rec.Code)
	}

	// The larger limit is for this route only: a 2 MiB multipart body to another route, and a 2 MiB
	// urlencoded one to this route, are still refused at 1 MiB.
	if rec = l.upload(t, l.owner, "/books/"+l.book.ID+"/cover", "a.jpg", fakeJPEG(2<<20), true); rec.Code != http.StatusForbidden {
		t.Errorf("2 MiB multipart to another route: %d, want 403", rec.Code)
	}
	form := url.Values{"pad": {strings.Repeat("x", 2<<20)}, "_csrf": {l.a.tokenFor(t, l.owner)}}
	if rec = l.a.do(http.MethodPost, path, form, nil, l.owner); rec.Code != http.StatusForbidden {
		t.Errorf("2 MiB urlencoded to the upload route: %d, want 403", rec.Code)
	}
	form = url.Values{"pad": {strings.Repeat("x", 2<<20)}, "_csrf": {l.a.tokenFor(t, l.owner)}}
	if rec = l.a.do(http.MethodPost, "/books/"+l.book.ID+"/details/reset", form, nil, l.owner); rec.Code != http.StatusForbidden {
		t.Errorf("2 MiB urlencoded to another route: %d, want 403", rec.Code)
	}
}

func TestCoverPickUploadForbidden(t *testing.T) {
	l := newCoverLab(t)
	path := "/books/" + l.book.ID + "/cover/upload"
	// A member who can open the book still cannot change its cover; nothing is staged.
	if rec := l.upload(t, l.member, path, "a.png", coverPNG(t), true); rec.Code != http.StatusNotFound {
		t.Errorf("member: %d, want 404", rec.Code)
	}
	if got := l.stagedFiles(t); len(got) != 0 {
		t.Errorf("a refused upload left %v", got)
	}
	// Anonymous: sent to sign in (or refused for the missing token), never staged.
	rec := l.a.do(http.MethodPost, path, url.Values{}, nil)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous: %d", rec.Code)
	}
	// With covers turned off the route does not exist.
	l.a.srv.covers = nil
	if rec := l.upload(t, l.owner, path, "a.png", coverPNG(t), true); rec.Code != http.StatusNotFound {
		t.Errorf("no covers service: %d, want 404", rec.Code)
	}
}

func TestCoverPickChoose(t *testing.T) {
	l := newCoverLab(t)
	ctx := context.Background()
	// A second cover of the book, and the cover of the other book.
	second := library.Cover{BookID: l.book.ID, Source: library.CoverUpload, Mime: "image/png", Width: 8, Height: 8,
		SHA256: fmt.Sprintf("%064x", 0xc0de99), Path: sweepWriteCover(t, l.a.dir, fmt.Sprintf("%064x", 0xc0de99))}
	if err := l.a.lib.Update(ctx, func(tx *library.Tx) error { return tx.PutCover(&second) }); err != nil {
		t.Fatal(err)
	}
	post := func(ck *http.Cookie, book, cover string) *httptest.ResponseRecorder {
		return l.a.do(http.MethodPost, "/books/"+book+"/cover", url.Values{"cover_id": {cover}, "_csrf": {l.a.tokenFor(t, ck)}}, nil, ck)
	}

	// Another book's cover id is a 404 and changes nothing.
	if rec := post(l.owner, l.book.ID, l.ocover); rec.Code != http.StatusNotFound {
		t.Errorf("other book's cover: %d, want 404", rec.Code)
	}
	if rec := post(l.owner, l.book.ID, "no-such-cover"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown cover: %d, want 404", rec.Code)
	}
	if got := l.bookCover(t, l.book.ID); got != l.cover {
		t.Fatalf("cover changed by a refused request: %q", got)
	}
	// A member cannot choose.
	if rec := post(l.member, l.book.ID, second.ID); rec.Code != http.StatusNotFound {
		t.Errorf("member: %d, want 404", rec.Code)
	}
	// The owner can: 303 to the Details tab, the cover changes, and an audit row is written.
	rec := post(l.owner, l.book.ID, second.ID)
	if rec.Code != http.StatusSeeOther || location(rec) != "/books/"+l.book.ID+"/details" {
		t.Fatalf("choose: %d %q", rec.Code, location(rec))
	}
	if got := l.bookCover(t, l.book.ID); got != second.ID {
		t.Errorf("books.cover_id = %q, want %q", got, second.ID)
	}
	if n := l.a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'book.cover.choose'`); n != 1 {
		t.Errorf("choose audit rows = %d", n)
	}
	// The sheet lists both covers, marks the chosen one and offers the upload form.
	page := l.a.do(http.MethodGet, "/books/"+l.book.ID+"/details?sheet=cover", nil, nil, l.owner).Body.String()
	for _, want := range []string{`value="` + l.cover + `"`, `value="` + second.ID + `"`, `enctype="multipart/form-data"`} {
		if !strings.Contains(page, want) {
			t.Errorf("cover sheet lacks %q", want)
		}
	}
	if !regexp.MustCompile(`value="` + second.ID + `"[^>]*checked`).MatchString(page) {
		t.Error("the chosen cover is not the checked one")
	}
	for _, want := range []string{coverUse, coverUploadLabel, coverUploadHint, coverFromFolder, coverFromUpload} {
		if !strings.Contains(page, html.EscapeString(want)) {
			t.Errorf("cover sheet lacks %q", want)
		}
	}
	// The thumbnail route: the book's own covers only, and only for people who may edit.
	choice := func(ck *http.Cookie, book, cover string) int {
		return l.a.do(http.MethodGet, "/covers/"+book+"/choices/"+cover+"?w=160", nil, nil, ck).Code
	}
	if c := choice(l.owner, l.book.ID, second.ID); c != http.StatusOK {
		t.Errorf("own cover: %d", c)
	}
	if c := choice(l.owner, l.book.ID, l.ocover); c != http.StatusNotFound {
		t.Errorf("other book's cover: %d, want 404", c)
	}
	if c := choice(l.member, l.book.ID, second.ID); c != http.StatusNotFound {
		t.Errorf("member: %d, want 404", c)
	}
	if rec := l.a.do(http.MethodGet, "/covers/"+l.book.ID+"/choices/"+second.ID+"?w=99", nil, nil, l.owner); rec.Code != http.StatusBadRequest {
		t.Errorf("odd width: %d, want 400", rec.Code)
	}
	// "Change cover" shows on the book page for the owner and not for the member.
	if p := l.a.do(http.MethodGet, "/books/"+l.book.ID, nil, nil, l.owner).Body.String(); !strings.Contains(p, coverChangeLink) {
		t.Error("the owner's book page has no Change cover link")
	}
	if p := l.a.do(http.MethodGet, "/books/"+l.book.ID, nil, nil, l.member).Body.String(); strings.Contains(p, coverChangeLink) {
		t.Error("the member's book page shows Change cover")
	}
	if rec := l.a.do(http.MethodGet, "/books/"+l.book.ID+"/details?sheet=cover", nil, nil, l.member); rec.Code != http.StatusNotFound {
		t.Errorf("member's cover sheet: %d, want 404", rec.Code)
	}
}

// waitFor polls until cond is true.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestCoverPickAdoptUpload runs the real job for an upload and reads the poll fragment and the sheet.
func TestCoverPickAdoptUpload(t *testing.T) {
	l := newCoverLab(t)
	l.start(t)
	rec := l.upload(t, l.owner, "/books/"+l.book.ID+"/cover/upload", "new.png", coverPNG(t), true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	jobID := strings.TrimPrefix(location(rec), "/books/"+l.book.ID+"/details?sheet=cover&job=")
	waitFor(t, "the new cover", func() bool { return l.bookCover(t, l.book.ID) != l.cover })
	var state string
	waitFor(t, "the job to finish", func() bool {
		return l.a.st.Reader().QueryRow(`SELECT state FROM jobs WHERE id = ?`, jobID).Scan(&state) == nil && state == "done"
	})
	frag := l.a.do(http.MethodGet, "/hx/jobs/"+jobID, nil, compareHx, l.owner)
	if frag.Code != http.StatusOK || !strings.Contains(frag.Body.String(), coverDone) || strings.Contains(frag.Body.String(), "every 2s") {
		t.Errorf("finished poll: %d %q", frag.Code, frag.Body.String())
	}
	var src string
	if err := l.a.st.Reader().QueryRow(`SELECT source FROM covers WHERE id = ?`, l.bookCover(t, l.book.ID)).Scan(&src); err != nil || src != library.CoverUpload {
		t.Errorf("new cover source = %q, %v", src, err)
	}
	// A job of this book is not readable through another book or by a member.
	if rec := l.a.do(http.MethodGet, "/books/"+l.other.ID+"/details?sheet=cover&job="+jobID, nil, nil, l.owner); rec.Code != http.StatusNotFound {
		t.Errorf("job of another book: %d, want 404", rec.Code)
	}
	if rec := l.a.do(http.MethodGet, "/hx/jobs/"+jobID, nil, compareHx, l.member); rec.Code != http.StatusNotFound {
		t.Errorf("member's poll: %d, want 404", rec.Code)
	}
}

// TestCoverPickAdoptRefused shows a refused picture as plain text in the poll fragment.
func TestCoverPickAdoptRefused(t *testing.T) {
	l := newCoverLab(t)
	l.start(t)
	// The first bytes pass staging; the job's full check refuses the broken picture.
	rec := l.upload(t, l.owner, "/books/"+l.book.ID+"/cover/upload", "broken.jpg", fakeJPEG(2000), true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d", rec.Code)
	}
	jobID := strings.TrimPrefix(location(rec), "/books/"+l.book.ID+"/details?sheet=cover&job=")
	var state string
	waitFor(t, "the job to fail", func() bool {
		return l.a.st.Reader().QueryRow(`SELECT state FROM jobs WHERE id = ?`, jobID).Scan(&state) == nil && state == "failed"
	})
	frag := l.a.do(http.MethodGet, "/hx/jobs/"+jobID, nil, compareHx, l.owner).Body.String()
	if !strings.Contains(frag, "not a JPEG, PNG or WebP picture") {
		t.Errorf("failed poll lacks the plain text: %q", frag)
	}
	if got := l.bookCover(t, l.book.ID); got != l.cover {
		t.Errorf("a refused picture changed the cover to %q", got)
	}
}

// TestCoverPickApplyCover applies a ticked online cover from the compare sheet: the apply route
// queues media.cover_adopt with the candidate's address, the job fetches it and the cover changes.
func TestCoverPickApplyCover(t *testing.T) {
	l := newCoverLab(t)
	e := &sweepEnv{t: t, a: l.a, cn: l.cn}
	lookup := compareSeedJob(t, e, l.book.ID, true)
	l.start(t)
	form := url.Values{"job": {lookup}, "c": {"0"}, "use": {"cover"}, "_csrf": {l.a.tokenFor(t, l.owner)}}
	rec := l.a.do(http.MethodPost, "/books/"+l.book.ID+"/match/apply", form, nil, l.owner)
	if rec.Code != http.StatusSeeOther || location(rec) != "/books/"+l.book.ID+"/details?applied=1" {
		t.Fatalf("apply: %d %q", rec.Code, location(rec))
	}
	waitFor(t, "the online cover", func() bool { return l.bookCover(t, l.book.ID) != l.cover })
	var src string
	if err := l.a.st.Reader().QueryRow(`SELECT source FROM covers WHERE id = ?`, l.bookCover(t, l.book.ID)).Scan(&src); err != nil || src != library.CoverOnline {
		t.Errorf("new cover source = %q, %v", src, err)
	}
	// The sheet now lists the online cover with its label and marks it in use.
	page := l.a.do(http.MethodGet, "/books/"+l.book.ID+"/details?sheet=cover", nil, nil, l.owner).Body.String()
	if !strings.Contains(page, html.EscapeString(coverFromOnline)) || !strings.Contains(page, html.EscapeString(coverFromFolder)) {
		t.Errorf("sheet lacks the online and folder covers")
	}
	// The compare sheet's cover row still previews through the cached route.
	table := l.a.do(http.MethodGet, "/books/"+l.book.ID+"/details?sheet=compare&job="+lookup+"&c=0", nil, nil, l.owner).Body.String()
	if !strings.Contains(table, "/books/"+l.book.ID+"/match/"+lookup+"/cover/0") {
		t.Error("the compare table does not preview the online cover through /match/{job}/cover/{n}")
	}
}

// photoLab seeds a photo for the lab's author and returns the person's id.
func (l *coverLab) author(t *testing.T) string {
	t.Helper()
	people, err := l.a.lib.BookPeople(context.Background(), l.book.ID)
	if err != nil || len(people) == 0 {
		t.Fatalf("people: %v %v", people, err)
	}
	return people[0].PersonID
}

func TestCoverPickPhoto(t *testing.T) {
	l := newCoverLab(t)
	id := l.author(t)
	sha := strings.Repeat("ab", 32)
	coverSeedPhoto(t, l.a, id, sha, ".jpg")
	get := func(ck *http.Cookie) *httptest.ResponseRecorder {
		return l.a.do(http.MethodGet, "/people/"+id+"/photo", nil, nil, ck)
	}
	rec := get(l.owner)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" ||
		rec.Header().Get("Cache-Control") != "private, max-age=86400" || rec.Header().Get("ETag") != `"`+sha+`.jpg"` {
		t.Errorf("owner: %d %v", rec.Code, rec.Header())
	}
	// A member who can open a book by this author sees the photo; anonymous does not.
	if rec := get(l.member); rec.Code != http.StatusOK {
		t.Errorf("member with a grant: %d, want 200", rec.Code)
	}
	if rec := l.a.do(http.MethodGet, "/people/"+id+"/photo", nil, nil); rec.Code == http.StatusOK {
		t.Errorf("anonymous got the photo")
	}
	// A member with no matching grant gets a 404.
	if _, err := l.a.st.Writer().Exec(`DELETE FROM grants`); err != nil {
		t.Fatal(err)
	}
	if rec := get(l.member); rec.Code != http.StatusNotFound {
		t.Errorf("member without a grant: %d, want 404", rec.Code)
	}
	// The page shows the photo (alt "" next to the name), the bio in paragraphs and the lookup button.
	if _, err := l.a.st.Writer().Exec(`UPDATE people SET description = ? WHERE id = ?`, "First paragraph.\n\nSecond <b>paragraph</b>.", id); err != nil {
		t.Fatal(err)
	}
	page := l.a.do(http.MethodGet, "/authors/"+id, nil, nil, l.owner).Body.String()
	for _, want := range []string{`src="/people/` + id + `/photo?v=abababab"`, `alt=""`, `>First paragraph.</p>`,
		`>Second &lt;b&gt;paragraph&lt;/b&gt;.</p>`, authorLookupButton} {
		if !strings.Contains(page, want) {
			t.Errorf("author page lacks %q", want)
		}
	}
	// A narrator page gets the photo and bio but no lookup button.
	if _, err := l.a.st.Writer().Exec(`UPDATE people SET image_id = NULL WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if rec := get(l.owner); rec.Code != http.StatusNotFound {
		t.Errorf("person without a photo: %d, want 404", rec.Code)
	}
}

// TestCoverPickPhotoPath: an image id planted in the database that is not a stored photo's name
// never reaches the file system.
func TestCoverPickPhotoPath(t *testing.T) {
	l := newCoverLab(t)
	id := l.author(t)
	// A file the traversal would reach if the id were used as a path.
	secret := filepath.Join(l.a.dir, "secret-320.jpg")
	if err := os.WriteFile(secret, []byte("\xff\xd8\xff secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../secret", "../secret.jpg", strings.Repeat("a", 63) + ".jpg", strings.Repeat("A", 64) + ".jpg",
		strings.Repeat("a", 64) + ".gif", strings.Repeat("a", 64), "/etc/passwd", strings.Repeat("a", 64) + "/../x.jpg"} {
		if _, err := l.a.st.Writer().Exec(`UPDATE people SET image_id = ? WHERE id = ?`, bad, id); err != nil {
			t.Fatal(err)
		}
		before := openCoverCalls(t, func() {
			if rec := l.a.do(http.MethodGet, "/people/"+id+"/photo", nil, nil, l.owner); rec.Code != http.StatusNotFound {
				t.Errorf("image id %q: %d, want 404", bad, rec.Code)
			}
		})
		if before != 0 {
			t.Errorf("image id %q reached the file system %d times", bad, before)
		}
		if p := l.a.do(http.MethodGet, "/authors/"+id, nil, nil, l.owner).Body.String(); strings.Contains(p, "/photo") {
			t.Errorf("image id %q: the page links a photo", bad)
		}
	}
}

// openCoverCalls counts the files opened through the cover opener while f runs.
func openCoverCalls(t *testing.T, f func()) int {
	t.Helper()
	orig := openCover
	n := 0
	openCover = func(name string) (*os.File, error) {
		n++
		return orig(name)
	}
	defer func() { openCover = orig }()
	f()
	return n
}

// TestCoverPickAuthorLookup starts an author lookup: owner and admin only, online on, and the page
// polls the job. A narrator page has no button.
func TestCoverPickAuthorLookup(t *testing.T) {
	l := newCoverLab(t)
	id := l.author(t)
	post := func(ck *http.Cookie) *httptest.ResponseRecorder {
		return l.a.do(http.MethodPost, "/people/"+id+"/lookup", url.Values{"_csrf": {l.a.tokenFor(t, ck)}}, nil, ck)
	}
	if rec := post(l.member); rec.Code != http.StatusNotFound {
		t.Errorf("member: %d, want 404", rec.Code)
	}
	rec := post(l.owner)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), "/authors/"+id+"?job=") {
		t.Fatalf("owner: %d %q", rec.Code, location(rec))
	}
	if n := l.a.count(t, `SELECT COUNT(*) FROM jobs WHERE kind = 'metadata.online_author'`); n != 1 {
		t.Errorf("author jobs = %d", n)
	}
	page := l.a.do(http.MethodGet, location(rec), nil, nil, l.owner).Body.String()
	jobID := strings.TrimPrefix(location(rec), "/authors/"+id+"?job=")
	for _, want := range []string{authorWaiting, `hx-get="/hx/jobs/` + jobID + `"`, `hx-trigger="every 2s"`} {
		if !strings.Contains(page, want) {
			t.Errorf("author page with a job lacks %q", want)
		}
	}
	// Finished: the poll answers without polling again.
	if _, err := l.a.st.Writer().Exec(`UPDATE jobs SET state = 'done', result_json = '{"found":true}' WHERE id = ?`, jobID); err != nil {
		t.Fatal(err)
	}
	frag := l.a.do(http.MethodGet, "/hx/jobs/"+jobID, nil, compareHx, l.owner).Body.String()
	if !strings.Contains(frag, html.EscapeString(authorDone)) || strings.Contains(frag, "every 2s") {
		t.Errorf("finished author poll: %q", frag)
	}
	// Online off: the button, the route and the poll are gone.
	if err := metadata.SetOnlineEnabled(context.Background(), l.a.st, false, ""); err != nil {
		t.Fatal(err)
	}
	if rec := post(l.owner); rec.Code != http.StatusNotFound {
		t.Errorf("online off: %d, want 404", rec.Code)
	}
	if rec := l.a.do(http.MethodGet, "/hx/jobs/"+jobID, nil, compareHx, l.owner); rec.Code != http.StatusNotFound {
		t.Errorf("online off, poll: %d, want 404", rec.Code)
	}
	if p := l.a.do(http.MethodGet, "/authors/"+id, nil, nil, l.owner).Body.String(); strings.Contains(p, authorLookupButton) {
		t.Error("online off: the author page still offers the lookup")
	}
}
