package progress_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// wlScope is a scope that sees every book except the hidden ones.
func wlScope(hidden ...string) library.Scope {
	sql, args := "1=1", []any(nil)
	for _, id := range hidden {
		sql += " AND b.id <> ?"
		args = append(args, id)
	}
	return library.Scope{Books: share.Filter{SQL: sql, Args: args}, Libraries: share.Filter{SQL: "1=1"}}
}

// TestWordListRepo: a word is saved once per person and book, folded; the place is kept only when it is real;
// another person sees and deletes nothing; the limit holds.
func TestWordListRepo(t *testing.T) {
	svc, st, u, books, clk := progEnv(t)
	repo := library.New(st)
	b := books[0].ID
	hlEdition(t, st, b, "ed_1")

	w, created, err := svc.AddWord(ctx, progress.Word{UserID: u[0], BookID: b, EditionID: "ed_1", Word: "  Café ", Quote: "A  café\nwith  a view.", SID: "c1.p1.s0", Off: 2})
	if err != nil || !created || !strings.HasPrefix(w.ID, "wd_") || w.Word != "Café" || w.Folded != "cafe" || w.Quote != "A café with a view." {
		t.Fatalf("add = %+v, %v, %v", w, created, err)
	}
	clk.Advance(time.Second)
	if _, again, err := svc.AddWord(ctx, progress.Word{UserID: u[0], BookID: b, Word: "CAFE"}); err != nil || again {
		t.Fatalf("the same word twice: created %v, %v", again, err)
	}
	// A place that is not this book's edition is dropped; the word and its quote stay.
	clk.Advance(time.Second)
	if _, _, err := svc.AddWord(ctx, progress.Word{UserID: u[0], BookID: books[1].ID, EditionID: "ed_1", Word: "serene", Quote: "calm", SID: "c1.p1.s0"}); err != nil {
		t.Fatal(err)
	}
	// Bad words.
	for name, in := range map[string]progress.Word{
		"empty":   {UserID: u[0], BookID: b, Word: "   "},
		"long":    {UserID: u[0], BookID: b, Word: strings.Repeat("w", 65)},
		"symbols": {UserID: u[0], BookID: b, Word: "***"},
	} {
		var bad *progress.InvalidError
		if _, _, err := svc.AddWord(ctx, in); !errors.As(err, &bad) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := svc.AddWord(ctx, progress.Word{UserID: u[0], BookID: "bk_nope", Word: "x"}); !errors.Is(err, progress.ErrNotFound) {
		t.Errorf("unknown book: %v", err)
	}

	list, next, err := repo.WordList(ctx, wlScope(), u[0], "", "", 10)
	if err != nil || next != "" || len(list) != 2 || list[0].Word != "serene" || list[0].EditionID != "" || list[0].SID != "" || list[0].Quote != "calm" ||
		list[1].EditionID != "ed_1" || list[1].SID != "c1.p1.s0" || list[1].Off != 2 || list[1].BookTitle != "B0" {
		t.Fatalf("list = %+v, next %q, %v", list, next, err)
	}
	if one, _, _ := repo.WordList(ctx, wlScope(), u[0], b, "", 10); len(one) != 1 || one[0].Word != "Café" {
		t.Errorf("one book: %+v", one)
	}
	// Pages: the cursor walks newest first with nothing twice.
	p1, next, err := repo.WordList(ctx, wlScope(), u[0], "", "", 1)
	if err != nil || len(p1) != 1 || next == "" {
		t.Fatalf("page 1 = %+v, %q, %v", p1, next, err)
	}
	p2, next, err := repo.WordList(ctx, wlScope(), u[0], "", next, 1)
	if err != nil || len(p2) != 1 || next != "" || p2[0].ID == p1[0].ID {
		t.Fatalf("page 2 = %+v, %q, %v", p2, next, err)
	}
	if _, _, err := repo.WordList(ctx, wlScope(), u[0], "", "garbage", 1); err == nil {
		t.Error("a bad cursor was accepted")
	}

	// Another person: empty list, cannot delete, can save the same word on their own.
	if other, _, _ := repo.WordList(ctx, wlScope(), u[1], "", "", 10); len(other) != 0 {
		t.Fatalf("B lists A's words: %+v", other)
	}
	if err := svc.DeleteWord(ctx, u[1], w.ID); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("B deletes A's word: %v", err)
	}
	if _, made, err := svc.AddWord(ctx, progress.Word{UserID: u[1], BookID: b, Word: "café"}); err != nil || !made {
		t.Fatalf("B saves the same word: %v %v", made, err)
	}

	// A book the person can no longer see lists nothing, and keeps the word stored.
	if hidden, _, _ := repo.WordList(ctx, wlScope(books[1].ID), u[0], "", "", 10); len(hidden) != 1 || hidden[0].BookTitle != "B0" {
		t.Fatalf("a hidden book listed: %+v", hidden)
	}
	if n, _ := svc.CountWords(ctx, u[0]); n != 2 {
		t.Errorf("CountWords = %d, want 2", n)
	}
	if err := svc.DeleteWord(ctx, u[0], w.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteWord(ctx, u[0], w.ID); !errors.Is(err, progress.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}

	// The limit counts every word the person keeps; a word already saved still answers.
	old := progress.MaxWordsPerPerson
	progress.MaxWordsPerPerson = 3
	defer func() { progress.MaxWordsPerPerson = old }()
	for i := 0; i < 2; i++ {
		if _, _, err := svc.AddWord(ctx, progress.Word{UserID: u[0], BookID: b, Word: fmt.Sprintf("word%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := svc.AddWord(ctx, progress.Word{UserID: u[0], BookID: b, Word: "one too many"}); !errors.Is(err, progress.ErrLimit) {
		t.Errorf("over the limit: %v", err)
	}
	if _, made, err := svc.AddWord(ctx, progress.Word{UserID: u[0], BookID: b, Word: "WORD1"}); err != nil || made {
		t.Errorf("a saved word at the limit: %v %v", made, err)
	}
}
