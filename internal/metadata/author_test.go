package metadata_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

func TestLookupAuthor(t *testing.T) {
	e, id := newLookupBook(t)
	people, _ := e.repo.BookPeople(bg, id)
	var pid string
	for _, p := range people {
		if p.Name == "Ann Author" {
			pid = p.PersonID
		}
	}
	if pid == "" {
		t.Fatal("no author")
	}
	jid, err := e.svc.StartAuthorLookup(bg, pid, "")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(jid); j.State != jobs.StateDone {
		t.Fatalf("author job: %s %s", j.State, j.ErrorCode)
	}
	p, err := e.repo.PersonByID(bg, pid)
	if err != nil {
		t.Fatal(err)
	}
	if p.Description != "Ann Author writes synthetic romances." || p.ASIN != "B0AUTHOR01" || p.ImageID == "" {
		t.Fatalf("person: %+v", p)
	}
	dir := metadata.PeopleDir(e.dataDir)
	sha := p.ImageID[:len(p.ImageID)-len(filepath.Ext(p.ImageID))]
	for _, f := range []string{p.ImageID, sha + "-320.jpg"} {
		if st, err := os.Stat(filepath.Join(dir, f)); err != nil || st.Size() == 0 {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := e.svc.StartAuthorLookup(bg, "pe_missing", ""); err == nil {
		t.Error("unknown person")
	}
	if err := metadata.SetOnlineEnabled(bg, e.st, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartAuthorLookup(bg, pid, ""); err != metadata.ErrOnlineOff {
		t.Errorf("while off: %v", err)
	}
}
