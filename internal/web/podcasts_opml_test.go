package web

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// multipartPost sends a multipart form with one file as a signed-in person.
func (w *podWorld) multipartPost(t *testing.T, path string, ck *http.Cookie, file []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("_csrf", w.a.tokenFor(t, ck))
	if file != nil {
		fw, _ := mw.CreateFormFile("file", "podcasts.opml")
		fw.Write(file)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(ck)
	rec := httptest.NewRecorder()
	w.a.h.ServeHTTP(rec, req)
	return rec
}

func TestPodcastOPMLImport(t *testing.T) {
	w := newPodWorld(t)
	opmlDoc := fmt.Sprintf(`<?xml version="1.0"?><opml version="2.0"><body><outline text="All"><outline type="rss" text="Other" xmlUrl=%q/>
		<outline type="rss" text="Main" xmlUrl=%q/><outline type="rss" text="Bad" xmlUrl="ftp://x.example/f"/></outline></body></opml>`, w.fx.otherFeedURL(), w.fx.feedURL())
	rec := w.multipartPost(t, "/podcasts/opml", w.cy, []byte(opmlDoc))
	if rec.Code != http.StatusSeeOther || location(rec) != "/podcasts" {
		t.Fatalf("import: %d %q", rec.Code, rec.Body)
	}
	var payload string
	if err := w.a.st.Reader().QueryRow(`SELECT payload_json FROM jobs WHERE kind = ?`, podcast.KindImport).Scan(&payload); err != nil || !strings.Contains(payload, w.cyID) {
		t.Fatalf("import job: %v %q", err, payload)
	}
	var audits int
	w.a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'podcast.import'`).Scan(&audits)
	if audits != 1 {
		t.Errorf("import audit rows = %d", audits)
	}
	// Run it: the two good addresses are followed, the third is skipped.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.a.runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		w.a.runner.Stop(stop)
	})
	waitFor(t, "the import", func() bool {
		shows, _ := w.fx.svc.Shows(ctx, w.cyID)
		return len(shows) == 2
	})
	// Refused files: no file, not OPML, too big, a guest.
	if rec = w.multipartPost(t, "/podcasts/opml", w.cy, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("no file: %d", rec.Code)
	}
	if rec = w.multipartPost(t, "/podcasts/opml", w.cy, []byte("<html>not opml</html>")); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("not opml: %d", rec.Code)
	}
	if rec = w.multipartPost(t, "/podcasts/opml", w.cy, bytes.Repeat([]byte("x"), 2<<20)); rec.Code != http.StatusForbidden && rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("too big: %d", rec.Code)
	}
	if rec = w.multipartPost(t, "/podcasts/opml", w.gus, []byte(opmlDoc)); rec.Code != http.StatusNotFound {
		t.Errorf("guest: %d", rec.Code)
	}
}
