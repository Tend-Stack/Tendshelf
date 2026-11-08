package web

import (
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

func TestSpineTitle(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"no brackets", "Small Winters", "Small Winters"},
		{"two groups", "Kiss and Don't Tell (The Vancouver Agitators #1) (Unabridged)", "Kiss and Don't Tell"},
		{"square group", "James Ellroy's American Tabloid꞉ A Full Cast Audio Drama [128k]", "James Ellroy's American Tabloid꞉ A Full Cast Audio Drama"},
		{"mixed groups", "Vacation Wars (Unabridged) [128k]", "Vacation Wars"},
		{"only a group stays whole", "(Unabridged)", "(Unabridged)"},
		{"only groups keep the first", "[128k] (Unabridged)", "[128k]"},
		{"nested", "Title (Series (Book 1)) (Unabridged)", "Title"},
		{"nested alone", "Title [a [b]]", "Title"},
		{"middle bracket stays", "The (Other) Side", "The (Other) Side"},
		{"middle stays, end goes", "The (Other) Side (Unabridged)", "The (Other) Side"},
		{"leading group stays", "(Part 1) The Start", "(Part 1) The Start"},
		{"no space before", "Title(Unabridged)", "Title"},
		{"unbalanced", "Title (Unabridged", "Title (Unabridged"},
		{"stray closer", "Title Unabridged)", "Title Unabridged)"},
		{"trailing spaces", "Title (Unabridged)  ", "Title"},
		{"empty", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := spineTitle(c.in); got != c.want {
				t.Errorf("spineTitle(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestHomeSpineShortTitle: the spine's text is the short title; its label keeps the whole one.
func TestHomeSpineShortTitle(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
		Title: "Kiss and Don't Tell (The Vancouver Agitators #1) (Unabridged)", Authors: []string{"Imogen Hale"}, DurationMs: 600_000, AddedAt: 1000,
	})
	body := browseGet(a, "/", false, nil).Body.String()
	for _, want := range []string{
		`<span class="ts-spine__title">Kiss and Don&#39;t Tell</span>`,
		`aria-label="Kiss and Don&#39;t Tell (The Vancouver Agitators #1) (Unabridged) by Imogen Hale, 10 m"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
}
