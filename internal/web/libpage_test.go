package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

// TestLookupScopePage: the Libraries page offers the three scopes with live counts, "missing details" is the
// default, and the chosen scope reaches the job's payload. A scope nobody knows is refused; members and
// guests get 404 and start nothing.
func TestLookupScopePage(t *testing.T) {
	e := sweepSeed(t)
	body := adminHTMLUnescape(e.compareGet(sweepOwner, "/admin/libraries", nil).Body.String())
	counts, err := e.a.srv.details.CountScopes(context.Background(), e.libID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{lookupScopeMissing(counts.Missing), lookupScopeUnlooked(counts.Unlooked), lookupScopeAll(counts.All)} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if !regexp.MustCompile(`value="missing"[^>]*checked`).MatchString(body) && !regexp.MustCompile(`checked[^>]*value="missing"`).MatchString(body) {
		t.Error("books missing details is not the default")
	}
	path := lookupsLibraryPath(e, "lookups")
	for _, role := range []int{sweepMember, sweepGuest} {
		if rec := e.comparePost(t, role, path, url.Values{"scope": {"all"}}); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", sweepRoleNames[role], rec.Code)
		}
	}
	if _, _, found, _ := e.a.srv.details.BulkStatus(context.Background(), e.libID); found {
		t.Fatal("a refused request started a lookup")
	}
	if rec := e.comparePost(t, sweepOwner, path, url.Values{"scope": {"everything"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown scope: %d", rec.Code)
	}
	form := url.Values{"scope": {"unlooked"}, "_csrf": {e.a.tokenFor(t, e.ck[sweepOwner])}}
	rec := e.a.do(http.MethodPost, path, form, func(r *http.Request) { r.Header.Set("HX-Request", "true") }, e.ck[sweepOwner])
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d", rec.Code)
	}
	j, _, found, err := e.a.srv.details.BulkStatus(context.Background(), e.libID)
	if err != nil || !found {
		t.Fatalf("no job: %v %v", found, err)
	}
	var p metadata.BulkPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil || p.Scope != "unlooked" {
		t.Errorf("payload %s (%v)", j.Payload, err)
	}
}

// TestSettingSavedFragment: a setting changed on the page answers with the quiet "Saved" mark; a plain post
// is redirected; a refusal for htmx is a toast, never plain text.
func TestSettingSavedFragment(t *testing.T) {
	e := sweepSeed(t)
	target := func(id string) func(r *http.Request) {
		return func(r *http.Request) {
			r.Header.Set("HX-Request", "true")
			r.Header.Set("HX-Target", "span#"+id)
		}
	}
	post := func(path string, form url.Values, hdr func(*http.Request)) (int, string) {
		form.Set("_csrf", e.a.tokenFor(t, e.ck[sweepOwner]))
		rec := e.a.do(http.MethodPost, path, form, hdr, e.ck[sweepOwner])
		return rec.Code, rec.Body.String()
	}
	for _, c := range []struct {
		path, id string
		form     url.Values
	}{
		{lookupsLibraryPath(e, "auto-lookup"), "saved-auto-" + e.libID, url.Values{"on": {"0", "1"}}},
		{"/admin/libraries/" + e.libID + "/watch", "saved-watch-" + e.libID, url.Values{"on": {"0", "1"}, "every": {"60"}}},
	} {
		code, body := post(c.path, c.form, target(c.id))
		if code != http.StatusOK || !strings.Contains(body, `id="`+c.id+`"`) || !strings.Contains(body, "Saved") || strings.Contains(body, "<html") {
			t.Errorf("%s: %d %s", c.path, code, body)
		}
		if code, _ := post(c.path, c.form, nil); code != http.StatusSeeOther {
			t.Errorf("%s plain post: %d", c.path, code)
		}
	}
	// A target that is not a Saved mark is not echoed.
	code, body := post("/admin/libraries/"+e.libID+"/watch", url.Values{"on": {"1"}, "every": {"60"}}, target("evil\"><script"))
	if strings.Contains(body, "evil") || code != http.StatusOK || !strings.Contains(body, "Libraries") {
		t.Errorf("unsafe target: %d %s", code, body)
	}
	// An htmx refusal is a toast.
	code, body = post("/admin/libraries/"+e.libID+"/watch", url.Values{"on": {"1"}, "every": {"7"}}, target("saved-watch-"+e.libID))
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, watchErrValue) || !strings.Contains(body, "toast") {
		t.Errorf("refusal: %d %s", code, body)
	}
}
