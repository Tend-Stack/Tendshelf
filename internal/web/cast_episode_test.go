package web

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// castPodMint mints a cast token for a podcast-world person and returns it. Casting is switched on first.
func castPodMint(t *testing.T, w *podWorld, who *http.Cookie, uid, id string) (string, int) {
	t.Helper()
	ctx := context.Background()
	set, err := w.a.srv.users.LoadSettings(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	set.Cast = true
	if err := w.a.srv.users.SaveSettings(ctx, uid, set); err != nil {
		t.Fatal(err)
	}
	csrf := w.a.tokenFor(t, who)
	rec := w.a.do(http.MethodPost, "/casting/address", url.Values{"kind": {"episode"}, "id": {id}}, func(r *http.Request) {
		r.TLS = &tls.ConnectionState{} // this world has no trusted proxy: a direct https request
		r.Host = "podcasts.example"
		r.Header.Set("X-CSRF-Token", csrf)
	}, who)
	if rec.Code != http.StatusOK {
		return "", rec.Code
	}
	var out struct{ Base string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`/cast/([A-Za-z0-9_-]{43})$`).FindStringSubmatch(out.Base)
	if m == nil {
		t.Fatalf("base %q", out.Base)
	}
	return m[1], rec.Code
}

func TestCastEpisode(t *testing.T) {
	w := newPodWorld(t)
	ep := w.eps[0]
	tok, code := castPodMint(t, w, w.ben, w.benID, ep)
	if code != http.StatusOK {
		t.Fatalf("mint = %d", code)
	}
	get := func(path string) int {
		return w.a.do(http.MethodGet, path, nil, nil).Code
	}
	rec := w.a.do(http.MethodGet, "/cast/"+tok+"/episode", nil, nil)
	if rec.Code != http.StatusOK || !equalBytes(rec.Body.Bytes(), w.fx.audio) || rec.Header().Get("Cache-Control") != "private, no-store" ||
		rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("episode = %d, %d bytes, headers %v", rec.Code, rec.Body.Len(), rec.Header())
	}
	if c := get("/cast/" + tok + "/audio/" + ep); c != http.StatusNotFound {
		t.Errorf("book path with an episode token = %d", c)
	}
	// A person who does not follow the show gets no address.
	if _, code := castPodMint(t, w, w.cy, w.cyID, ep); code != http.StatusNotFound {
		t.Errorf("stranger mint = %d", code)
	}
	if _, code := castPodMint(t, w, w.ben, w.benID, "pe_nope"); code != http.StatusNotFound {
		t.Errorf("unknown episode mint = %d", code)
	}
	// Turning podcasts off ends the address, and no new one is minted.
	off := podcast.Defaults()
	if err := podcast.SaveSettings(context.Background(), w.a.st, "", off, time.Now()); err != nil {
		t.Fatal(err)
	}
	if c := get("/cast/" + tok + "/episode"); c != http.StatusNotFound {
		t.Errorf("podcasts off at fetch = %d", c)
	}
	if _, code := castPodMint(t, w, w.ben, w.benID, ep); code != http.StatusNotFound {
		t.Errorf("podcasts off at mint = %d", code)
	}
}
