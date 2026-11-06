package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// watchSweepRows are the route matrix rows of the watch setting (S1): owner and admin manage libraries;
// a member and a guest get 404.
func watchSweepRows() []sweepRow {
	see := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	return []sweepRow{sweepPost("POST /admin/libraries/{id}/watch", func(e *sweepEnv, _ int) string {
		return "/admin/libraries/" + e.libID + "/watch"
	}, sweepFormOf(url.Values{"on": {"0", "1"}, "every": {"720"}}), see, "/admin/libraries")}
}

func watchSetting(t *testing.T, e *sweepEnv) (bool, int) {
	t.Helper()
	l, err := e.a.lib.Library(context.Background(), e.libID)
	if err != nil {
		t.Fatal(err)
	}
	return l.Watch, l.ScanIntervalMin
}

// TestWatchRoutes: the owner and an admin save the setting; a member, a guest and a missing token change
// nothing; the page shows the section in plain words.
func TestWatchRoutes(t *testing.T) {
	e := sweepSeed(t)
	path := "/admin/libraries/" + e.libID + "/watch"
	body := e.compareGet(sweepOwner, "/admin/libraries", nil).Body.String()
	for _, want := range []string{"Watching", watchLabel, watchEveryLabel, watchStatusOn} {
		if !strings.Contains(body, want) {
			t.Errorf("Libraries page lacks %q", want)
		}
	}
	for _, role := range []int{sweepOwner, sweepAdmin} {
		rec := e.comparePost(t, role, path, url.Values{"on": {"0"}, "every": {"60"}})
		if rec.Code != http.StatusSeeOther || location(rec) != "/admin/libraries" {
			t.Fatalf("%s: %d -> %q", sweepRoleNames[role], rec.Code, location(rec))
		}
		if on, every := watchSetting(t, e); on || every != 60 {
			t.Errorf("%s: watch %v every %d, want off and 60", sweepRoleNames[role], on, every)
		}
	}
	if !strings.Contains(e.compareGet(sweepOwner, "/admin/libraries", nil).Body.String(), watchStatusOff) {
		t.Error("page does not say the library is not watched")
	}
	for _, bad := range []url.Values{{"on": {"1"}, "every": {"7"}}, {"on": {"yes"}, "every": {"60"}}, {"on": {"1"}}} {
		if rec := e.comparePost(t, sweepOwner, path, bad); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", bad, rec.Code)
		}
	}
	for _, role := range []int{sweepMember, sweepGuest} {
		if rec := e.comparePost(t, role, path, url.Values{"on": {"1"}, "every": {"360"}}); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", sweepRoleNames[role], rec.Code)
		}
	}
	if rec := e.a.do(http.MethodPost, path, url.Values{"on": {"1"}, "every": {"360"}}, nil, e.ck[sweepOwner]); rec.Code != http.StatusForbidden {
		t.Errorf("no csrf: %d", rec.Code)
	}
	if on, every := watchSetting(t, e); on || every != 60 {
		t.Errorf("a refused request changed the setting: %v %d", on, every)
	}
	if rec := e.comparePost(t, sweepOwner, "/admin/libraries/nope/watch", url.Values{"on": {"1"}, "every": {"0"}}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d", rec.Code)
	}
}
