package metadata_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

// TestLookupScope checks the three scopes of a started lookup: the live counts equal what each scope's job
// really asks about, a book a person accepted (chosen, dismissed or typed by the owner) never counts as
// missing details, and the scope travels in the job's payload.
func TestLookupScope(t *testing.T) {
	// A scope's job runs in its own environment, so one job's records never change the next one's counts.
	for _, scope := range []string{metadata.ScopeMissing, metadata.ScopeUnlooked, metadata.ScopeAll} {
		t.Run(scope, func(t *testing.T) { lookupScopeCase(t, scope) })
	}
}

func lookupScopeCase(t *testing.T, scope string) {
	e := newBulkEnv(t)
	ids := e.addBooks(titles("Plain", "Old Lookup", "Chosen", "Dismissed", "Has Online", "Owner Typed", "Looked Lately", "Failed Before")...)
	plain, old, chosen, dismissed, hasOnline, typed, lately, failed := ids[0], ids[1], ids[2], ids[3], ids[4], ids[5], ids[6], ids[7]
	e.putLookup(old, library.LookupNoMatch, e.now().Add(-90*24*time.Hour))
	e.putLookup(chosen, library.LookupChosen, e.now().Add(-90*24*time.Hour))
	if err := e.svc.DismissLookup(bg, dismissed, "us_1"); err != nil {
		t.Fatal(err)
	}
	e.putValue(hasOnline, metadata.FieldPublisher, metadata.SourceOnline, `"Earlier Press"`)
	if err := e.repo.Update(bg, func(tx *library.Tx) error {
		return tx.PutUserMetadataValue(typed, library.MetadataValue{Field: metadata.FieldDescription, Source: metadata.SourceUser,
			Value: json.RawMessage(`"Typed by the owner"`), ObservedAt: 1})
	}); err != nil {
		t.Fatal(err)
	}
	e.putLookup(lately, library.LookupNoMatch, e.now().Add(-24*time.Hour))
	e.putLookup(failed, library.LookupFailed, e.now().Add(-time.Hour))

	// What each scope should ask about, from the definitions:
	// all: everything not settled lately and not chosen or dismissed; missing: those with nothing online or
	// typed; unlooked: those with no settled lookup on record (a failed one does not count).
	want := map[string][]string{
		metadata.ScopeAll:      {plain, old, hasOnline, typed, failed},
		metadata.ScopeMissing:  {plain, old, failed},
		metadata.ScopeUnlooked: {plain, hasOnline, typed, failed},
	}
	counts, err := e.svc.CountScopes(bg, e.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if counts.All != len(want[metadata.ScopeAll]) || counts.Missing != len(want[metadata.ScopeMissing]) || counts.Unlooked != len(want[metadata.ScopeUnlooked]) {
		t.Fatalf("counts = %+v, want all %d, missing %d, unlooked %d", counts,
			len(want[metadata.ScopeAll]), len(want[metadata.ScopeMissing]), len(want[metadata.ScopeUnlooked]))
	}
	for _, accepted := range []string{chosen, dismissed, typed} {
		if slices.Contains(want[metadata.ScopeMissing], accepted) {
			t.Fatalf("accepted book %s is listed as missing details", accepted)
		}
	}

	// The scope's job asks about exactly the books counted: the others keep their old record.
	id, err := e.svc.StartBulk(bg, e.lib.ID, scope, false, "")
	if err != nil {
		t.Fatal(err)
	}
	j := e.finished(id)
	if j.State != jobs.StateDone {
		t.Fatalf("job %s %s", j.State, j.ErrorCode)
	}
	var p metadata.BulkPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil || p.Scope != scope || p.Mode != metadata.BulkAll || p.LibraryID != e.lib.ID {
		t.Errorf("payload %s (%v)", j.Payload, err)
	}
	if got := e.result(j).Done; got != len(want[scope]) {
		t.Errorf("job handled %d books, count said %d", got, len(want[scope]))
	}
	for _, b := range ids {
		if touched := e.outcomeJob(b) == id; touched != slices.Contains(want[scope], b) {
			t.Errorf("book %s touched = %v", b, touched)
		}
	}
	if _, err := e.svc.StartBulk(bg, e.lib.ID, "everything", false, ""); !errors.Is(err, library.ErrInvalid) {
		t.Errorf("an unknown scope: %v", err)
	}
}

func (e *bulkEnv) outcomeJob(bookID string) string {
	var j string
	if err := e.st.Reader().QueryRow(`SELECT job_id FROM book_lookups WHERE book_id = ?`, bookID).Scan(&j); err != nil {
		return ""
	}
	return j
}
