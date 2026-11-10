package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/podcast/rss"
)

// The podcast pages (plan P3): what each page draws, who may open it, and that off is off.

func TestPodcastPagesContent(t *testing.T) {
	w := newPodWorld(t)
	body := func(path string, ck *http.Cookie) string {
		t.Helper()
		rec := w.get(path, ck)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		return rec.Body.String()
	}

	home := body("/podcasts", w.ben)
	for _, want := range []string{"<h1", "Podcasts", "Find a podcast", `href="/podcasts/shows/` + w.show + `"`, `aria-label="Podcasts sections"`, `aria-current="page"`} {
		if !strings.Contains(home, want) {
			t.Errorf("home lacks %q", want)
		}
	}
	// Three episodes came after the follow time? They were applied after Follow, dated 2024, so none is new.
	for _, tab := range []string{"new", "favourites", "history"} {
		if b := body("/podcasts?tab="+tab, w.ben); !strings.Contains(b, "ts-empty") {
			t.Errorf("tab %s: no empty state", tab)
		}
	}
	// A person who follows nothing sees the empty state with the plan's sentence and a link to search.
	empty := body("/podcasts", w.cy)
	if !strings.Contains(empty, "You&#39;re not following any podcasts yet. Search for one, or paste its RSS address.") ||
		!strings.Contains(empty, `href="/podcasts/search"`) {
		t.Errorf("empty home: %s", empty)
	}

	show := body("/podcasts/shows/"+w.show, w.ben)
	for _, want := range []string{"Main", "Ann Author", "Stop following", "Refresh", "Following", "Settings for this show", "Speed for this show",
		"Skip the first", "Skip the last", "Unplayed", "In progress", "Played", "Newest first", "Oldest first", "Search this show",
		"Main episode 1", "Main episode 3", "Notes about Main", "Mark as played", "Star"} {
		if !strings.Contains(show, want) {
			t.Errorf("show page lacks %q", want)
		}
	}
	// Newest first by default, oldest first on request.
	if strings.Index(show, "Main episode 3") > strings.Index(show, "Main episode 1") {
		t.Error("default order is not newest first")
	}
	if old := body("/podcasts/shows/"+w.show+"?sort=oldest", w.ben); strings.Index(old, "Main episode 1") > strings.Index(old, "Main episode 3") {
		t.Error("oldest first is not oldest first")
	}
	// The search box finds one episode.
	if q := body("/podcasts/shows/"+w.show+"?q=episode+2", w.ben); strings.Contains(q, "Main episode 1") || !strings.Contains(q, "Main episode 2") {
		t.Error("search within the show did not narrow the list")
	}
	if q := body("/podcasts/shows/"+w.show+"?q=nothing-like-it", w.ben); !strings.Contains(q, "No episodes match.") {
		t.Error("no match line missing")
	}
	// Filters: mark one played, then see it under Played and not under Unplayed.
	if rec := w.post(t, "/podcasts/episodes/"+w.eps[0]+"/played", w.ben, url.Values{"played": {"1"}, "back": {"/podcasts/shows/" + w.show}}); rec.Code != http.StatusSeeOther ||
		rec.Header().Get("Location") != "/podcasts/shows/"+w.show {
		t.Fatalf("mark played: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if b := body("/podcasts/shows/"+w.show+"?filter=played", w.ben); !strings.Contains(b, "Main episode 1") || strings.Contains(b, "Main episode 2") {
		t.Error("played filter wrong")
	}
	if b := body("/podcasts/shows/"+w.show+"?filter=unplayed", w.ben); strings.Contains(b, "Main episode 1") || !strings.Contains(b, "Main episode 2") {
		t.Error("unplayed filter wrong")
	}
	if b := body("/podcasts/shows/"+w.show+"?filter=in_progress", w.ben); !strings.Contains(b, "No episodes match.") {
		t.Error("in progress filter wrong")
	}
	if rec := w.get("/podcasts/shows/"+w.show+"?filter=bogus", w.ben); rec.Code != http.StatusBadRequest {
		t.Errorf("bad filter: %d", rec.Code)
	}
	// A star lands in Favourites; a position shows in In progress.
	w.post(t, "/podcasts/episodes/"+w.eps[1]+"/favourite", w.ben, url.Values{"on": {"1"}})
	if b := body("/podcasts?tab=favourites", w.ben); !strings.Contains(b, "Main episode 2") || !strings.Contains(b, `aria-pressed="true"`) {
		t.Error("favourites tab missing the starred episode")
	}

	ep := body("/podcasts/episodes/"+w.eps[2], w.ben)
	for _, want := range []string{"Main episode 3", "Play", "Mark as played", "Save the file", `/podcasts/audio/` + w.eps[2] + `?download=1`, `data-ts-play="` + w.eps[2] + `"`} {
		if !strings.Contains(ep, want) {
			t.Errorf("episode page lacks %q", want)
		}
	}
	// The play button of an episode carries the pause icon the player script swaps in while it plays.
	if !strings.Contains(ep, "data-ts-pause-icon") {
		t.Error("episode page play button has no pause icon")
	}
	if !strings.Contains(body("/podcasts/shows/"+w.show, w.ben), "data-ts-pause-icon") {
		t.Error("episode rows have no pause icon")
	}

	search := body("/podcasts/search", w.ben)
	for _, want := range []string{"Find a podcast", "Search podcasts", "Add by RSS address", "Paste the show&#39;s RSS address.", `name="feed_url"`} {
		if !strings.Contains(search, want) {
			t.Errorf("search page lacks %q", want)
		}
	}
	if hits := body("/podcasts/search?q=found", w.ben); !strings.Contains(hits, "Found Show") || !strings.Contains(hits, `name="slug" value="found-show"`) {
		t.Error("search results not drawn with a Follow form")
	}

	// Every image the pages draw comes from this server (the CSP allows only 'self').
	for _, p := range []string{"/podcasts", "/podcasts/search?q=found", "/podcasts/shows/" + w.show, "/podcasts/episodes/" + w.eps[0]} {
		for _, m := range regexp.MustCompile(`(?:src|srcset)="([^"]+)"`).FindAllStringSubmatch(body(p, w.ben), -1) {
			if strings.Contains(m[1], "://") && !strings.Contains(m[1], "/podcasts/") {
				t.Errorf("%s draws an outside image: %s", p, m[1])
			}
		}
	}
}

// Show notes are cleaned: scripts, frames, event handlers and javascript: links never reach the page.
func TestPodcastNotesAreCleaned(t *testing.T) {
	w := newPodWorld(t)
	evil := `<p onclick="x()">Hello <a href="javascript:alert(1)">bad</a> <a href="https://example.com/ok">good</a></p><script>alert(1)</script><iframe src="https://evil.test"></iframe><img src="https://evil.test/p.png" onerror="x()">`
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_shows SET notes_html = ? WHERE id = ?`, evil, w.show); err != nil {
		t.Fatal(err)
	}
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_episodes SET notes_html = ? WHERE id = ?`, evil, w.eps[0]); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/podcasts/shows/" + w.show, "/podcasts/episodes/" + w.eps[0]} {
		b := w.get(p, w.ben).Body.String()
		for _, bad := range []string{"<script>", "alert(1)", "<iframe", "onclick", "onerror", "javascript:", "evil.test", "<img src=\"https://evil"} {
			if strings.Contains(b, bad) {
				t.Errorf("%s keeps %q", p, bad)
			}
		}
		if !strings.Contains(b, `href="https://example.com/ok"`) || !strings.Contains(b, "nofollow") {
			t.Errorf("%s lost the safe link", p)
		}
	}
	_ = rss.NotesMax
}

// A mark or star returns only to a podcast page of this server.
func TestPodcastBackTarget(t *testing.T) {
	w := newPodWorld(t)
	def := "/podcasts/episodes/" + w.eps[0]
	for _, c := range []struct{ back, want string }{
		{"/podcasts/shows/" + w.show + "?filter=played", "/podcasts/shows/" + w.show + "?filter=played"},
		{"/podcasts?tab=new", "/podcasts?tab=new"},
		{"https://evil.test/podcasts/x", def},
		{"//evil.test/podcasts", def},
		{"/admin/users", def},
		{"/podcastsx", def},
		{"/podcasts/\r\nSet-Cookie: x=1", def},
		{"", def},
	} {
		rec := w.post(t, "/podcasts/episodes/"+w.eps[0]+"/played", w.ben, url.Values{"played": {"1"}, "back": {c.back}})
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != c.want {
			t.Errorf("back %q: %d -> %q, want %q", c.back, rec.Code, rec.Header().Get("Location"), c.want)
		}
	}
}

// Looking at a show clears its new count; the nav badge follows the count.
func TestPodcastNewBadge(t *testing.T) {
	w := newPodWorld(t)
	// Make the follow older than the episodes so they count as new.
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_subscriptions SET seen_at = 1 WHERE show_id = ?`, w.show); err != nil {
		t.Fatal(err)
	}
	home := w.get("/podcasts", w.ben).Body.String()
	if !strings.Contains(home, "ts-navbadge") || !strings.Contains(home, "3 new episodes") {
		t.Error("nav badge missing")
	}
	if !strings.Contains(home, "ts-showcard__new") {
		t.Error("show card badge missing")
	}
	// The same badge is on a page that is not a podcast page.
	if b := w.get("/", w.ben).Body.String(); !strings.Contains(b, `href="/podcasts"`) || !strings.Contains(b, "3 new episodes") {
		t.Error("nav item missing from Home")
	}
	// The show page marks the new ones, then clears the count.
	show := w.get("/podcasts/shows/"+w.show, w.ben).Body.String()
	if strings.Count(show, "ts-eprow__new") != 3 {
		t.Errorf("new marks = %d, want 3", strings.Count(show, "ts-eprow__new"))
	}
	after := w.get("/podcasts", w.ben).Body.String()
	if strings.Contains(after, "ts-navbadge") || strings.Contains(after, "ts-showcard__new") {
		t.Error("count not cleared after looking")
	}
	// Someone who follows nothing has the item but no badge; the owner's off switch removes the item.
	if b := w.get("/", w.cy).Body.String(); !strings.Contains(b, `href="/podcasts"`) || strings.Contains(b, "ts-navbadge") {
		t.Error("nav for a person with no follows")
	}
	if b := w.get("/", w.gus).Body.String(); strings.Contains(b, `href="/podcasts"`) {
		t.Error("a guest sees the Podcasts item")
	}
	w.fx.enable(t, w.a, func(s *podcast.Settings) { s.Enabled = false })
	if b := w.get("/", w.ben).Body.String(); strings.Contains(b, `href="/podcasts"`) {
		t.Error("nav item stays after podcasts were turned off")
	}
	if rec := w.get("/podcasts", w.ben); rec.Code != http.StatusNotFound {
		t.Errorf("home while off: %d", rec.Code)
	}
}

// The owner's page: fields, saved banner, and members and guests are turned away.
func TestPodcastAdminPage(t *testing.T) {
	w := newPodWorld(t)
	b := w.get("/admin/podcasts", w.owner).Body.String()
	for _, want := range []string{"Podcasts and radio", "Let people follow podcasts", "Check for new episodes", "Only when someone looks", "Every hour",
		"Every 6 hours", "Once a day", "Room for saved episodes on this server", "Saved episodes use", "Remove saved episodes", `name="cache_gib"`} {
		if !strings.Contains(b, want) {
			t.Errorf("admin page lacks %q", want)
		}
	}
	if !strings.Contains(w.get("/admin/podcasts?saved=1", w.owner).Body.String(), "Saved") {
		t.Error("saved banner missing")
	}
	if !strings.Contains(w.get("/admin/podcasts?saved=cleared", w.owner).Body.String(), "Saved episodes removed.") {
		t.Error("cleared banner missing")
	}
	for name, ck := range map[string]*http.Cookie{"member": w.ben, "guest": w.gus} {
		if rec := w.get("/admin/podcasts", ck); rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// Settings page link is in the owner's settings list only.
	if !strings.Contains(w.get("/me", w.owner).Body.String(), "/admin/podcasts") {
		t.Error("Profile settings menu lacks the Podcasts and radio link")
	}
	if strings.Contains(w.get("/me", w.ben).Body.String(), "/admin/podcasts") {
		t.Error("a member sees the owner's link")
	}
}

// Search while OndaCast is unreachable still offers the RSS address form, with the plan's sentence.
func TestPodcastSearchDown(t *testing.T) {
	w := newPodWorld(t)
	w.fx.ondaDown.Store(true)
	b := w.get("/podcasts/search?q=found", w.ben).Body.String()
	if !strings.Contains(b, "Search isn&#39;t working right now. You can still paste a show&#39;s RSS address.") || !strings.Contains(b, `name="feed_url"`) {
		t.Errorf("search down page: %s", b)
	}
	_ = context.Background()
}
