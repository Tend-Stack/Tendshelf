package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

// providersSeed adds the site the provider rows act on, and a finished test job of it.
func providersSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	ctx := t.Context()
	id, err := e.a.srv.details.AddCustomProvider(ctx, metadata.CustomProviderInput{Name: "Sweep site", Address: "https://sweep.example.org", Auth: "x"}, e.uid[sweepOwner])
	if err != nil {
		t.Fatal(err)
	}
	e.providerID, e.providerJob = id, "jb_provtest"
	if _, err := e.a.st.Writer().Exec(`INSERT INTO jobs(id, kind, state, payload_json, result_json, created_by, created_at, updated_at)
		VALUES (?, 'metadata.provider_test', 'done', ?, '{"ok":true,"found":1,"code":""}', ?, 1, 1)`,
		e.providerJob, `{"provider_id":"`+id+`"}`, e.uid[sweepOwner]); err != nil {
		t.Fatal(err)
	}
}

// providersSweepRows are the rows of the lookup sites routes (decision 0046). The page is the server
// owner's alone (the server manage action), like the Online switch: admins, members and guests get a 404.
func providersSweepRows() []sweepRow {
	ownerOnlyPage := sweepOwnerOnly(http.StatusOK, http.StatusNotFound)
	ownerOnlyPost := sweepOwnerOnly(http.StatusSeeOther, http.StatusNotFound)
	page := sweepGet("GET /admin/providers", sweepStatic("/admin/providers"), ownerOnlyPage)
	add := sweepPost("POST /admin/providers", sweepStatic("/admin/providers"), sweepFormOf(url.Values{
		"name": {"Sweep added"}, "address": {"https://added.example.org"}, "auth": {"x"}}), ownerOnlyPost, "/admin/providers?added=1")
	test := sweepPost("POST /admin/providers/{id}/test", func(e *sweepEnv, _ int) string { return "/admin/providers/" + e.providerID + "/test" },
		nil, ownerOnlyPost, "")
	poll := sweepGet("GET /hx/admin/providers/test/{job}", func(e *sweepEnv, _ int) string { return "/hx/admin/providers/test/" + e.providerJob }, ownerOnlyPage)
	remove := sweepPost("POST /admin/providers/{id}/remove", func(e *sweepEnv, _ int) string { return "/admin/providers/" + e.providerID + "/remove" },
		nil, ownerOnlyPost, "/admin/providers?removed=1")
	// The poll comes before the removal, which takes the site (and so its test) away.
	return []sweepRow{page, add, test, poll, remove}
}
