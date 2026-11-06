package web

import (
	"context"
	"crypto/md5" // #nosec G501 -- KOReader sends the MD5 of the key; the test does what it does
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// kosLab is a ready three-chapter EPUB in a library, and a person with a KOReader key for each case:
// owner and reader may read it, noRead holds a grant without reading.
type kosLab struct {
	*rsLab
	ids     map[string]string // owner, reader, noRead
	secrets map[string]string
	digest  string
	units   int
}

func kosEPUB(t *testing.T) []byte {
	t.Helper()
	para := func(w string) string { return strings.Repeat(w+" is a word that fills a line of this chapter. ", 30) }
	return ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Kosync Book", Authors: []string{"K. Author"},
		Chapters: []ebooktest.EPUBChapter{
			{Title: "One", Paras: []string{para("alpha"), para("bravo")}},
			{Title: "Two", Paras: []string{para("charlie"), para("delta")}},
			{Title: "Three", Paras: []string{para("echo"), para("foxtrot")}},
		}})
}

func newKosLab(t *testing.T) *kosLab {
	t.Helper()
	l := &kosLab{rsLab: rsSeed(t, kosEPUB(t)), ids: map[string]string{}, secrets: map[string]string{}}
	l.ids["owner"] = l.ownerID
	l.ids["reader"] = sweepUserID(t, l.a, l.reader)
	l.ids["noRead"] = sweepUserID(t, l.a, l.noRead)
	for who, id := range l.ids {
		l.secrets[who] = l.kosKey(t, id, auth.KindKOReader)
	}
	if err := l.a.st.Reader().QueryRow(`SELECT partial_md5 FROM ebook_files WHERE id = ?`, l.file.ID).Scan(&l.digest); err != nil || len(l.digest) != 32 {
		t.Fatalf("the uploaded file has no digest: %q %v", l.digest, err)
	}
	l.units = l.doc.TotalUnits()
	return l
}

func (l *kosLab) kosKey(t *testing.T, userID string, kind auth.DeviceKind) string {
	t.Helper()
	var secret string
	err := l.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		secret, _, err = l.a.srv.devices.IssueKey(context.Background(), tx, userID, "", kind)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func kosMD5(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// kos sends one KOReader request. user and key are the two header values (the key already an MD5).
func (l *kosLab) kos(method, path, user, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Accept", "application/vnd.koreader.v1+json")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if user != "" {
		req.Header.Set("x-auth-user", user)
	}
	if key != "" {
		req.Header.Set("x-auth-key", key)
	}
	rec := httptest.NewRecorder()
	l.a.h.ServeHTTP(rec, req)
	return rec
}

// as sends a request as a lab person with their own key.
func (l *kosLab) as(who, method, path, body string) *httptest.ResponseRecorder {
	return l.kos(method, path, l.userName(who), kosMD5(l.secrets[who]), body)
}

func (l *kosLab) userName(who string) string {
	var n string
	if err := l.a.st.Reader().QueryRow(`SELECT username FROM users WHERE id = ?`, l.ids[who]).Scan(&n); err != nil {
		panic(err)
	}
	return n
}

func kosPut(doc, progressText string, pct float64, device string) string {
	return fmt.Sprintf(`{"document":%q,"progress":%q,"percentage":%v,"device":%q,"device_id":"%s-id-0001"}`, doc, progressText, pct, device, device)
}

func kosDecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return m
}

// reading is the stored reading place of a lab person in the lab book.
func (l *kosLab) reading(who string) (progress.ReadingPosition, bool) {
	p, ok, err := l.a.srv.progress.Reading(context.Background(), l.ids[who], l.book.ID)
	if err != nil {
		panic(err)
	}
	return p, ok
}

// chapterStart is the number of units before chapter n (from 1) in the lab's document.
func (l *kosLab) chapterStart(n int) int {
	total := 0
	for _, c := range l.doc.Chapters {
		if c.Idx == n {
			return total
		}
		for _, b := range c.Blocks {
			total += len(b.Text) // the lab's text is ASCII
		}
	}
	return total
}

func (l *kosLab) rowCount(table string) int {
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		panic(err)
	}
	return n
}

func TestKosyncRefusals(t *testing.T) {
	l := newKosLab(t)
	owner, key := l.userName("owner"), kosMD5(l.secrets["owner"])
	opdsKey := l.kosKey(t, l.ids["owner"], auth.KindOPDS)
	revoked := l.kosKey(t, l.ids["reader"], auth.KindKOReader)
	var revokedID string
	hash := auth.KeyHash(auth.KindKOReader, revoked)
	if err := l.a.st.Reader().QueryRow(`SELECT id FROM devices WHERE token_hash = ?`, hash[:]).Scan(&revokedID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.a.srv.devices.Revoke(context.Background(), l.a.st.Writer(), l.ids["reader"], revokedID, l.a.srv.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if rec := l.as("owner", http.MethodGet, "/kosync/users/auth", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"authorized":"OK"`) {
		t.Fatalf("the right key = %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct{ name, user, key string }{
		{"no headers", "", ""},
		{"no key", owner, ""},
		{"no user", "", key},
		{"wrong key", owner, kosMD5("not the key")},
		{"key not hex", owner, "zzzz"},
		{"the login password", owner, kosMD5(testPassword)},
		{"the key typed instead of its MD5", owner, l.secrets["owner"]},
		{"an opds key", owner, kosMD5(opdsKey)},
		{"another person's name", l.userName("reader"), key},
		{"a revoked key", l.userName("reader"), kosMD5(revoked)},
	} {
		l.a.srv.keyLimiter.Reset("192.0.2.1") // the limiter has its own test; here every case must reach the key check
		for _, req := range []struct{ method, path, body string }{
			{http.MethodGet, "/kosync/users/auth", ""},
			{http.MethodGet, "/kosync/syncs/progress/" + l.digest, ""},
			{http.MethodPut, "/kosync/syncs/progress", kosPut(l.digest, "5", 0.5, "Kobo")},
		} {
			rec := l.kos(req.method, req.path, tc.user, tc.key, req.body)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":2001`) {
				t.Errorf("%s %s %s = %d %s, want 401 with code 2001", tc.name, req.method, req.path, rec.Code, rec.Body)
			}
		}
	}
	if n := l.rowCount("kosync_progress"); n != 0 {
		t.Errorf("a refused request stored %d places", n)
	}
	// A kosync key opens nothing else: not a page, not the API, not the OPDS feed.
	basic := func(r *http.Request) { r.SetBasicAuth(owner, l.secrets["owner"]) }
	for _, p := range []string{"/opds", "/api/v1/me", "/"} {
		if rec := l.a.do(http.MethodGet, p, nil, basic); rec.Code == http.StatusOK {
			t.Errorf("a koreader key opened %s", p)
		}
	}
}

func TestKosyncLimiter(t *testing.T) {
	l := newKosLab(t)
	owner := l.userName("owner")
	for i := 0; i < keyFailMax; i++ {
		if rec := l.kos(http.MethodGet, "/kosync/users/auth", owner, kosMD5(fmt.Sprint("wrong", i)), ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong key %d = %d", i, rec.Code)
		}
	}
	rec := l.as("owner", http.MethodGet, "/kosync/users/auth", "")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("after the limit = %d (Retry-After %q)", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestKosyncCreateMakesNobody(t *testing.T) {
	l := newKosLab(t)
	before := l.rowCount("users")
	for _, user := range []string{"", "newcomer", l.userName("owner")} {
		rec := l.kos(http.MethodPost, "/kosync/users/create", user, kosMD5("whatever"), `{"username":"newcomer","password":"`+kosMD5("x")+`"}`)
		if rec.Code != http.StatusPaymentRequired || !strings.Contains(rec.Body.String(), "Make a KOReader key in Tendshelf") {
			t.Errorf("create as %q = %d %s, want 402", user, rec.Code, rec.Body)
		}
	}
	if after := l.rowCount("users"); after != before {
		t.Errorf("users %d -> %d", before, after)
	}
}

func TestKosyncTrailingSlash(t *testing.T) {
	l := newKosLab(t)
	// A server address typed with a trailing slash sends the path with two slashes; it is served, not redirected.
	for _, p := range []string{"/kosync//users/auth", "/kosync///users//auth", "/kosync/users/auth"} {
		if rec := l.as("owner", http.MethodGet, p, ""); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d %s", p, rec.Code, rec.Body)
		}
	}
	rec := l.as("owner", http.MethodPut, "/kosync//syncs/progress", kosPut(l.digest, "/body/DocFragment[2]/body", 0.4, "Kobo"))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	if rec := l.as("owner", http.MethodGet, "/kosync//syncs/progress/"+l.digest, ""); rec.Code != http.StatusOK || kosDecode(t, rec)["progress"] != "/body/DocFragment[2]/body" {
		t.Errorf("GET = %d %s", rec.Code, rec.Body)
	}
	if rec := l.as("owner", http.MethodGet, "/kosync/nothing", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown path = %d", rec.Code)
	}
	if rec := l.as("owner", http.MethodDelete, "/kosync/syncs/progress", ""); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Errorf("delete = %d", rec.Code)
	}
}

func TestKosyncFields(t *testing.T) {
	l := newKosLab(t)
	long := strings.Repeat("x", 513)
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"all good", kosPut(l.digest, "7", 0.5, "Kobo"), 200},
		{"upper-case document", kosPut(strings.ToUpper(l.digest), "7", 0.5, "Kobo"), 200},
		{"zero and one", kosPut(l.digest, "7", 0, "Kobo"), 200},
		{"one", kosPut(l.digest, "7", 1, "Kobo"), 200},
		{"not json", `nope`, 400},
		{"empty body", ``, 400},
		{"short document", kosPut("abc", "7", 0.5, "Kobo"), 400},
		{"document not hex", kosPut(strings.Repeat("g", 32), "7", 0.5, "Kobo"), 400},
		{"percentage above one", kosPut(l.digest, "7", 1.5, "Kobo"), 400},
		{"percentage below zero", kosPut(l.digest, "7", -0.1, "Kobo"), 400},
		{"percentage missing", `{"document":"` + l.digest + `","progress":"7","device":"d","device_id":"d"}`, 400},
		{"percentage a string", `{"document":"` + l.digest + `","progress":"7","percentage":"x","device":"d","device_id":"d"}`, 400},
		{"progress empty", kosPut(l.digest, "", 0.5, "Kobo"), 400},
		{"progress too long", kosPut(l.digest, long, 0.5, "Kobo"), 400},
		{"device too long", kosPut(l.digest, "7", 0.5, strings.Repeat("d", 65)), 400},
		{"body too big", `{"document":"` + l.digest + `","progress":"7","percentage":0.5,"pad":"` + strings.Repeat("p", 9000) + `"}`, 400},
	} {
		rec := l.as("owner", http.MethodPut, "/kosync/syncs/progress", tc.body)
		if rec.Code != tc.want {
			t.Errorf("%s = %d %s, want %d", tc.name, rec.Code, rec.Body, tc.want)
		}
		if tc.want == 400 && !strings.Contains(rec.Body.String(), `"code":2003`) {
			t.Errorf("%s: body %s", tc.name, rec.Body)
		}
	}
	if rec := l.as("owner", http.MethodGet, "/kosync/syncs/progress/short", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("GET with a short document = %d", rec.Code)
	}
}

func TestKosyncUnknownDocumentIsStoredOnly(t *testing.T) {
	l := newKosLab(t)
	doc := strings.Repeat("ab", 16)
	rec := l.as("owner", http.MethodPut, "/kosync/syncs/progress", kosPut(doc, "/body/DocFragment[9]/body/p[4]", 0.33, "Boox"))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	if m := kosDecode(t, rec); m["document"] != doc || m["timestamp"] == nil {
		t.Errorf("answer = %v", m)
	}
	if n := l.rowCount("reading_positions"); n != 0 {
		t.Errorf("reading_positions = %d rows, want none", n)
	}
	got := kosDecode(t, l.as("owner", http.MethodGet, "/kosync/syncs/progress/"+doc, ""))
	if got["progress"] != "/body/DocFragment[9]/body/p[4]" || got["percentage"] != 0.33 || got["device"] != "Boox" || got["device_id"] != "Boox-id-0001" {
		t.Errorf("GET = %v", got)
	}
	// It is that person's: nobody else sees it.
	if rec := l.as("reader", http.MethodGet, "/kosync/syncs/progress/"+doc, ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Errorf("another person's GET = %d %s, want {}", rec.Code, rec.Body)
	}
	if rec := l.as("owner", http.MethodGet, "/kosync/syncs/progress/"+strings.Repeat("cd", 16), ""); strings.TrimSpace(rec.Body.String()) != "{}" || rec.Code != 200 {
		t.Errorf("no row = %d %s, want {}", rec.Code, rec.Body)
	}
}

func TestKosyncPersonWithoutReadingRightKeepsOnlyTheirRow(t *testing.T) {
	l := newKosLab(t)
	rec := l.as("noRead", http.MethodPut, "/kosync/syncs/progress", kosPut(l.digest, "/body/DocFragment[2]/body", 0.5, "Kobo"))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	if n := l.rowCount("reading_positions"); n != 0 {
		t.Errorf("a person who may not read the book got a reading place (%d rows)", n)
	}
	if n := l.rowCount("kosync_progress"); n != 1 {
		t.Errorf("kosync rows = %d, want 1", n)
	}
	// What a person with no right reads back is only their own KOReader row, never the Tendshelf place of the book.
	if _, _, err := l.a.srv.progress.SaveReading(context.Background(), progress.ReadingSave{UserID: l.ids["owner"], BookID: l.book.ID, EditionID: l.ed.ID,
		ChapterIdx: 3, ClientUpdatedAt: l.a.srv.now().UnixMilli() + 5000, DeviceID: "web-aaaaaaaa", PercentBP: 9000}); err != nil {
		t.Fatal(err)
	}
	got := kosDecode(t, l.as("noRead", http.MethodGet, "/kosync/syncs/progress/"+l.digest, ""))
	if got["device"] != "Kobo" || got["progress"] != "/body/DocFragment[2]/body" {
		t.Errorf("noRead GET = %v", got)
	}
}

func TestKosyncInboundLandsInTheRightChapter(t *testing.T) {
	l := newKosLab(t)
	pct := float64(l.chapterStart(3)+5) / float64(l.units)
	rec := l.as("reader", http.MethodPut, "/kosync/syncs/progress", kosPut(l.digest, "/body/DocFragment[3]/body/p[1]", pct, "Kobo"))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	p, ok := l.reading("reader")
	if !ok {
		t.Fatal("no reading place was saved")
	}
	if p.ChapterIdx != 3 || p.DeviceLabel != "KOReader" || !strings.HasPrefix(p.DeviceID, "ko-") || len(p.DeviceID) != 19 || p.EditionID != l.ed.ID {
		t.Errorf("place = %+v", p)
	}
	if want := int(pct*10000 + 0.5); p.PercentBP != want {
		t.Errorf("percent_bp = %d, want %d", p.PercentBP, want)
	}
	if p.CharPos < l.chapterStart(3) || p.CharPos > l.chapterStart(3)+20 {
		t.Errorf("char_pos = %d, chapter three starts at %d", p.CharPos, l.chapterStart(3))
	}
	if _, ok := l.reading("owner"); ok {
		t.Error("another person's place was written")
	}
	// The start and the end of the book.
	time.Sleep(2 * time.Millisecond) // the newest place wins, and two writes in one millisecond tie
	l.as("reader", http.MethodPut, "/kosync/syncs/progress", kosPut(l.digest, "1", 0, "Kobo"))
	if p, _ := l.reading("reader"); p.ChapterIdx != 1 {
		t.Errorf("at 0 the chapter is %d", p.ChapterIdx)
	}
	time.Sleep(2 * time.Millisecond) // the newest place wins, and two writes in one millisecond tie
	l.as("reader", http.MethodPut, "/kosync/syncs/progress", kosPut(l.digest, "9", 1, "Kobo"))
	if p, _ := l.reading("reader"); p.ChapterIdx != 3 || p.PercentBP != 10000 {
		t.Errorf("at 1 the place is %+v", p)
	}
}

func TestKosyncOutboundWhenTendshelfIsNewer(t *testing.T) {
	l := newKosLab(t)
	ctx := context.Background()
	l.as("reader", http.MethodPut, "/kosync/syncs/progress", kosPut(l.digest, "/body/DocFragment[1]/body", 0.05, "Kobo"))
	// Nothing newer: KOReader's own pointer comes back.
	if got := kosDecode(t, l.as("reader", http.MethodGet, "/kosync/syncs/progress/"+l.digest, "")); got["device"] != "Kobo" {
		t.Fatalf("before = %v", got)
	}
	newer := l.a.srv.now().UnixMilli() + 30_000
	if _, _, err := l.a.srv.progress.SaveReading(ctx, progress.ReadingSave{UserID: l.ids["reader"], BookID: l.book.ID, EditionID: l.ed.ID,
		ChapterIdx: 2, ClientUpdatedAt: newer, DeviceID: "web-aaaaaaaa", PercentBP: 5000}); err != nil {
		t.Fatal(err)
	}
	got := kosDecode(t, l.as("reader", http.MethodGet, "/kosync/syncs/progress/"+l.digest, ""))
	if got["progress"] != "/body/DocFragment[2]/body" || got["device"] != "Tendshelf" || got["device_id"] != "tendshelf" || got["percentage"] != 0.5 {
		t.Errorf("epub = %v", got)
	}
	if ts, _ := got["timestamp"].(float64); int64(ts) != newer/1000 {
		t.Errorf("timestamp = %v, want %d", got["timestamp"], newer/1000)
	}

	// The same file read as a PDF: a page.
	if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET format = 'pdf' WHERE id = ?`, l.file.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE editions SET page_count = 120 WHERE id = ?`, l.ed.ID); err != nil {
		t.Fatal(err)
	}
	if got := kosDecode(t, l.as("reader", http.MethodGet, "/kosync/syncs/progress/"+l.digest, "")); got["progress"] != "60" || got["device"] != "Tendshelf" {
		t.Errorf("pdf = %v", got)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE editions SET page_count = 0 WHERE id = ?`, l.ed.ID); err != nil {
		t.Fatal(err)
	}
	if got := kosDecode(t, l.as("reader", http.MethodGet, "/kosync/syncs/progress/"+l.digest, "")); got["device"] != "Kobo" {
		t.Errorf("pdf without a page count = %v, want the KOReader row", got)
	}

	// Any other format answers the last KOReader row.
	if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET format = 'mobi' WHERE id = ?`, l.file.ID); err != nil {
		t.Fatal(err)
	}
	if got := kosDecode(t, l.as("reader", http.MethodGet, "/kosync/syncs/progress/"+l.digest, "")); got["device"] != "Kobo" || got["progress"] != "/body/DocFragment[1]/body" {
		t.Errorf("mobi = %v", got)
	}
	// ... or {} when KOReader never sent one.
	if rec := l.as("owner", http.MethodGet, "/kosync/syncs/progress/"+l.digest, ""); strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Errorf("mobi without a row = %s", rec.Body)
	}
}

func TestKosyncOutboundWithoutAKoreaderRow(t *testing.T) {
	l := newKosLab(t)
	if _, _, err := l.a.srv.progress.SaveReading(context.Background(), progress.ReadingSave{UserID: l.ids["reader"], BookID: l.book.ID, EditionID: l.ed.ID,
		ChapterIdx: 3, ClientUpdatedAt: l.a.srv.now().UnixMilli(), DeviceID: "web-aaaaaaaa", PercentBP: 7000}); err != nil {
		t.Fatal(err)
	}
	got := kosDecode(t, l.as("reader", http.MethodGet, "/kosync/syncs/progress/"+l.digest, ""))
	if got["progress"] != "/body/DocFragment[3]/body" || got["device"] != "Tendshelf" {
		t.Errorf("GET = %v", got)
	}
	// A person with no right to the book gets nothing from it.
	if rec := l.as("noRead", http.MethodGet, "/kosync/syncs/progress/"+l.digest, ""); strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Errorf("noRead GET = %s", rec.Body)
	}
}

func TestKosyncNewestWinsBothWays(t *testing.T) {
	l := newKosLab(t)
	ctx := context.Background()
	now := l.a.srv.now().UnixMilli()
	// Tendshelf holds a newer place: the PUT from KOReader is stored in its row, but the reading place stays.
	if _, _, err := l.a.srv.progress.SaveReading(ctx, progress.ReadingSave{UserID: l.ids["reader"], BookID: l.book.ID, EditionID: l.ed.ID,
		ChapterIdx: 2, ClientUpdatedAt: now + 60_000, DeviceID: "web-aaaaaaaa", PercentBP: 5000}); err != nil {
		t.Fatal(err)
	}
	l.as("reader", http.MethodPut, "/kosync/syncs/progress", kosPut(l.digest, "/body/DocFragment[1]/body", 0.01, "Kobo"))
	if p, _ := l.reading("reader"); p.ChapterIdx != 2 || p.DeviceID != "web-aaaaaaaa" {
		t.Errorf("an older KOReader place replaced a newer Tendshelf one: %+v", p)
	}
	// And Tendshelf's newer place is what KOReader gets back.
	if got := kosDecode(t, l.as("reader", http.MethodGet, "/kosync/syncs/progress/"+l.digest, "")); got["device"] != "Tendshelf" {
		t.Errorf("GET = %v", got)
	}
	// An older Tendshelf place never beats a newer KOReader one.
	if _, err := l.a.st.Writer().Exec(`UPDATE reading_positions SET client_updated_at = 1 WHERE user_id = ?`, l.ids["reader"]); err != nil {
		t.Fatal(err)
	}
	l.as("reader", http.MethodPut, "/kosync/syncs/progress", kosPut(l.digest, "/body/DocFragment[3]/body", 0.9, "Kobo"))
	if p, _ := l.reading("reader"); p.ChapterIdx != 3 || p.DeviceLabel != "KOReader" {
		t.Errorf("a newer KOReader place did not land: %+v", p)
	}
	if got := kosDecode(t, l.as("reader", http.MethodGet, "/kosync/syncs/progress/"+l.digest, "")); got["device"] != "Kobo" {
		t.Errorf("GET = %v", got)
	}
}

func TestKosyncKeysOfTwoPeopleStayApart(t *testing.T) {
	l := newKosLab(t)
	doc := strings.Repeat("ef", 16)
	l.as("owner", http.MethodPut, "/kosync/syncs/progress", kosPut(doc, "1", 0.1, "Kobo"))
	l.as("reader", http.MethodPut, "/kosync/syncs/progress", kosPut(doc, "2", 0.2, "Boox"))
	if got := kosDecode(t, l.as("owner", http.MethodGet, "/kosync/syncs/progress/"+doc, "")); got["progress"] != "1" {
		t.Errorf("owner sees %v", got)
	}
	if got := kosDecode(t, l.as("reader", http.MethodGet, "/kosync/syncs/progress/"+doc, "")); got["progress"] != "2" {
		t.Errorf("reader sees %v", got)
	}
}

func TestKosyncAnswerHeaders(t *testing.T) {
	l := newKosLab(t)
	rec := l.as("owner", http.MethodGet, "/kosync/users/auth", "")
	if rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Errorf("headers = %v", rec.Header())
	}
}

// TestKosyncTotalUnitsAgreesWithCharPos: the length the percent is multiplied by is the one the anchors count along.
func TestKosyncTotalUnitsAgreesWithCharPos(t *testing.T) {
	l := newKosLab(t)
	a, ok := l.doc.AnchorAtChar(l.units / 2)
	if !ok {
		t.Fatal("no anchor")
	}
	pos, total, ok := l.doc.CharPos(a)
	if !ok || total != l.units || pos > l.units/2 || l.units/2-pos > 5000 {
		t.Errorf("CharPos = %d of %d (ok %v), TotalUnits = %d", pos, total, ok, l.units)
	}
}
