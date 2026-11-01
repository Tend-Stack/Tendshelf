//go:build dev

package web

import (
	"net/http"
	"strings"
	"testing"
)

// utilityClasses is the class list of utilities.css (P0c) that the kitchen
// sink must show, so a visual review covers every one.
var utilityClasses = []string{
	"ts-stack", "ts-cluster", "ts-gap-4", "ts-gap-8", "ts-gap-12", "ts-gap-16", "ts-gap-24", "ts-gap-32",
	"ts-grid", "ts-grid--covers", "ts-center", "ts-measure", "ts-switcher", "ts-with-aside", "ts-sr-focusable",
	"ts-safe-top", "ts-safe-bottom", "ts-safe-inline", "ts-truncate", "ts-clamp-2", "ts-clamp-3", "ts-scroll-x",
	"ts-full-height", "ts-tap", "ts-value",
	"ts-cover--24", "ts-cover--36", "ts-cover--48", "ts-cover--52", "ts-cover--56", "ts-cover--64", "ts-cover--78",
	"ts-cover--112", "ts-cover--136", "ts-cover--152", "ts-cover--200", "ts-cover--fill",
}

func TestKitchenSinkRenders(t *testing.T) {
	rec, body := getPage(t, "/dev/kitchen-sink", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	for _, c := range utilityClasses {
		if !strings.Contains(body, c) {
			t.Errorf("kitchen sink lacks %s", c)
		}
	}
	if n := strings.Count(body, "ts-vt-cover"); n != 1 {
		t.Errorf("ts-vt-cover appears %d times on page A, want 1", n)
	}
	if strings.Contains(body, "style=") {
		t.Error("inline style attribute (the CSP forbids it)")
	}
	for _, id := range []string{"ks-utilities", "ks-press", "ks-progress", "ks-skeleton", "ks-toasts", "ks-sheet", "ks-cover", "ks-themes", "ks-bar"} {
		if !strings.Contains(body, `id="`+id+`"`) {
			t.Errorf("missing section %s", id)
		}
	}
	_, b := getPage(t, "/dev/kitchen-sink/b", nil)
	if strings.Count(b, "ts-vt-cover") != 1 || !strings.Contains(b, "ts-cover--200") {
		t.Error("page B needs exactly one ts-vt-cover on a ts-cover--200")
	}
	_, sheet := getPage(t, "/dev/kitchen-sink?sheet=1", nil)
	for _, w := range []string{"ts-sheet-layer", `class="ts-scrim"`, `class="ts-sheet"`} {
		if !strings.Contains(sheet, w) {
			t.Errorf("sheet page lacks %s", w)
		}
	}
	if strings.Contains(body, "ts-sheet-layer") {
		t.Error("sheet layer without ?sheet=1")
	}
}

func TestKitchenSinkThemeCookie(t *testing.T) {
	rec, _ := getPage(t, "/dev/kitchen-sink?theme=sepia", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dev/kitchen-sink" {
		t.Fatalf("status %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == themeCookie && c.Value == "sepia" {
			found = true
		}
	}
	if !found {
		t.Error("no ts_theme cookie")
	}
}

func TestDevToastNeedsCSRF(t *testing.T) {
	h := NewHandler(Options{})
	c, tok := session(t, h)
	hx := func(r *http.Request) { r.Header.Set("HX-Request", "true") }
	if rec := send(h, http.MethodPost, "/hx/dev/toast?kind=ok", c, hx); rec.Code != http.StatusForbidden {
		t.Fatalf("without the token: status %d, want 403", rec.Code)
	}
	rec := send(h, http.MethodPost, "/hx/dev/toast?kind=ok", c, func(r *http.Request) { hx(r); r.Header.Set("X-CSRF-Token", tok) })
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hx-swap-oob") {
		t.Fatalf("with the token: status %d, body %q", rec.Code, rec.Body.String())
	}
}

func TestDevToastKinds(t *testing.T) {
	h := NewHandler(Options{})
	c, tok := session(t, h)
	post := func(kind string) *httpResult {
		rec := send(h, http.MethodPost, "/hx/dev/toast?kind="+kind, c, func(r *http.Request) {
			r.Header.Set("HX-Request", "true")
			r.Header.Set("X-CSRF-Token", tok)
		})
		return &httpResult{rec.Code, rec.Body.String()}
	}
	cases := []struct {
		kind, role, text string
	}{
		{"ok", `role="status"`, "Bookmarked at 14:06"},
		{"hint", `role="status"`, "Rewound 5 s after your break"},
		{"err", `role="alert"`, "Couldn&#39;t reach your server."},
	}
	for _, tc := range cases {
		r := post(tc.kind)
		if r.code != http.StatusOK || !strings.Contains(r.body, tc.role) || !strings.Contains(r.body, tc.text) {
			t.Errorf("%s: status %d, body %q", tc.kind, r.code, r.body)
		}
	}
	r := post("nope")
	if r.code != http.StatusBadRequest || !strings.Contains(r.body, `role="alert"`) {
		t.Errorf("unknown kind: status %d, body %q", r.code, r.body)
	}
}

type httpResult struct {
	code int
	body string
}

func TestDevProgressBounds(t *testing.T) {
	h := NewHandler(Options{})
	for _, v := range []string{"-1", "101", "x", ""} {
		if rec := send(h, http.MethodGet, "/hx/dev/progress?v="+v, nil, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("v=%q: status %d, want 400", v, rec.Code)
		}
	}
	rec := send(h, http.MethodGet, "/hx/dev/progress?v=30", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-ts-value="30"`) || !strings.Contains(rec.Body.String(), `aria-valuenow="30"`) {
		t.Errorf("v=30: status %d, body %q", rec.Code, rec.Body.String())
	}
}
