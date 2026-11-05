package web

import (
	"strings"
	"testing"
)

// Home's row of three covers under the hero (decision 0021, PR3). The lab and helpers are those of
// me_bookfirst_test.go.

// bookfirstHomeRow cuts the "Also in progress" row out of a page ("" when there is none).
func bookfirstHomeRow(body string) string {
	i := strings.Index(body, `aria-labelledby="home-also-title"`)
	if i < 0 {
		return ""
	}
	body = body[i:]
	return body[:strings.Index(body, "</section>")]
}

func TestBookFirstHomeRow(t *testing.T) {
	l := fmt2Seed(t)
	hero := bookfirstHeroBook(t, l, l.owner)
	var titles []string
	for i, name := range []string{"Row One", "Row Two", "Row Three", "Row Four"} {
		b := bookfirstAudio(t, l, name)
		bookfirstSave(t, l, l.owner, b.ID, 600_000, int64(800_000-i*10_000), false)
		titles = append(titles, name)
	}
	body := l.get(l.owner, "/", false).Body.String()
	row := bookfirstHomeRow(body)
	if row == "" {
		t.Fatal("no Also in progress row on Home")
	}
	if n := strings.Count(row, `<a class="ts-card"`); n != 3 {
		t.Errorf("%d cards in the row, want 3", n)
	}
	for _, name := range titles[:3] {
		if !strings.Contains(row, name) {
			t.Errorf("%q missing from the row", name)
		}
	}
	if strings.Contains(row, titles[3]) {
		t.Error("a fourth book is in the row")
	}
	if strings.Contains(row, hero.Title) {
		t.Error("the hero's book is in the row")
	}
	if !strings.Contains(row, `href="/me"`) || !strings.Contains(row, "See all") {
		t.Error("the row lacks See all to /me")
	}
	if strings.Contains(row, "data-ts-play") || strings.Contains(row, "data-ts-read") || strings.Contains(row, "/read") {
		t.Error("a row card carries a resume control of its own")
	}
	if n := bookfirstControls(bookMain(body), hero.ID); n != 1 {
		t.Errorf("hero has %d resume controls, want 1", n)
	}

	// A book out of scope never shows.
	if _, err := l.a.st.Writer().Exec(`UPDATE books SET missing_since = 1 WHERE title = 'Row One'`); err != nil {
		t.Fatal(err)
	}
	if row := bookfirstHomeRow(l.get(l.owner, "/", false).Body.String()); strings.Contains(row, "Row One") || !strings.Contains(row, "Row Four") {
		t.Error("a book out of reach shows, or the row did not refill")
	}

	// With the hero alone there is no row.
	l2 := fmt2Seed(t)
	bookfirstHeroBook(t, l2, l2.owner)
	if bookfirstHomeRow(l2.get(l2.owner, "/", false).Body.String()) != "" {
		t.Error("a row shows with one book under way")
	}
}

func TestBookFirstHomeFragment(t *testing.T) {
	l := fmt2Seed(t)
	hero := bookfirstHeroBook(t, l, l.owner)
	next := bookfirstAudio(t, l, "Next Up")
	bookfirstSave(t, l, l.owner, next.ID, 600_000, 800_000, false)
	frag := func() string { return l.get(l.owner, "/hx/home/hero", true).Body.String() }
	if row := bookfirstHomeRow(frag()); !strings.Contains(row, "Next Up") || strings.Contains(frag(), "<html") {
		t.Fatal("the fragment lacks the row, or is a full page")
	}
	// A save of the row's book makes it the hero and takes it out of the row.
	bookfirstSave(t, l, l.owner, next.ID, 900_000, 1_000_000, false)
	f := frag()
	if !strings.Contains(bookfirstHero(f), "Next Up") {
		t.Error("the saved book is not the hero")
	}
	row := bookfirstHomeRow(f)
	if strings.Contains(row, "Next Up") || !strings.Contains(row, hero.Title) {
		t.Error("the row did not swap the old hero in and the new one out")
	}
}

// TestBookFirstHomeRequests: the row's three covers cost three requests, paid for by three stylesheets that
// Home no longer prefetches. The measured number (at or under 61) is `TENDSHELF_TEST_PERF=1 TestPageWeight`.
func TestBookFirstHomeRequests(t *testing.T) {
	l := fmt2Seed(t)
	body := l.get(l.owner, "/", false).Body.String()
	for _, tag := range strings.SplitAfter(body, ">") {
		if !strings.Contains(tag, `rel="prefetch"`) {
			continue
		}
		for _, name := range []string{"pages-invite", "pages-folders", "pages-upload", "pages-email"} {
			if strings.Contains(tag, name) {
				t.Errorf("%s.css is still prefetched", name)
			}
		}
	}
}
