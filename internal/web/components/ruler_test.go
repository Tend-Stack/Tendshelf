package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// A ruler of a book with two or more chapters is a chapter slider (chapter-bar.js); otherwise it is
// the plain progress bar it always was.
func TestRulerChapterBar(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    RulerView
		want []string
		not  []string
	}{
		{"chapters", RulerView{Label: "Book progress, chapter 2 of 3", Now: "50.0", Ticks: []string{"33.3", "66.7"}, Book: "b1", Chapters: 3, Current: 2},
			[]string{`role="slider"`, `tabindex="0"`, `aria-valuemin="1"`, `aria-valuemax="3"`, `aria-valuenow="2"`, `data-ts-chbar`, `data-ts-book="b1"`}, []string{"progressbar"}},
		{"one chapter", RulerView{Label: "Book progress", Now: "50.0", Book: "b1", Chapters: 1, Current: 1},
			[]string{`role="progressbar"`}, []string{"data-ts-chbar", "tabindex"}},
		{"no book", RulerView{Label: "Progress", Now: "10"},
			[]string{`role="progressbar"`}, []string{"data-ts-chbar", "tabindex"}},
	} {
		var buf bytes.Buffer
		if err := Ruler(tc.v).Render(context.Background(), &buf); err != nil {
			t.Fatal(err)
		}
		html := buf.String()
		for _, w := range tc.want {
			if !strings.Contains(html, w) {
				t.Errorf("%s: missing %s in %s", tc.name, w, html)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(html, n) {
				t.Errorf("%s: unexpected %s", tc.name, n)
			}
		}
	}
}
