package web

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// CH1: GET /api/v1/home, the car's start page (docs/plans/car-home.md).

type homeBook struct {
	ID string `json:"id"`
}

type homeBody struct {
	Continue []struct {
		Book homeBook `json:"book"`
	} `json:"continue"`
	Series []struct {
		Book homeBook `json:"book"`
	} `json:"series"`
	New     []homeBook `json:"new"`
	Because *struct {
		Seed  string     `json:"seed"`
		Books []homeBook `json:"books"`
	} `json:"because"`
	Episodes []map[string]any `json:"episodes"`
}

// ids lists every book id in every section, in order.
func (h homeBody) ids() []string {
	var out []string
	for _, c := range h.Continue {
		out = append(out, c.Book.ID)
	}
	for _, c := range h.Series {
		out = append(out, c.Book.ID)
	}
	for _, b := range h.New {
		out = append(out, b.ID)
	}
	if h.Because != nil {
		for _, b := range h.Because.Books {
			out = append(out, b.ID)
		}
	}
	return out
}

func (h homeBody) has(id string) bool {
	for _, x := range h.ids() {
		if x == id {
			return true
		}
	}
	return false
}

func (e *syncEnv) home(t *testing.T, tok string) (homeBody, string) {
	t.Helper()
	rec := e.send(tok, http.MethodGet, "/api/v1/home", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("home: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	var h homeBody
	apiDecode(t, rec, &h)
	return h, rec.Body.String()
}

func TestAPIHomeSections(t *testing.T) {
	e := syncSeed(t)
	now := time.Now().UnixMilli()
	if rec := e.send(e.owner, http.MethodPut, "/api/v1/progress/"+e.extra[0], putBody(60000, now, "")); rec.Code != 200 {
		t.Fatalf("progress: %d %s", rec.Code, rec.Body)
	}
	h, _ := e.home(t, e.owner)
	// The seed already holds a started book; the one just saved leads.
	if len(h.Continue) == 0 || h.Continue[0].Book.ID != e.extra[0] {
		t.Errorf("continue = %+v", h.Continue)
	}
	inNew := false
	for _, b := range h.New {
		if b.ID == e.extra[0] {
			t.Errorf("a started book is in New")
		}
		if b.ID == e.extra[1] {
			inNew = true
		}
	}
	if !inNew {
		t.Errorf("new lacks %s: %+v", e.extra[1], h.New)
	}
	seen := map[string]bool{}
	for _, id := range h.ids() {
		if seen[id] {
			t.Errorf("book %s twice", id)
		}
		seen[id] = true
	}

	// A person with little history still gets every key.
	_, raw := e.home(t, e.member)
	for _, want := range []string{`"series":[]`, `"new":[`, `"because":null`, `"episodes":[]`, `"continue":[`} {
		if !strings.Contains(raw, want) {
			t.Errorf("member body lacks %s: %s", want, raw)
		}
	}
}

func TestAPIHomeFinishedAndText(t *testing.T) {
	e := syncSeed(t)
	now := time.Now().UnixMilli()
	if rec := e.send(e.owner, http.MethodPut, "/api/v1/progress/"+e.extra[0], putBody(60000, now, "")); rec.Code != 200 {
		t.Fatalf("progress: %d %s", rec.Code, rec.Body)
	}
	if _, err := e.a.st.Writer().Exec(`UPDATE progress SET finished_at = ? WHERE book_id = ?`, now, e.extra[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.st.Writer().Exec(`UPDATE books SET kind = 'text' WHERE id = ?`, e.extra[1]); err != nil {
		t.Fatal(err)
	}
	h, _ := e.home(t, e.owner)
	for _, id := range []string{e.extra[0], e.extra[1]} {
		if h.has(id) {
			t.Errorf("book %s is on Home", id)
		}
	}
}

func TestAPIHomeForbidden(t *testing.T) {
	e := syncSeed(t)
	now := time.Now().UnixMilli()
	if _, err := e.a.st.Writer().Exec(`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, client_updated_at, server_updated_at, device_id, device_label)
VALUES (?,?,?,?,?,?,?,?)`, e.uid[sweepMember], e.hidden.ID, 1000, 3*sweepHourMs, now, now, "devicealpha1", "x"); err != nil {
		t.Fatal(err)
	}
	for _, role := range []int{sweepMember, sweepGuest} {
		tok := apiToken(t, e.sweepEnv, role)
		h, raw := e.home(t, tok)
		if h.has(e.hidden.ID) {
			t.Errorf("%s: hidden book on Home", sweepRoleNames[role])
		}
		for _, w := range e.hiddenWords() {
			if strings.Contains(raw, w) {
				t.Errorf("%s: body leaks %q", sweepRoleNames[role], w)
			}
		}
		if role == sweepGuest && !strings.Contains(raw, `"episodes":[]`) {
			t.Errorf("guest episodes: %s", raw)
		}
	}
	rec := jsonReq(e.a, http.MethodGet, "/api/v1/home", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no credentials: %d", rec.Code)
	}
}

func TestAPIHomeEpisodes(t *testing.T) {
	off := syncSeed(t)
	if _, raw := off.home(t, off.owner); !strings.Contains(raw, `"episodes":[]`) {
		t.Errorf("podcasts off: %s", raw)
	}

	w := newPodWorld(t)
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_subscriptions SET seen_at = 1 WHERE show_id = ?`, w.show); err != nil {
		t.Fatal(err)
	}
	tok := w.bearer(t, w.benID)
	rec := jsonReq(w.a, http.MethodGet, "/api/v1/home", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) })
	var h homeBody
	apiDecode(t, rec, &h)
	if rec.Code != 200 || len(h.Episodes) == 0 || len(h.Episodes) > carHomeEpisodes {
		t.Errorf("episodes on: %d %d %s", rec.Code, len(h.Episodes), rec.Body)
	}
	// Someone who follows nothing has none.
	tok = w.bearer(t, w.cyID)
	rec = jsonReq(w.a, http.MethodGet, "/api/v1/home", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) })
	if !strings.Contains(rec.Body.String(), `"episodes":[]`) {
		t.Errorf("no follows: %s", rec.Body)
	}
}
