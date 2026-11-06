package components

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/web/static"
)

func renderShell(t *testing.T, which string, p Page) string {
	t.Helper()
	assets, err := static.Load()
	if err != nil {
		t.Fatal(err)
	}
	p.Assets = assets
	body := templ.Raw("<p>body</p>")
	var buf bytes.Buffer
	switch which {
	case "layout":
		err = Layout(p, body).Render(context.Background(), &buf)
	case "frame":
		err = Frame(p, body).Render(context.Background(), &buf)
	default:
		err = LinkShell(p, "", nil, body).Render(context.Background(), &buf)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// barOf returns the markup from the bar to the start of #main.
func barOf(t *testing.T, out string) string {
	t.Helper()
	start, end := strings.Index(out, `id="desktop-bar"`), strings.Index(out, `id="main"`)
	if start < 0 || end < start {
		t.Fatalf("the bar must come before #main (%d, %d)", start, end)
	}
	// Layout puts the offline banner between the bar and #main.
	if offline := strings.Index(out[start:end], `class="ts-offline`); offline >= 0 {
		end = start + offline
	}
	return out[start:end]
}

// TestDeskBarOnlyInShell: the bar, its script and the crumb exist under the desktop shell and nowhere
// else, and it comes before #main.
func TestDeskBarOnlyInShell(t *testing.T) {
	for _, which := range []string{"layout", "frame", "linkshell"} {
		for _, shell := range []string{"", "ios", "android", "desktop"} {
			out := renderShell(t, which, Page{Title: "Home", Shell: shell, Crumb: Crumb{Label: "Library", Href: "/libraries"}})
			has := strings.Contains(out, `id="desktop-bar"`)
			if has != (shell == "desktop") {
				t.Errorf("%s as %q: bar present = %v", which, shell, has)
			}
			if shell != "desktop" {
				for _, not := range []string{"ts-wbar", "js/desktop-shell", "data-ts-crumb", "Quit Tendshelf"} {
					if strings.Contains(out, not) {
						t.Errorf("%s as %q contains %q", which, shell, not)
					}
				}
				continue
			}
			barOf(t, out)
			if strings.Count(out, `id="desktop-bar"`) != 1 || !strings.Contains(out, "hx-preserve") {
				t.Errorf("%s: one preserved bar expected", which)
			}
			if !strings.Contains(out, "js/desktop-shell") {
				t.Errorf("%s: desktop-shell.js missing", which)
			}
			if which == "layout" && !strings.Contains(out, `data-ts-crumb-href="/libraries"`) {
				t.Error("layout: crumb missing on #main")
			}
		}
	}
}

var (
	controlRe = regexp.MustCompile(`(?s)<(button|a)\b([^>]*)>(.*?)</(?:button|a)>`)
	tagRe     = regexp.MustCompile(`(?s)<[^>]*>`)
)

// TestDeskBarLabels: every button and link of the bar and its menu has a name a screen reader can say.
func TestDeskBarLabels(t *testing.T) {
	bar := barOf(t, renderShell(t, "layout", Page{Title: "Home", Shell: "desktop"}))
	controls := controlRe.FindAllStringSubmatch(bar, -1)
	if len(controls) < 8 {
		t.Fatalf("only %d controls found in the bar", len(controls))
	}
	for _, c := range controls {
		named := strings.Contains(c[2], "aria-label=") || strings.TrimSpace(tagRe.ReplaceAllString(c[3], "")) != ""
		// The crumb and the playing book get their words from the page when they show.
		filledLater := strings.Contains(c[2], "data-wbar-crumb") || strings.Contains(c[2], "data-wbar-now")
		if !named && !filledLater {
			t.Errorf("a %s in the bar has no name: %s", c[1], c[2])
		}
	}
	for _, label := range []string{"Back", "Forward", "More", "Minimise", "Maximise", "Restore", "Close", "Tendshelf, Home"} {
		if !strings.Contains(bar, `aria-label="`+label+`"`) {
			t.Errorf("no control named %q", label)
		}
	}
	if !strings.Contains(bar, `aria-haspopup="menu"`) || !strings.Contains(bar, `role="menuitem"`) {
		t.Error("More must open a menu with menu items")
	}
	if !strings.Contains(bar, `role="group" aria-label="Window"`) {
		t.Error("the window buttons are a labelled group")
	}
}

// TestDeskBarPicker: the start page's bar has the brand and the window buttons only.
func TestDeskBarPicker(t *testing.T) {
	out := renderShell(t, "frame", Page{Title: "Welcome", Shell: "desktop", Picker: true})
	bar := barOf(t, out)
	for _, not := range []string{`aria-label="Back"`, `aria-label="Forward"`, `aria-label="More"`, "desktop-menu", `href="/"`} {
		if strings.Contains(bar, not) {
			t.Errorf("picker bar contains %q", not)
		}
	}
	for _, label := range []string{"Minimise", "Maximise", "Close"} {
		if !strings.Contains(bar, `aria-label="`+label+`"`) {
			t.Errorf("picker bar lacks %q", label)
		}
	}
}

// TestDeskBarCSP: the bar uses no inline script, no style attribute and no event attribute.
func TestDeskBarCSP(t *testing.T) {
	bar := barOf(t, renderShell(t, "layout", Page{Title: "Home", Shell: "desktop", Crumb: Crumb{Label: "Library", Href: "/libraries"}}))
	for _, not := range []string{"<script", " style=", " onclick=", " hx-on", "javascript:"} {
		if strings.Contains(bar, not) {
			t.Errorf("the bar contains %q", not)
		}
	}
}
