package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

func (e *progRouteEnv) speedPost(ck *http.Cookie, tok string, form url.Values, htmx bool) int {
	var cookies []*http.Cookie
	if ck != nil {
		cookies = append(cookies, ck)
	}
	return e.a.do(http.MethodPost, "/hx/books/"+e.book.ID+"/speed", form, func(r *http.Request) {
		if tok != "" {
			r.Header.Set("X-CSRF-Token", tok)
		}
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, cookies...).Code
}

// TestBookSpeedRoute: a speed saved for one book reaches that book's player only, per person, and
// "make it my usual speed" moves it to the usual speed and clears the book's own.
func TestBookSpeedRoute(t *testing.T) {
	e := progRouteEnvOf(t)
	ctx := context.Background()
	repo := e.a.srv.users
	hasSpeed := func(ck *http.Cookie, want string) {
		t.Helper()
		_, body := e.player("/hx/player/"+e.book.ID, ck)
		if !strings.Contains(body, `data-speed="`+want+`"`) {
			t.Errorf("player data lacks speed %s", want)
		}
	}
	hasSpeed(e.owner, "1.00")
	if code := e.speedPost(e.owner, e.ownerTok, url.Values{"speed": {"1.35"}}, true); code != 204 {
		t.Fatalf("save: %d", code)
	}
	if sp, err := repo.BookSpeed(ctx, e.ownerID, e.book.ID); err != nil || sp != 1.35 {
		t.Fatalf("stored = %v, %v", sp, err)
	}
	hasSpeed(e.owner, "1.35")
	if set, _ := repo.LoadSettings(ctx, e.ownerID); set.Speed != 1.0 {
		t.Errorf("the usual speed changed to %v", set.Speed)
	}
	if _, err := repo.BookSpeed(ctx, e.memberID, e.book.ID); err == nil {
		t.Error("another person got the speed")
	}
	// Make it the usual speed: the book's own row goes, the usual speed takes the value.
	if code := e.speedPost(e.owner, e.ownerTok, url.Values{"speed": {"1.35"}, "make_default": {"1"}}, true); code != 204 {
		t.Fatalf("make default: %d", code)
	}
	if _, err := repo.BookSpeed(ctx, e.ownerID, e.book.ID); err != users.ErrNoBookSpeed {
		t.Errorf("the book's own speed stayed: %v", err)
	}
	if set, _ := repo.LoadSettings(ctx, e.ownerID); set.Speed != 1.35 {
		t.Errorf("usual speed = %v", set.Speed)
	}
	hasSpeed(e.owner, "1.35")
}

func TestBookSpeedForbidden(t *testing.T) {
	e := progRouteEnvOf(t)
	ctx := context.Background()
	repo := e.a.srv.users
	good := url.Values{"speed": {"1.25"}}
	// No grant: the book answers like an unknown one and nothing is stored.
	if code := e.speedPost(e.member, e.mTok, good, true); code != 404 {
		t.Errorf("member without a grant: %d", code)
	}
	if code := e.a.do(http.MethodPost, "/hx/books/bk_unknown/speed", good, func(r *http.Request) { r.Header.Set("X-CSRF-Token", e.ownerTok) }, e.owner).Code; code != 404 {
		t.Errorf("unknown book: %d", code)
	}
	// No CSRF token, and no session.
	if code := e.speedPost(e.owner, "", good, true); code != 403 {
		t.Errorf("without the token: %d", code)
	}
	if code := e.speedPost(nil, e.ownerTok, good, true); code == 204 {
		t.Errorf("signed out: %d", code)
	}
	for name, speed := range map[string]string{"low": "0.4", "high": "3.5", "off step": "1.33", "NaN": "NaN", "text": "fast", "empty": ""} {
		if code := e.speedPost(e.owner, e.ownerTok, url.Values{"speed": {speed}}, true); code != 422 {
			t.Errorf("%s: %d", name, code)
		}
	}
	for _, id := range []string{e.ownerID, e.memberID} {
		if _, err := repo.BookSpeed(ctx, id, e.book.ID); err != users.ErrNoBookSpeed {
			t.Errorf("a refused request stored a speed for %s", id)
		}
	}
}

// TestBookSpeedStore: the per-book speed is validated and keeps one row per person and book.
func TestBookSpeedStore(t *testing.T) {
	e := progRouteEnvOf(t)
	ctx := context.Background()
	repo := e.a.srv.users
	if err := repo.SetBookSpeed(ctx, e.ownerID, e.book.ID, 2.05+0.01); err == nil {
		t.Error("an off-step speed was accepted")
	}
	if err := repo.SetBookSpeed(ctx, e.ownerID, e.book.ID, 1.5); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetBookSpeed(ctx, e.ownerID, e.book.ID, 1.75); err != nil {
		t.Fatal(err)
	}
	if sp, _ := repo.BookSpeed(ctx, e.ownerID, e.book.ID); sp != 1.75 {
		t.Errorf("second write = %v", sp)
	}
	if n := progRouteCount(t, e, "book_settings"); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

// TestSmartRewindCurve: the rewind grows with the pause; the table the player reads is the same one.
func TestSmartRewindCurve(t *testing.T) {
	for _, c := range []struct {
		pause time.Duration
		want  int
	}{
		{0, 0}, {9 * time.Second, 0}, {10 * time.Second, 2}, {59 * time.Second, 2},
		{time.Minute, 5}, {59 * time.Minute, 5}, {time.Hour, 10}, {23 * time.Hour, 10},
		{24 * time.Hour, 20}, {90 * 24 * time.Hour, 20},
	} {
		if got := rewindSeconds(c.pause); got != c.want {
			t.Errorf("pause %v: rewind %d s, want %d", c.pause, got, c.want)
		}
	}
	if got, want := rewindAttr(), "10000:2,60000:5,3600000:10,86400000:20"; got != want {
		t.Errorf("rewindAttr = %q, want %q", got, want)
	}
	e := progRouteEnvOf(t)
	if _, body := e.player("/hx/player/"+e.book.ID, e.owner); !strings.Contains(body, `data-rewind="`+rewindAttr()+`"`) {
		t.Error("the player data lacks data-rewind")
	}
	if !strings.Contains(msgRewound, "{s}") {
		t.Error("the rewound hint has no {s}")
	}
}
