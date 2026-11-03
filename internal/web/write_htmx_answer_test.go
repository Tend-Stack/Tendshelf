package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// jobInSheet reads the job id the sheet in an htmx answer polls.
func jobInSheet(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, "/hx/jobs/")
	if i < 0 {
		t.Fatalf("no job poll in the answer: %.300s", body)
	}
	rest := body[i+len("/hx/jobs/"):]
	return rest[:strings.IndexAny(rest, `"?`)]
}

// An htmx POST from the write sheet must never answer HX-Redirect or a 3xx: that loads the whole
// window and stops the audio playing in the layout. It answers 200 with the Details page, from
// which the form swaps the sheet or the panel.
func TestWriteHTMXAnswersWithoutRedirect(t *testing.T) {
	l := newWriteLab(t, 1, false)
	l.start(t)
	form := url.Values{"_csrf": {l.a.tokenFor(t, l.owner)}}
	start := func() {
		t.Helper()
		rec := l.a.do(http.MethodPost, "/books/"+l.book.ID+"/write", form, hx, l.owner)
		if rec.Code != http.StatusOK || rec.Header().Get("HX-Redirect") != "" || rec.Header().Get("Location") != "" {
			t.Fatalf("start: %d, HX-Redirect %q, Location %q", rec.Code, rec.Header().Get("HX-Redirect"), rec.Header().Get("Location"))
		}
		body := rec.Body.String()
		if !strings.Contains(body, `id="write-body"`) {
			t.Fatalf("start: the answer does not carry the sheet that follows the job: %.300s", body)
		}
		l.waitDone(t, l.owner, jobInSheet(t, body))
	}
	for _, c := range []struct{ path, note string }{{"/undo", writeUndone}, {"/confirm", writeKept}} {
		start()
		rec := l.a.do(http.MethodPost, "/books/"+l.book.ID+"/write"+c.path, form, hx, l.owner)
		if rec.Code != http.StatusOK || rec.Header().Get("HX-Redirect") != "" {
			t.Fatalf("%s: %d, HX-Redirect %q", c.path, rec.Code, rec.Header().Get("HX-Redirect"))
		}
		if body := rec.Body.String(); !strings.Contains(body, c.note) || strings.Contains(body, "ts-write__actions") {
			t.Errorf("%s: the answer should show %q and no waiting panel", c.path, c.note)
		}
	}
}
