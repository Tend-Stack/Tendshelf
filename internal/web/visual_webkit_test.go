//go:build dev

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/testutil/webdriver"
)

// defaultMiniBrowser is the WebKitGTK 4.1 MiniBrowser that WebKitWebDriver (4.1) launches.
const defaultMiniBrowser = "/usr/lib/x86_64-linux-gnu/webkit2gtk-4.1/MiniBrowser"

// TestVisualWebKitKitchenSink runs the kitchen sink in WebKitGTK. Opt-in: set
// TENDSHELF_TEST_WEBKIT_DRIVER=/usr/bin/WebKitWebDriver (and optionally TENDSHELF_TEST_WEBKIT_BROWSER).
// MiniBrowser has no headless mode, so run it as `xvfb-run -a make visual-webkit` to keep the window
// off the desktop. The window cannot be narrower than 447 CSS px (a GTK minimum), so the phone
// layout is checked at 447, not 390. Reduced motion and layout shift cannot be read here; see the
// manual checks in docs/roadmap.md.
func TestVisualWebKitKitchenSink(t *testing.T) {
	driver := os.Getenv("TENDSHELF_TEST_WEBKIT_DRIVER")
	if driver == "" {
		t.Skip("TENDSHELF_TEST_WEBKIT_DRIVER not set")
	}
	browser := os.Getenv("TENDSHELF_TEST_WEBKIT_BROWSER")
	if browser == "" {
		browser = defaultMiniBrowser
	}
	base := appServer(t)
	out := visualDir(t, "webkit")
	s := webdriver.Start(t, driver, browser)

	var support struct {
		ViewTransitionName bool   `json:"viewTransitionName"`
		StartingStyle      string `json:"startingStyle"`
		UserAgent          string `json:"userAgent"`
	}
	s.Navigate(base + "/dev/kitchen-sink")
	s.Eval(`({viewTransitionName: CSS.supports("view-transition-name: none"),
		startingStyle: typeof CSSStartingStyleRule, userAgent: navigator.userAgent})`, &support)
	b, _ := json.MarshalIndent(support, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "support.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("WebKit support: %s", b)

	for _, w := range []int{447, 1440} {
		s.SetViewport(w, 900)
		for _, theme := range themes {
			t.Run(fmt.Sprintf("%s-%d", theme, w), func(t *testing.T) {
				s.Navigate(base + "/dev/kitchen-sink?theme=" + theme)
				var m struct {
					Over   bool    `json:"over"`
					Height float64 `json:"height"`
					Inner  float64 `json:"inner"`
				}
				s.Eval(`({over: document.documentElement.scrollWidth > window.innerWidth,
					height: document.documentElement.scrollHeight, inner: window.innerHeight})`, &m)
				if m.Over {
					t.Errorf("horizontal overflow at %d px in theme %s", w, theme)
				}
				// WebDriver screenshots the viewport: scroll through the page, at most eight shots.
				for i := 0; i < 8 && float64(i)*m.Inner < m.Height; i++ {
					s.Exec(fmt.Sprintf("window.scrollTo(0, %d);", i*int(m.Inner)), nil)
					s.Screenshot(filepath.Join(out, fmt.Sprintf("kitchen-%s-%d-%d.png", theme, w, i+1)))
				}
				s.Exec("window.scrollTo(0, 0);", nil)
			})
		}
		t.Run(fmt.Sprintf("swap-%d", w), func(t *testing.T) {
			s.Navigate(base + "/dev/kitchen-sink")
			var res struct {
				Path   string               `json:"path"`
				Before map[string][]float64 `json:"before"`
				After  map[string][]float64 `json:"after"`
			}
			s.ExecAsync(`const done = arguments[arguments.length - 1];
				const rects = () => { const out = {};
					for (const id of ["nav-tabbar", "nav-rail", "nav-side", "player", "audio"]) {
						const el = document.getElementById(id);
						const r = el ? el.getBoundingClientRect() : null;
						out[id] = r ? [r.x, r.y, r.width, r.height] : null;
					}
					return out; };
				const before = rects();
				document.addEventListener("htmx:after:settle", () => setTimeout(() => done({path: location.pathname, before: before, after: rects()}), 500), {once: true});
				setTimeout(() => done({path: "timeout", before: before, after: rects()}), 8000);
				document.querySelector("[data-probe-nav]").click();`, &res)
			if res.Path != "/dev/kitchen-sink/b" {
				t.Fatalf("path after the swap = %q", res.Path)
			}
			if fmt.Sprint(res.Before) != fmt.Sprint(res.After) {
				t.Errorf("shell rects moved on swap:\nbefore %v\nafter  %v", res.Before, res.After)
			}
		})
	}
	t.Logf("screenshots in %s", out)
}

// TestVisualWebKitPages opens eight real pages in WebKitGTK in dark and light at 447 and 1440 px and
// checks that each has no horizontal overflow, exactly one h1 and no touch target under 44 px
// (43.5 allowed), then takes a screenshot. Same opt-in as the kitchen sink; `make visual-webkit`
// runs both and should finish within two minutes.
func TestVisualWebKitPages(t *testing.T) {
	driver := os.Getenv("TENDSHELF_TEST_WEBKIT_DRIVER")
	if driver == "" {
		t.Skip("TENDSHELF_TEST_WEBKIT_DRIVER not set")
	}
	browser := os.Getenv("TENDSHELF_TEST_WEBKIT_BROWSER")
	if browser == "" {
		browser = defaultMiniBrowser
	}
	app := newTestApp(t)
	app.owner(t)
	browseSeed(t, app, 60)
	all := share.Filter{SQL: "1=1"}
	books, err := app.lib.ListBooks(context.Background(), library.Scope{Books: all, Libraries: all}, library.ListQuery{Limit: 1})
	if err != nil || len(books.Books) == 0 {
		t.Fatalf("seed: %v", err)
	}
	signed := themedServer(t, app.signedIn(t))
	anon := themedServer(t, app.h)
	out := visualDir(t, "webkit")
	s := webdriver.Start(t, driver, browser)

	pgs := []struct {
		name, base, path, probe string
	}{
		{"home", signed, "/", browseProbeJS},
		{"library", signed, "/libraries", browseProbeJS},
		{"library-list", signed, "/libraries?view=list", browseProbeJS},
		{"library-shelf", signed, "/libraries?view=shelf", browseProbeJS},
		{"book", signed, "/books/" + books.Books[0].ID, browseProbeJS},
		{"search", signed, "/search?q=lantern", browseProbeJS},
		{"settings", signed, "/me/settings", browseProbeJS},
		{"admin-libraries", signed, "/admin/libraries", browseProbeJS},
		{"login", anon, "/login", authProbeJS},
	}
	for _, w := range []int{447, 1440} {
		s.SetViewport(w, 900)
		for _, theme := range []string{"dark", "light"} {
			for _, pg := range pgs {
				t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
					sep := "?"
					if strings.Contains(pg.path, "?") {
						sep = "&"
					}
					s.Navigate(pg.base + pg.path + sep + "theme=" + theme)
					var m browseProbe
					s.Eval(pg.probe, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if m.H1 != 1 {
						t.Errorf("%d h1 elements", m.H1)
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					if len(m.Unlabelled) > 0 {
						t.Errorf("inputs without a label: %v", m.Unlabelled)
					}
					s.Screenshot(filepath.Join(out, fmt.Sprintf("pages-%s-%s-%d.png", pg.name, theme, w)))
				})
			}
		}
	}
	t.Logf("screenshots in %s", out)
}

// bookFirstProbeJS measures every book tile on a page: the cover box and, for the Profile's
// "Also in progress" tiles, where the round resume button sits. WebKit once collapsed those covers
// (container-type: inline-size on the tile), which Chromium never did.
const bookFirstProbeJS = `(() => {
	const box = (el) => { const r = el.getBoundingClientRect(); return {x: r.x, y: r.y, w: r.width, h: r.height}; };
	const tiles = [];
	for (const sel of [".ts-pbook", ".ts-profile__done > *", ".ts-grid--covers > *", ".ts-hero", ".ts-book-hero"]) {
		for (const el of document.querySelectorAll(sel)) {
			if (el.getBoundingClientRect().width === 0) { continue; }
			const cover = el.querySelector(".ts-cover");
			const btn = el.querySelector(":scope > .ts-iconbtn");
			const img = el.querySelector(".ts-cover img");
			tiles.push({sel: sel, loaded: img ? img.complete && img.naturalWidth > 0 : true, cover: cover ? box(cover) : null, btn: btn ? box(btn) : null, tile: box(el)});
		}
	}
	return {tiles: tiles, over: document.documentElement.scrollWidth > window.innerWidth};
})()`

type bookFirstBox struct{ X, Y, W, H float64 }

// TestBookFirstWebKitProfile opens /me with several books in progress and some finished in WebKitGTK
// and checks every tile: the cover has height, and the round control sits inside the cover box.
// Same opt-in as the other WebKit tests.
func TestBookFirstWebKitProfile(t *testing.T) {
	driver := os.Getenv("TENDSHELF_TEST_WEBKIT_DRIVER")
	if driver == "" {
		t.Skip("TENDSHELF_TEST_WEBKIT_DRIVER not set")
	}
	browser := os.Getenv("TENDSHELF_TEST_WEBKIT_BROWSER")
	if browser == "" {
		browser = defaultMiniBrowser
	}
	app := newTestApp(t)
	app.owner(t)
	browseSeed(t, app, 20)
	all := share.Filter{SQL: "1=1"}
	list, err := app.lib.ListBooks(context.Background(), library.Scope{Books: all, Libraries: all}, library.ListQuery{Limit: 12})
	if err != nil || len(list.Books) < 12 {
		t.Fatalf("seed: %v", err)
	}
	// The seeded audio files carry no duration, so no book would get a play control; give each its book's.
	streamExec(t, app, `UPDATE audio_files SET duration_ms = (SELECT duration_ms FROM books WHERE books.id = audio_files.book_id), container = 'mp3', codec = 'mp3'`)
	owner := ""
	if err := app.st.Writer().QueryRowContext(context.Background(), `SELECT id FROM users WHERE role = 'owner'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	now := int64(1_800_000_000_000)
	for i, b := range list.Books {
		var finished any
		pos := int64(600_000 + i*1000)
		if i >= 7 {
			finished, pos = now-int64(i)*1000, 3_000_000
		}
		streamExec(t, app, `INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id, device_label)
VALUES (?,?,?,?,?,?,?,?,?)`, owner, b.ID, pos, 3_000_000, finished, now-int64(i)*1000, now-int64(i)*1000, "devicealpha1", "x")
	}
	signed := themedServer(t, app.signedIn(t))
	out := visualDir(t, "webkit")
	s := webdriver.Start(t, driver, browser)

	pages := []struct{ name, path string }{
		{"profile", "/me"},
		{"library", "/libraries"},
		{"home", "/"},
		{"book", "/books/" + list.Books[0].ID},
	}
	for _, w := range []int{447, 1440} {
		s.SetViewport(w, 900)
		for _, pg := range pages {
			t.Run(fmt.Sprintf("%s-%d", pg.name, w), func(t *testing.T) {
				s.Navigate(signed + pg.path + "?theme=dark")
				var m struct {
					Over  bool `json:"over"`
					Tiles []struct {
						Sel    string        `json:"sel"`
						Loaded bool          `json:"loaded"`
						Cover  *bookFirstBox `json:"cover"`
						Btn    *bookFirstBox `json:"btn"`
						Tile   bookFirstBox  `json:"tile"`
					} `json:"tiles"`
				}
				s.Eval(bookFirstProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if pg.name == "profile" && len(m.Tiles) == 0 {
					t.Fatalf("no tiles on the Profile")
				}
				buttons := 0
				for i, tl := range m.Tiles {
					if tl.Btn != nil {
						buttons++
					}
					if tl.Cover == nil {
						continue
					}
					if tl.Cover.H < 40 || tl.Cover.W < 40 {
						t.Errorf("tile %d (%s): cover collapsed to %.0fx%.0f", i, tl.Sel, tl.Cover.W, tl.Cover.H)
						continue
					}
					if !tl.Loaded {
						t.Errorf("tile %d (%s): the cover image did not load or decode", i, tl.Sel)
					}
					// On a phone the control sits at the row's end, beside the cover; tiles put it on the cover.
					if tl.Btn != nil && w >= 768 {
						c, b := tl.Cover, tl.Btn
						if b.X < c.X-1 || b.Y < c.Y-1 || b.X+b.W > c.X+c.W+1 || b.Y+b.H > c.Y+c.H+1 {
							t.Errorf("tile %d (%s): button %+v is outside the cover %+v", i, tl.Sel, *b, *c)
						}
					}
				}
				if pg.name == "profile" && buttons == 0 {
					t.Errorf("no tile has its round resume control, so its place was never checked")
				}
				// WebKit paints a decoded image a moment after it reports complete; a shot taken at once shows flat boxes.
				s.ExecAsync(`const done = arguments[arguments.length - 1]; setTimeout(() => done(1), 1000);`, nil)
				s.Screenshot(filepath.Join(out, fmt.Sprintf("bookfirst-%s-%d.png", pg.name, w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
