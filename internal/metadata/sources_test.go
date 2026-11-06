package metadata_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

func TestSourcesSavedPerPerson(t *testing.T) {
	e := newBulkEnv(t)
	for _, id := range []string{"us_1", "us_2"} {
		if _, err := e.st.Writer().Exec(`INSERT INTO users(id, username, role, created_at) VALUES (?, ?, 'member', 1)`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := e.svc.LoadSources(bg, "us_1"); err != nil || len(got) != 0 {
		t.Fatalf("nothing saved: %v %v", got, err)
	}
	if err := e.svc.SaveSources(bg, "us_1", []string{"itunes", "google", "bogus", "itunes"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.LoadSources(bg, "us_1"); !slices.Equal(got, []string{"itunes"}) {
		t.Errorf("saved: %v", got)
	}
	if got, _ := e.svc.LoadSources(bg, "us_2"); len(got) != 0 {
		t.Errorf("another person sees %v", got)
	}
	// A row that names only the retired place reads as no extra places.
	if _, err := e.st.Writer().Exec(`UPDATE user_settings SET value_json = '{"places":["google"]}' WHERE user_id = 'us_1' AND key = 'lookup.v1'`); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.LoadSources(bg, "us_1"); len(got) != 0 {
		t.Errorf("old google row: %v", got)
	}
}

func TestBulkAndLookupProvidersFollowTheChoice(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"default", nil, "audnexus"},
		{"open library", []string{"openlibrary"}, "audnexus,openlibrary"},
		{"apple books", []string{"itunes"}, "audnexus,itunes"},
		{"all", []string{"openlibrary", "itunes"}, "audnexus,openlibrary,itunes"},
		{"junk and google", []string{"google", "x"}, "audnexus"},
	}
	for _, c := range cases {
		e := newBulkEnv(t)
		e.setHook(func(ctx context.Context, n int, d time.Duration) error { <-ctx.Done(); return ctx.Err() })
		id, err := e.svc.StartBulk(bg, e.lib.ID, "", c.in, "")
		if err != nil {
			t.Fatal(err)
		}
		j, err := e.runner.Get(bg, id)
		if err != nil {
			t.Fatal(err)
		}
		var p metadata.BulkPayload
		if json.Unmarshal(j.Payload, &p) != nil || strings.Join(p.Providers, ",") != c.want {
			t.Errorf("bulk %s: %s", c.name, j.Payload)
		}
		_ = e.svc.StopBulk(bg, e.lib.ID, "")
	}
	e, id := newLookupBook(t)
	for _, c := range cases {
		jid, err := e.svc.StartLookup(bg, id, online.Query{Title: "The Test Match"}, c.in, "")
		if err != nil {
			t.Fatal(err)
		}
		j, err := e.runner.Get(bg, jid)
		if err != nil {
			t.Fatal(err)
		}
		var p metadata.LookupPayload
		if json.Unmarshal(j.Payload, &p) != nil || strings.Join(p.Providers, ",") != c.want {
			t.Errorf("lookup %s: %s", c.name, j.Payload)
		}
	}
}

// A job queued before Google Books was removed still runs, and nothing is sent to it.
func TestQueuedJobNamingGoogleStillRuns(t *testing.T) {
	e := newBulkEnv(t)
	e.addBooks(titles("Old Queue One")...)
	id := e.enqueue(metadata.BulkAll, "audnexus", "google")
	if j := e.finished(id); j.State != jobs.StateDone {
		t.Fatalf("state %s %s", j.State, j.ErrorCode)
	}
	if n := e.urlsWith("googleapis"); n != 0 {
		t.Errorf("%d requests went to Google Books", n)
	}
}
