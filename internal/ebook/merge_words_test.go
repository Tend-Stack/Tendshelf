package ebook

import "testing"

// wlWord saves one word for the test person on a book.
func wlWord(e *ienv, id, book, edition, folded string) {
	pairTExec(e, `INSERT INTO word_list(id, user_id, book_id, edition_id, word, folded, quote, sid, off, created_at)
VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, 'a quote', 'c1.p1.s0', 2, 1)`, id, pairTUser, book, edition, folded, folded)
}

// TestWordListMerge: words follow a text book into the audiobook it merges with, a word both books hold stays once
// (the audiobook's), and a split sends the ebook edition's words back with the ebook.
func TestWordListMerge(t *testing.T) {
	e := newIEnv(t, false)
	tbUsers(e)
	tbAudio(e, "a1", "Words", "9780306406157")
	f := tbText(e, "f1", "Words", "9780306406157")
	old := f.BookID
	wlWord(e, "wd_text", old, "ed_f1", "serene")
	wlWord(e, "wd_both_t", old, "ed_f1", "both")
	wlWord(e, "wd_both_a", "a1", "", "both")
	if err := e.svc.mergePending(bg, "", ""); err != nil || !tbMerged(e, old) {
		t.Fatalf("merge: %v", err)
	}
	if e.count(`SELECT count(*) FROM word_list WHERE id='wd_text' AND book_id='a1' AND quote='a quote'`) != 1 {
		t.Error("the text book's word did not move, or lost its quote")
	}
	if e.count(`SELECT count(*) FROM word_list WHERE user_id=? AND book_id='a1' AND folded='both'`, pairTUser) != 1 {
		t.Error("a word both books held is not stored exactly once")
	}
	if e.count(`SELECT count(*) FROM word_list WHERE book_id=?`, old) != 0 {
		t.Error("words left on the merged-away book")
	}
	if err := e.svc.Unpair(bg, "f1", "a1", pairTUser); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM word_list WHERE id='wd_text' AND book_id=?`, old) != 1 {
		t.Error("the ebook's word did not go back with the ebook")
	}
	if e.count(`SELECT count(*) FROM word_list WHERE id='wd_both_a' AND book_id='a1'`) != 1 {
		t.Error("the audiobook's own word moved")
	}
}
