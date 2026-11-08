package web

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// After "Nothing close turned up", Try again brings back the query that job searched, not the book's own title
// and author. It is a plain address, so it works without JavaScript, and the edit right is still checked.
func TestCompareTryAgainKeepsQuery(t *testing.T) {
	e := sweepSeed(t)
	id := e.book.ID
	job, err := e.a.srv.details.StartLookup(context.Background(), id,
		online.Query{Title: "Typed Title", Author: "Typed Author", ASIN: "B0TYPED001"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.st.Writer().Exec(`UPDATE jobs SET state = 'done', result_json = '{}' WHERE id = ?`, job); err != nil {
		t.Fatal(err)
	}
	rec := e.compareGet(sweepOwner, compareSheetPath(e, job, ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("sheet: %d", rec.Code)
	}
	m := regexp.MustCompile(`ts-btn--secondary" href="([^"]*sheet=lookup[^"]*)"`).FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("no Try again link in %.300s", rec.Body.String())
	}
	href := html.UnescapeString(m[1])
	rec = e.compareGet(sweepOwner, href, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("form: %d", rec.Code)
	}
	for _, w := range []string{`value="Typed Title"`, `value="Typed Author"`, `value="B0TYPED001"`} {
		if !strings.Contains(body, w) {
			t.Errorf("Try again form misses %s", w)
		}
	}
	if strings.Contains(body, `value="Sweepword Lantern"`) {
		t.Error("Try again form shows the book's own title")
	}
	// Through hx-get as well.
	if rec = e.compareGet(sweepOwner, href, compareHx); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `value="Typed Title"`) {
		t.Errorf("hx-get: %d", rec.Code)
	}
	// A value past the cap is cut like a posted one.
	long := "/books/" + id + "/details?sheet=lookup&title=" + strings.Repeat("a", lookupFieldRunes+40)
	if body = e.compareGet(sweepOwner, long, nil).Body.String(); !strings.Contains(body, `value="`+strings.Repeat("a", lookupFieldRunes)+`"`) || strings.Contains(body, strings.Repeat("a", lookupFieldRunes+1)) {
		t.Error("title not capped")
	}
	// Without the query, the form still starts from the book's own details.
	if body = e.compareGet(sweepOwner, "/books/"+id+"/details?sheet=lookup", nil).Body.String(); !strings.Contains(body, `value="Sweepword Lantern"`) {
		t.Error("plain form lost the book's title")
	}
	// A person who may not edit the book gets 404, query or not.
	for _, role := range []int{sweepMember, sweepGuest} {
		if rec := e.compareGet(role, href, nil); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Typed Title") {
			t.Errorf("%s: %d, want 404", sweepRoleNames[role], rec.Code)
		}
	}
}
