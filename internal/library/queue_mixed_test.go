package library_test

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// seedEpisodes adds a show with n episodes (pe_0..) that us_a follows; us_b has no access.
func seedEpisodes(t *testing.T, w *sql.DB, n int) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO podcast_shows(id, feed_url, title, created_at, updated_at) VALUES ('ps_1', 'https://a.example/f.xml', 'The Show', 1, 1)`,
		`INSERT INTO podcast_subscriptions(user_id, show_id, followed_at, seen_at) VALUES ('us_a', 'ps_1', 1, 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		if _, err := w.Exec(`INSERT INTO podcast_episodes(id, show_id, guid_key, title, audio_url, created_at, updated_at) VALUES (?, 'ps_1', ?, ?, 'https://a.example/x.mp3', 1, 1)`,
			fmt.Sprintf("pe_%d", i), fmt.Sprintf("g%d", i), fmt.Sprintf("E%d", i)); err != nil {
			t.Fatal(err)
		}
	}
}

func queueMixed(t *testing.T, r *library.Repo, sc library.Scope, user string, k library.QueueKinds) string {
	t.Helper()
	list, err := r.QueueIn(ctx, sc, user, k)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range list {
		if e.Kind == library.QueueKindEpisode {
			out = append(out, e.Episode.Title)
		} else {
			out = append(out, e.Book.Title)
		}
	}
	return strings.Join(out, ",")
}

// A mixed queue keeps one order for books and episodes; a caller that does not ask for episodes
// never sees, counts, moves past or pops them; an episode the person cannot open stays hidden.
func TestQueueMixedOrder(t *testing.T) {
	r, st, b, _ := shelfSeed(t, 2)
	seedEpisodes(t, st.Writer(), 2)
	queueAdd(t, r, "us_a", b[0], false)
	if added, err := r.QueueAddEpisode(ctx, "us_a", "pe_0", false); err != nil || !added {
		t.Fatalf("add episode = %v, %v", added, err)
	}
	queueAdd(t, r, "us_a", b[1], false)
	if added, err := r.QueueAddEpisode(ctx, "us_a", "pe_1", true); err != nil || !added {
		t.Fatalf("play next episode = %v, %v", added, err)
	}
	if got := queueMixed(t, r, allScope, "us_a", library.QueueAll); got != "E1,B0,E0,B1" {
		t.Errorf("mixed = %s", got)
	}
	if got := queueMixed(t, r, allScope, "us_a", library.QueueBooks); got != "B0,B1" {
		t.Errorf("books only = %s", got)
	}
	if n, _ := r.QueueCountIn(ctx, allScope, "us_a", library.QueueAll); n != 4 {
		t.Errorf("mixed count = %d", n)
	}
	if n, _ := r.QueueCount(ctx, allScope, "us_a"); n != 2 {
		t.Errorf("book count = %d", n)
	}
	if added, err := r.QueueAddEpisode(ctx, "us_a", "pe_0", false); err != nil || added {
		t.Errorf("re-add = %v, %v", added, err)
	}
	if _, err := r.QueueAddEpisode(ctx, "us_a", "pe_nope", false); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown episode = %v", err)
	}
	if err := r.QueueMoveIn(ctx, allScope, "us_a", "bk_nope", 1, library.QueueAll); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("move of an item not queued = %v", err)
	}
	// E0 moves up across kinds; B0 moving down as a books-only caller steps over the episode.
	if err := r.QueueMoveIn(ctx, allScope, "us_a", "pe_0", -1, library.QueueAll); err != nil {
		t.Fatal(err)
	}
	if got := queueMixed(t, r, allScope, "us_a", library.QueueAll); got != "E1,E0,B0,B1" {
		t.Errorf("after move = %s", got)
	}
	if err := r.QueueMoveIn(ctx, allScope, "us_a", b[0].ID, 1, library.QueueBooks); err != nil {
		t.Fatal(err)
	}
	if got := queueMixed(t, r, allScope, "us_a", library.QueueAll); got != "E1,E0,B1,B0" {
		t.Errorf("books-only move = %s", got)
	}
	if got := queueMixed(t, r, allScope, "us_b", library.QueueAll); got != "" {
		t.Errorf("other person = %q", got)
	}
	// A books-only pop leaves the episodes in place.
	it, err := r.QueuePopIn(ctx, allScope, "us_a", "", library.QueueBooks)
	if err != nil || it.Kind != library.QueueKindBook || it.ID != b[1].ID {
		t.Errorf("books-only pop = %+v, %v", it, err)
	}
	// A mixed pop gives the first item whatever its kind, and drops the ended one.
	it, err = r.QueuePopIn(ctx, allScope, "us_a", "pe_1", library.QueueAll)
	if err != nil || it.Kind != library.QueueKindEpisode || it.ID != "pe_0" {
		t.Errorf("mixed pop = %+v, %v", it, err)
	}
	if got := queueMixed(t, r, allScope, "us_a", library.QueueAll); got != "B0" {
		t.Errorf("left = %s", got)
	}
	// An episode of an unfollowed show with no state row is hidden and kept; a state row brings it back.
	if _, err := r.QueueAddEpisode(ctx, "us_a", "pe_0", false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Writer().Exec(`DELETE FROM podcast_subscriptions`); err != nil {
		t.Fatal(err)
	}
	if got := queueMixed(t, r, allScope, "us_a", library.QueueAll); got != "B0" {
		t.Errorf("unfollowed = %s", got)
	}
	if _, err := st.Writer().Exec(`INSERT INTO podcast_episode_state(user_id, episode_id, updated_at) VALUES ('us_a', 'pe_0', 1)`); err != nil {
		t.Fatal(err)
	}
	if got := queueMixed(t, r, allScope, "us_a", library.QueueAll); got != "B0,E0" {
		t.Errorf("with a state row = %s", got)
	}
	if err := r.QueueRemove(ctx, "us_a", "pe_0"); err != nil {
		t.Errorf("remove episode = %v", err)
	}
}

// An opened show's queued episode lists for the opener only; another person's identical row stays hidden.
func TestQueueEpisodeOfOpenedShow(t *testing.T) {
	r, st, _, _ := shelfSeed(t, 1)
	seedEpisodes(t, st.Writer(), 1)
	w := st.Writer()
	for _, q := range []string{
		`DELETE FROM podcast_subscriptions`,
		`INSERT INTO podcast_opens(user_id, show_id, opened_at) VALUES ('us_a', 'ps_1', 1)`,
		`INSERT INTO queue_items(user_id, item_id, episode_id, position, added_at) VALUES ('us_a', 'pe_0', 'pe_0', 1, 1), ('us_b', 'pe_0', 'pe_0', 1, 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if got := queueMixed(t, r, allScope, "us_a", library.QueueAll); got != "E0" {
		t.Errorf("opener queue = %q", got)
	}
	if got := queueMixed(t, r, allScope, "us_b", library.QueueAll); got != "" {
		t.Errorf("other person's queue = %q", got)
	}
}
