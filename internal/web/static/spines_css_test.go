package static

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestShelfViewSpinesCSS: the shelf's stylesheet (decision 0034) uses tokens only, never !important,
// and styles every thickness step and every height step the server can name.
func TestShelfViewSpinesCSS(t *testing.T) {
	css := stripComments(readCSS(t, "pages-spines.css"))
	if m := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(`).FindString(css); m != "" {
		t.Errorf("pages-spines.css has a literal colour: %q", m)
	}
	if strings.Contains(css, "!important") {
		t.Error("pages-spines.css uses !important")
	}
	for i := 1; i <= 12; i++ {
		if want := ".ts-spine--w" + strconv.Itoa(i) + " "; !strings.Contains(css, want) {
			t.Errorf("no rule for %s", want)
		}
	}
	for i := 1; i <= 5; i++ {
		if want := ".ts-spine--h" + strconv.Itoa(i) + " "; !strings.Contains(css, want) {
			t.Errorf("no rule for %s", want)
		}
	}
}
