package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// The app's settings, per-book speed, Continue shelf and series-next (A1, decision 0026).

type a1Settings struct {
	Speed        float64 `json:"speed"`
	SkipBackS    int     `json:"skipBackS"`
	SkipForwardS int     `json:"skipForwardS"`
	AutoRewind   bool    `json:"autoRewind"`
	NextInSeries bool    `json:"nextInSeries"`
}

func (e *syncEnv) a1Settings(t *testing.T, tok string) a1Settings {
	t.Helper()
	var s a1Settings
	rec := e.send(tok, http.MethodGet, "/api/v1/settings", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET settings = %d %v", rec.Code, rec.Header())
	}
	apiDecode(t, rec, &s)
	return s
}

// S3: the table is the one in rewind.go, read-only (a PATCH value is ignored).
func TestA5SettingsRewindSteps(t *testing.T) {
	e := syncSeed(t)
	type steps struct {
		RewindSteps []struct {
			FromMs  int64 `json:"fromMs"`
			Seconds int   `json:"seconds"`
		} `json:"rewindSteps"`
	}
	read := func(rec *httptest.ResponseRecorder) steps {
		var s steps
		apiDecode(t, rec, &s)
		return s
	}
	check := func(label string, s steps) {
		if len(s.RewindSteps) != len(rewindSteps) {
			t.Fatalf("%s: rewindSteps = %+v", label, s.RewindSteps)
		}
		for i, want := range rewindSteps {
			if got := s.RewindSteps[i]; got.FromMs != want.From.Milliseconds() || got.Seconds != want.Seconds {
				t.Errorf("%s: step %d = %+v, want %v/%d", label, i, got, want.From, want.Seconds)
			}
		}
	}
	check("get", read(e.send(e.owner, http.MethodGet, "/api/v1/settings", "")))
	check("patch", read(e.send(e.owner, http.MethodPatch, "/api/v1/settings", `{"rewindSteps":[{"fromMs":1,"seconds":99}]}`)))
	check("after patch", read(e.send(e.owner, http.MethodGet, "/api/v1/settings", "")))
}

func TestA1SettingsReadAndPatch(t *testing.T) {
	e := syncSeed(t)
	if got := e.a1Settings(t, e.owner); got != (a1Settings{Speed: 1, SkipBackS: 15, SkipForwardS: 30, AutoRewind: true, NextInSeries: true}) {
		t.Fatalf("defaults = %+v", got)
	}
	// The answer holds the player keys and nothing else.
	var keys map[string]any
	apiDecode(t, e.send(e.owner, http.MethodGet, "/api/v1/settings", ""), &keys)
	if len(keys) != 6 {
		t.Errorf("keys = %v", keys)
	}

	rec := e.send(e.owner, http.MethodPatch, "/api/v1/settings", `{"speed":1.25,"skipBackS":10,"skipForwardS":60,"autoRewind":false,"nextInSeries":false}`)
	var got a1Settings
	apiDecode(t, rec, &got)
	if rec.Code != http.StatusOK || got != (a1Settings{Speed: 1.25, SkipBackS: 10, SkipForwardS: 60}) {
		t.Fatalf("patch = %d %+v", rec.Code, got)
	}
	// Each key alone, the others untouched.
	e.send(e.owner, http.MethodPatch, "/api/v1/settings", `{"autoRewind":true}`)
	if s := e.a1Settings(t, e.owner); !s.AutoRewind || s.Speed != 1.25 || s.SkipBackS != 10 || s.NextInSeries {
		t.Errorf("one key = %+v", s)
	}
	// An empty patch and unknown keys are fine and change nothing; the theme is not writable here.
	if rec := e.send(e.owner, http.MethodPatch, "/api/v1/settings", `{}`); rec.Code != http.StatusOK {
		t.Errorf("empty patch = %d", rec.Code)
	}
	if rec := e.send(e.owner, http.MethodPatch, "/api/v1/settings", `{"theme":"dark","textSize":"large","sound":"warm","extra":1}`); rec.Code != http.StatusOK {
		t.Errorf("unknown keys = %d", rec.Code)
	}
	set, err := e.a.srv.users.LoadSettings(context.Background(), e.uid[sweepOwner])
	if err != nil || set.Theme != "system" || set.TextSize != "standard" {
		t.Errorf("theme or text size changed: %+v %v", set, err)
	}
	// The member's settings are their own.
	if s := e.a1Settings(t, e.member); s.Speed != 1 {
		t.Errorf("member sees the owner's speed: %+v", s)
	}
}

func TestA1SettingsBadValues(t *testing.T) {
	e := syncSeed(t)
	for _, c := range []struct{ body, field string }{
		{`{"speed":0.4}`, "speed"}, {`{"speed":3.5}`, "speed"}, {`{"speed":1.01}`, "speed"}, {`{"speed":"fast"}`, "speed"}, {`{"speed":null}`, "speed"},
		{`{"skipBackS":7}`, "skipBackS"}, {`{"skipBackS":15.5}`, "skipBackS"}, {`{"skipForwardS":"30"}`, "skipForwardS"},
		{`{"autoRewind":"yes"}`, "autoRewind"}, {`{"nextInSeries":1}`, "nextInSeries"},
		// The first bad key in the order of the answer is named, and nothing is saved.
		{`{"nextInSeries":2,"skipForwardS":7,"speed":9}`, "speed"},
		{`{"autoRewind":false,"skipForwardS":7}`, "skipForwardS"},
	} {
		rec := e.send(e.owner, http.MethodPatch, "/api/v1/settings", c.body)
		if rec.Code != http.StatusBadRequest || a1Field(t, rec) != c.field {
			t.Errorf("%s = %d field %q, want %q", c.body, rec.Code, a1Field(t, rec), c.field)
		}
	}
	if s := e.a1Settings(t, e.owner); !s.AutoRewind || s.Speed != 1 {
		t.Errorf("a refused patch saved something: %+v", s)
	}
	if rec := e.send(e.owner, http.MethodPatch, "/api/v1/settings", `{"speed":`+strings.Repeat("1", 5000)+`}`); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a large body = %d", rec.Code)
	}
	if rec := e.send(e.owner, http.MethodPatch, "/api/v1/settings", `[1]`); rec.Code != http.StatusBadRequest {
		t.Errorf("not an object = %d", rec.Code)
	}
}

func TestA1BookSpeed(t *testing.T) {
	e := syncSeed(t)
	id := e.extra[0]
	bookSpeed := func(tok string) float64 {
		var d struct {
			Speed float64 `json:"speed"`
		}
		rec := e.send(tok, http.MethodGet, "/api/v1/books/"+id, "")
		apiDecode(t, rec, &d)
		return d.Speed
	}
	if got := bookSpeed(e.owner); got != 1 {
		t.Fatalf("usual speed = %v", got)
	}
	if rec := e.send(e.owner, http.MethodPut, "/api/v1/books/"+id+"/speed", `{"speed":1.5}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	if bookSpeed(e.owner) != 1.5 || e.a1Settings(t, e.owner).Speed != 1 {
		t.Error("the book's own speed did not stay with the book")
	}
	if bookSpeed(e.member) != 1 {
		t.Error("the member sees the owner's book speed")
	}
	if rec := e.send(e.owner, http.MethodPut, "/api/v1/books/"+id+"/speed", `{"speed":2,"asUsual":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT asUsual = %d", rec.Code)
	}
	if bookSpeed(e.owner) != 2 || e.a1Settings(t, e.owner).Speed != 2 {
		t.Error("asUsual did not become the usual speed and drop the book's own")
	}
	for _, body := range []string{`{}`, `{"speed":0.3}`, `{"speed":2.02}`, `{"speed":null}`} {
		rec := e.send(e.owner, http.MethodPut, "/api/v1/books/"+id+"/speed", body)
		if rec.Code != http.StatusBadRequest || a1Field(t, rec) != "speed" {
			t.Errorf("%s = %d field %q", body, rec.Code, a1Field(t, rec))
		}
	}
	// Hidden and unknown books answer 404 whatever the body says.
	for _, path := range []string{"/api/v1/books/" + e.hidden.ID + "/speed", "/api/v1/books/bk_nothere/speed"} {
		if rec := e.send(e.member, http.MethodPut, path, `{"speed":1.5}`); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Hiddenword") {
			t.Errorf("PUT %s = %d", path, rec.Code)
		}
	}
	if e.a.count(t, `SELECT COUNT(*) FROM book_settings WHERE book_id = ?`, e.hidden.ID) != 0 {
		t.Error("a hidden book got a speed")
	}
}

func TestA1Continue(t *testing.T) {
	e := syncSeed(t)
	ids := []string{e.extra[0], e.extra[1]}
	for i, id := range ids {
		body := putBody(int64(5000+i), int64(1000+i), "")
		if rec := e.send(e.owner, http.MethodPut, "/api/v1/progress/"+id, body); rec.Code != http.StatusOK {
			t.Fatalf("progress = %d %s", rec.Code, rec.Body)
		}
	}
	var out struct {
		Items []struct {
			Book       struct{ ID string }
			PositionMs int64
			DurationMs int64
			UpdatedAt  int64
		}
	}
	rec := e.send(e.owner, http.MethodGet, "/api/v1/continue", "")
	apiDecode(t, rec, &out)
	// The newest place leads; the two just written follow in order of their times.
	pos := map[string]int{}
	for i, it := range out.Items {
		pos[it.Book.ID] = i + 1
	}
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || pos[ids[0]] == 0 || pos[ids[1]] == 0 ||
		pos[ids[1]] > pos[ids[0]] || out.Items[pos[ids[1]]-1].PositionMs != 5001 {
		t.Fatalf("continue = %d %+v", rec.Code, out)
	}
	apiDecode(t, e.send(e.owner, http.MethodGet, "/api/v1/continue?limit=1", ""), &out)
	if len(out.Items) != 1 {
		t.Errorf("limit 1 gave %d", len(out.Items))
	}
	if rec := e.send(e.owner, http.MethodGet, "/api/v1/continue?limit=24", ""); rec.Code != http.StatusOK {
		t.Errorf("limit 24 = %d", rec.Code)
	}
	for _, q := range []string{"0", "25", "-1", "abc", "1.5"} {
		rec := e.send(e.owner, http.MethodGet, "/api/v1/continue?limit="+q, "")
		if rec.Code != http.StatusBadRequest || a1Field(t, rec) != "limit" {
			t.Errorf("limit=%s = %d field %q", q, rec.Code, a1Field(t, rec))
		}
	}
	// The member has none of the owner's places, and a finished book is not on the shelf.
	apiDecode(t, e.send(e.member, http.MethodGet, "/api/v1/continue", ""), &out)
	for _, it := range out.Items {
		if it.Book.ID == ids[0] || it.Book.ID == ids[1] {
			t.Error("the member sees the owner's place")
		}
	}
	if _, err := e.a.st.Writer().Exec(`UPDATE progress SET finished_at = 1 WHERE book_id = ?`, ids[0]); err != nil {
		t.Fatal(err)
	}
	apiDecode(t, e.send(e.owner, http.MethodGet, "/api/v1/continue", ""), &out)
	for _, it := range out.Items {
		if it.Book.ID == ids[0] {
			t.Error("a finished book is on the shelf")
		}
	}
}

// a1Series makes three books of one series: two the member can open around one in a closed library.
func (e *syncEnv) a1Series(t *testing.T) (first, hidden, last library.Book) {
	t.Helper()
	folder, err := e.a.lib.Folder(context.Background(), e.folderID)
	if err != nil {
		t.Fatal(err)
	}
	_, closed := librarytest.NewLibrary(t, e.a.lib, "Closed two", adminAudioDir(t, true))
	first = librarytest.AddBook(t, e.a.lib, folder, librarytest.BookSpec{Title: "Roadone Alpha", Series: "Roadone Saga", Sequence: "1", DurationMs: 1000, Files: 1})
	hidden = librarytest.AddBook(t, e.a.lib, closed, librarytest.BookSpec{Title: "Roadone Secret", Series: "Roadone Saga", Sequence: "2", DurationMs: 1000, Files: 1})
	last = librarytest.AddBook(t, e.a.lib, folder, librarytest.BookSpec{Title: "Roadone Gamma", Series: "Roadone Saga", Sequence: "3", DurationMs: 1000, Files: 1})
	return first, hidden, last
}

func TestA1QueueNextSeries(t *testing.T) {
	e := syncSeed(t)
	first, hidden, last := e.a1Series(t)
	next := func(tok, body string) (int, map[string]any) {
		rec := e.send(tok, http.MethodPost, "/api/v1/queue/next", body)
		var out map[string]any
		if rec.Body.Len() > 0 {
			apiDecode(t, rec, &out)
		}
		return rec.Code, out
	}
	titleOf := func(out map[string]any) string {
		b, _ := out["book"].(map[string]any)
		s, _ := b["title"].(string)
		return s
	}
	ended := `{"ended":"` + first.ID + `","series":true}`

	// Opt-in: without it an empty queue is still a 204.
	if code, _ := next(e.owner, `{"ended":"`+first.ID+`"}`); code != http.StatusNoContent {
		t.Errorf("no opt-in = %d", code)
	}
	if code, _ := next(e.owner, `{"series":true}`); code != http.StatusNoContent {
		t.Errorf("series with no ended book = %d", code)
	}
	code, out := next(e.owner, ended)
	if code != http.StatusOK || out["source"] != "series" || titleOf(out) != "Roadone Secret" {
		t.Errorf("owner's next = %d %v", code, out)
	}
	// The member never learns of the closed book: the next one they may open is the third.
	code, out = next(e.member, ended)
	if code != http.StatusOK || titleOf(out) != "Roadone Gamma" {
		t.Errorf("member's next = %d %v", code, out)
	}
	// The last in the series, and an unknown ended book, are 204.
	if code, _ := next(e.owner, `{"ended":"`+last.ID+`","series":true}`); code != http.StatusNoContent {
		t.Errorf("last in series = %d", code)
	}
	if code, _ := next(e.owner, `{"ended":"bk_nothere","series":true}`); code != http.StatusNoContent {
		t.Errorf("unknown ended = %d", code)
	}
	// A hidden ended book gives the member nothing, not even a series.
	if rec := e.send(e.member, http.MethodPost, "/api/v1/queue/next", `{"ended":"`+hidden.ID+`","series":true}`); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("hidden ended = %d %s", rec.Code, rec.Body)
	}
	// A text next book is not played.
	if _, err := e.a.st.Writer().Exec(`UPDATE books SET kind = 'text' WHERE id = ?`, hidden.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := next(e.owner, ended); code != http.StatusNoContent {
		t.Errorf("text next = %d", code)
	}
	if _, err := e.a.st.Writer().Exec(`UPDATE books SET kind = 'audio' WHERE id = ?`, hidden.ID); err != nil {
		t.Fatal(err)
	}

	// The queue wins, and the series branch never touches it.
	if rec := e.send(e.owner, http.MethodPost, "/api/v1/queue", queueBody(e.extra[0], "")); rec.Code != http.StatusOK {
		t.Fatalf("queue add = %d", rec.Code)
	}
	if code, out := next(e.owner, ended); code != http.StatusOK || out["source"] != "queue" {
		t.Errorf("queue first = %d %v", code, out)
	}
	if code, out := next(e.owner, ended); code != http.StatusOK || out["source"] != "series" {
		t.Errorf("then the series = %d %v", code, out)
	}
	if e.queue(t, e.owner).Total != 0 {
		t.Error("the series branch changed the queue")
	}
	// The setting off means a 204.
	if rec := e.send(e.owner, http.MethodPatch, "/api/v1/settings", `{"nextInSeries":false}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if code, _ := next(e.owner, ended); code != http.StatusNoContent {
		t.Errorf("setting off = %d", code)
	}
	// The older answer keeps its field for apps that read a queue pop.
	e.send(e.owner, http.MethodPost, "/api/v1/queue", queueBody(e.extra[1], ""))
	if code, out := next(e.owner, ``); code != http.StatusOK || out["source"] != "queue" || out["book"] == nil {
		t.Errorf("plain pop = %d %v", code, out)
	}
}
