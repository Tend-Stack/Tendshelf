package ebook

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/share"
)

func suggestIDs(t *testing.T, e *ienv, f share.Filter) map[string]Suggestion {
	t.Helper()
	got, err := e.svc.Suggestions(bg, f, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Suggestion{}
	for _, s := range got {
		out[s.File.ID] = s
	}
	return out
}

func TestSuggestWhatTheEngineLeaves(t *testing.T) {
	e := newIEnv(t, false)
	tbUsers(e)
	// Weak: the same author only. Unsure: two audiobooks with the same title. Confident: the engine takes it.
	tbAudio(e, "a1", "Other Title", "", "Ann Author")
	tbText(e, "weak", "A Different Book", "", "Ann Author")
	tbAudio(e, "d1", "Dune", "", "Frank Herbert")
	tbAudio(e, "d2", "Dune", "", "Frank Herbert")
	tbText(e, "twice", "Dune", "", "Frank Herbert")
	tbAudio(e, "c1", "Certain", "9780306406157")
	tbText(e, "sure", "Certain", "9780306406157")
	got := suggestIDs(t, e, share.Filter{})
	if _, ok := got["sure"]; ok {
		t.Error("a confident match is the engine's, not a suggestion")
	}
	if s, ok := got["weak"]; !ok || s.Match.BookID != "a1" || s.Guard {
		t.Errorf("weak = %+v, %v", s, ok)
	}
	if s, ok := got["twice"]; !ok || s.Guard {
		t.Errorf("twice = %+v, %v", s, ok)
	}
}

func TestSuggestGuardRefused(t *testing.T) {
	e := newIEnv(t, false)
	tbUsers(e)
	tbAudio(e, "c1", "Certain", "9780306406157")
	f := tbText(e, "f1", "Certain", "9780306406157")
	pairTExec(e, `INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('g1', ?, 'book', ?, 1, 1)`, tbUser2, f.BookID)
	s, ok := suggestIDs(t, e, share.Filter{})["f1"]
	if !ok || !s.Guard || s.Match.BookID != "c1" {
		t.Fatalf("suggestion = %+v, %v; want a guard-refused match with c1", s, ok)
	}
}

func TestSuggestFilter(t *testing.T) {
	e := newIEnv(t, false)
	tbAudio(e, "hidden", "Other Title", "", "Ann Author")
	tbText(e, "f1", "A Different Book", "", "Ann Author")
	if got := suggestIDs(t, e, share.Filter{}); len(got) != 1 {
		t.Fatalf("unfiltered = %d", len(got))
	}
	// The caller cannot see the audiobook: nothing is offered, and the title does not leak.
	if got := suggestIDs(t, e, share.Filter{SQL: "b.id <> ?", Args: []any{"hidden"}}); len(got) != 0 {
		t.Errorf("a hidden audiobook was offered: %v", got)
	}
	// Nor can they merge a text book they cannot see.
	f := e.file("f1")
	if got := suggestIDs(t, e, share.Filter{SQL: "b.id <> ?", Args: []any{f.BookID}}); len(got) != 0 {
		t.Errorf("a hidden text book was offered: %v", got)
	}
}

func TestSuggestNotSameAndKeep(t *testing.T) {
	e := newIEnv(t, false)
	tbUsers(e)
	tbAudio(e, "a1", "Other Title", "", "Ann Author")
	f := tbText(e, "f1", "A Different Book", "", "Ann Author")
	g := tbText(e, "f2", "Another Different Book", "", "Ann Author")
	if err := e.svc.NotSame(bg, "f1", "a1", pairTUser); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Keep(bg, "f2", pairTUser); err != nil {
		t.Fatal(err)
	}
	if got := suggestIDs(t, e, share.Filter{}); len(got) != 0 {
		t.Errorf("remembered answers came back: %v", got)
	}
	// Nothing moved: both are still their own books.
	if e.file("f1").BookID != f.BookID || e.file("f2").BookID != g.BookID {
		t.Error("not-same or keep moved a file")
	}
	if e.file("f2").Hold != "kept" || e.count(`SELECT count(*) FROM ebook_not_same WHERE ebook_file_id='f1' AND book_id='a1'`) != 1 {
		t.Error("hold or not-same row missing")
	}
	// A file on an audiobook is not covered.
	pairTExec(e, `UPDATE ebook_files SET book_id='a1' WHERE id='f1'`)
	if err := e.svc.Keep(bg, "f1", pairTUser); err == nil {
		t.Error("Keep took a file that is on an audiobook")
	}
}
