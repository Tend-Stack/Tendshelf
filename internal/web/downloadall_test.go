package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// downloadAllList is the JSON of the two download-list routes.
type downloadAllList struct {
	Books []struct {
		ID, Title string
		Bytes     int64
	}
	TotalBytes int64
}

// downloadAllGet reads a list route as a lab person. The status is returned; the list only on 200.
func (l *feedLab) downloadAllGet(t *testing.T, who, path string) (int, downloadAllList) {
	t.Helper()
	rec := l.a.do(http.MethodGet, path, nil, proxyDefault, l.cookies[who])
	var out downloadAllList
	if rec.Code != http.StatusOK {
		return rec.Code, out
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("%s %s content type %q", who, path, ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("%s %s Cache-Control %q, want no-store", who, path, cc)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s %s: %v", who, path, err)
	}
	return rec.Code, out
}

// downloadAllBytes is the size of the audio files of books, as the lab laid them down.
func (l *feedLab) downloadAllBytes(books ...library.Book) int64 {
	var n int64
	for _, b := range books {
		for _, f := range l.files[b.ID] {
			n += f.Size
		}
	}
	return n
}

func (l *feedLab) downloadAllExpect(t *testing.T, who, path string, want ...library.Book) {
	t.Helper()
	code, got := l.downloadAllGet(t, who, path)
	if code != http.StatusOK {
		t.Fatalf("%s GET %s = %d, want 200", who, path, code)
	}
	var ids, wantIDs []string
	for _, b := range got.Books {
		ids = append(ids, b.ID)
	}
	for _, b := range want {
		wantIDs = append(wantIDs, b.ID)
	}
	if !slices.Equal(ids, wantIDs) {
		t.Errorf("%s GET %s lists %v, want %v", who, path, ids, wantIDs)
	}
	if want := l.downloadAllBytes(want...); got.TotalBytes != want {
		t.Errorf("%s GET %s totalBytes %d, want %d", who, path, got.TotalBytes, want)
	}
	var sum int64
	for _, b := range got.Books {
		sum += b.Bytes
	}
	if sum != got.TotalBytes {
		t.Errorf("%s GET %s: books add up to %d, totalBytes %d", who, path, sum, got.TotalBytes)
	}
}

func (l *feedLab) downloadAllShelf(t *testing.T, ownerName string, books ...library.Book) library.Shelf {
	t.Helper()
	ctx := context.Background()
	sh, err := l.a.lib.CreateShelf(ctx, l.ids[ownerName], "Download all "+ownerName)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range books {
		if err := l.a.lib.AddToShelf(ctx, l.ids[ownerName], sh.ID, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	return sh
}

// TestDownloadAllSeriesList: each role sees the books it may download, in the series page's order, with
// the audio bytes; refused books, books with no audio on disk and an unknown series are left out or 404.
func TestDownloadAllSeriesList(t *testing.T) {
	l := newFeedLab(t)
	path := "/series/" + l.seriesID + "/download-list"
	// Saga: multi (1), sa (2), sb (3, library two), a refused book (4) and a book whose audio is gone (5).
	l.downloadAllExpect(t, "owner", path, l.multi, l.sa, l.sb)
	l.downloadAllExpect(t, "m_dl", path, l.multi, l.sa, l.sb)
	l.downloadAllExpect(t, "m_part", path, l.multi, l.sa) // library two is not theirs
	l.downloadAllExpect(t, "g_dl", path, l.multi, l.sa)
	// A listener without a download grant sees the series page but gets nothing to download.
	if code, _ := l.downloadAllGet(t, "m_nodl", path); code != http.StatusNotFound {
		t.Errorf("listener without the download grant = %d, want 404", code)
	}
	if code, _ := l.downloadAllGet(t, "owner", "/series/ser_none/download-list"); code != http.StatusNotFound {
		t.Errorf("unknown series = %d, want 404", code)
	}
	if rec := l.a.do(http.MethodGet, path, nil, proxyDefault); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous = %d, want 303", rec.Code)
	}
	// Methods other than GET are not routed.
	if rec := l.a.do(http.MethodPost, path, nil, proxyDefault, l.cookies["owner"]); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("POST = %d, want it refused", rec.Code)
	}
}

// TestDownloadAllShelfList: a shelf lists everything downloadable on it, in shelf order, past the page size of 48;
// another person's private book never shows; a copy-protected, refused or audio-less book is left out.
func TestDownloadAllShelfList(t *testing.T) {
	l := newFeedLab(t)
	sh := l.downloadAllShelf(t, "owner", l.sb, l.protected, l.single, l.refused, l.tx, l.multi)
	path := "/shelves/" + sh.ID + "/download-list"
	l.downloadAllExpect(t, "owner", path, l.sb, l.single, l.multi)
	// Someone else's shelf is the plain 404 until it is shared.
	for _, who := range []string{"m_dl", "m_part", "m_nodl", "g_dl"} {
		if code, _ := l.downloadAllGet(t, who, path); code != http.StatusNotFound {
			t.Errorf("%s on a private shelf = %d, want 404", who, code)
		}
	}
	if err := l.a.lib.SetShelfShared(context.Background(), l.ids["owner"], sh.ID, true); err != nil {
		t.Fatal(err)
	}
	l.downloadAllExpect(t, "m_dl", path, l.sb, l.single, l.multi)
	l.downloadAllExpect(t, "m_part", path, l.single, l.multi) // the book of library two is the owner's alone
	l.downloadAllExpect(t, "m_nodl", path)                    // may see the shelf, may download nothing
	if code, _ := l.downloadAllGet(t, "owner", "/shelves/shf_none/download-list"); code != http.StatusNotFound {
		t.Errorf("unknown shelf = %d, want 404", code)
	}
}

// TestDownloadAllShelfLongerThanAPage: the page shows 48 books; the list reads the whole shelf.
func TestDownloadAllShelfLongerThanAPage(t *testing.T) {
	l := newFeedLab(t)
	_, folder := librarytest.NewLibrary(t, l.a.lib, "Download all long", adminAudioDir(t, true))
	var books []library.Book
	for i := 0; i < shelfPageSize+3; i++ {
		books = append(books, librarytest.AddBook(t, l.a.lib, folder, librarytest.BookSpec{Title: fmt.Sprintf("Long %03d", i), Files: 1}))
	}
	sh := l.downloadAllShelf(t, "owner", books...)
	code, got := l.downloadAllGet(t, "owner", "/shelves/"+sh.ID+"/download-list")
	if code != http.StatusOK || len(got.Books) != len(books) {
		t.Fatalf("long shelf = %d with %d books, want %d", code, len(got.Books), len(books))
	}
	for i, b := range books {
		if got.Books[i].ID != b.ID {
			t.Fatalf("book %d is %s, want %s", i, got.Books[i].ID, b.ID)
		}
	}
}

// TestDownloadAllContributor: a contributor may download only the books their own uploads placed.
func TestDownloadAllContributor(t *testing.T) {
	l := newFeedLab(t)
	l.exec(t, `UPDATE users SET adds_books = 1 WHERE id = ?`, l.ids["m_nodl"])
	l.exec(t, `DELETE FROM grants WHERE user_id = ?`, l.ids["m_nodl"])
	l.exec(t, `INSERT INTO upload_books(root_path, upload_id, library_id, added_by, created_at) VALUES (?, 'up1', ?, ?, 1)`, l.single.RootPath, l.lib1.ID, l.ids["m_nodl"])
	sh := l.downloadAllShelf(t, "m_nodl", l.single)
	l.downloadAllExpect(t, "m_nodl", "/shelves/"+sh.ID+"/download-list", l.single)
	if err := l.a.lib.AddToShelf(context.Background(), l.ids["m_nodl"], sh.ID, l.multi.ID); err == nil {
		l.downloadAllExpect(t, "m_nodl", "/shelves/"+sh.ID+"/download-list", l.single)
	}
}

// TestDownloadAllNeedsASession: a device token is not a session, so it never reaches the two pages' routes.
func TestDownloadAllNeedsASession(t *testing.T) {
	l := newFeedLab(t)
	sh := l.downloadAllShelf(t, "owner", l.single)
	var tok string
	err := l.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, _, err = auth.NewDevices(l.a.st, nil).Issue(context.Background(), tx, l.ids["owner"], "Download all test", auth.KindMobile)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/series/" + l.seriesID + "/download-list", "/shelves/" + sh.ID + "/download-list"} {
		rec := deviceBearerReq(l.a, http.MethodGet, p, tok, nil)
		if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), l.single.ID) {
			t.Errorf("a device token on %s = %d", p, rec.Code)
		}
	}
}

// TestDownloadAllButton: the control is on the series and shelf pages only for a person who may download something there.
func TestDownloadAllButton(t *testing.T) {
	l := newFeedLab(t)
	const marker = `data-ts-download-all="`
	series := "/series/" + l.seriesID
	for who, want := range map[string]bool{"owner": true, "m_dl": true, "m_part": true, "g_dl": true, "m_nodl": false} {
		code, body := l.page(who, series)
		if code != http.StatusOK {
			t.Fatalf("%s %s = %d", who, series, code)
		}
		if got := strings.Contains(body, marker+series+`/download-list"`); got != want {
			t.Errorf("%s: the series page shows Download all = %v, want %v", who, got, want)
		}
	}
	_, body := l.page("owner", series)
	for _, want := range []string{"Download all", "Download {count} books?", "About {size}. Books you already have are skipped.", "Downloading {done} of {total}", `data-ts-dlall-stop`, `data-ts-dlall-dialog`} {
		if !strings.Contains(body, want) {
			t.Errorf("series page lacks %q", want)
		}
	}
	if !strings.Contains(body, "hidden") || strings.Contains(body, `<div class="ts-dlall">`) {
		t.Error("the control starts visible, so it would show without JavaScript")
	}

	mixed := l.downloadAllShelf(t, "owner", l.protected, l.refused, l.tx)
	if _, body := l.page("owner", "/shelves/"+mixed.ID); strings.Contains(body, marker) {
		t.Error("a shelf with nothing to download shows Download all")
	}
	good := l.downloadAllShelf(t, "m_dl", l.single)
	if _, body := l.page("m_dl", "/shelves/"+good.ID); !strings.Contains(body, marker+"/shelves/"+good.ID+`/download-list"`) {
		t.Error("a shelf with a downloadable book lacks Download all")
	}
	if err := l.a.lib.SetShelfShared(context.Background(), l.ids["m_dl"], good.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, body := l.page("m_nodl", "/shelves/"+good.ID); strings.Contains(body, marker) {
		t.Error("a listener without the download grant sees Download all on a shared shelf")
	}
}
