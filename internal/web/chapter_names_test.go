package web

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

func TestChapterNames(t *testing.T) {
	for _, c := range []struct {
		name   string
		titles []string
		nums   []int
		total  int
	}{
		{"prologue and epilogue", []string{"Prologue: 9:17 P.M.", "Chapter 1: Mud", "Chapter 2: Rain", "Epilogue"}, []int{0, 1, 2, 0}, 2},
		{"credits", []string{"Opening Credits", "Chapter 1", "Chapter 2", "End Credits"}, []int{0, 1, 2, 0}, 2},
		{"a number far from the place", []string{"Prologue", "Chapter 13: Mud", "Chapter 14"}, []int{0, 13, 14}, 2},
		{"blank titles", []string{"", "", "", ""}, []int{1, 2, 3, 4}, 4},
		{"track names", []string{"Track 01", "Track 02"}, []int{1, 2}, 2},
		{"named chapters", []string{"The Lamp", "The Road", "The Sea"}, []int{1, 2, 3}, 3},
		{"spanish", []string{"Prólogo", "Capítulo 3", "Capítulo 4"}, []int{0, 3, 4}, 2},
		{"one numbered among named", []string{"Intro", "Chapter 7", "Outro"}, []int{0, 7, 0}, 1},
		{"empty", nil, []int{}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			n := newChapterNames(c.titles)
			if !reflect.DeepEqual(n.Nums, c.nums) || n.Total != c.total {
				t.Errorf("%q: nums %v total %d; want %v and %d", c.titles, n.Nums, n.Total, c.nums, c.total)
			}
		})
	}
}

func TestChapterWording(t *testing.T) {
	type words struct{ name, of, inline string }
	n := newChapterNames([]string{"Prologue: 9:17 P.M.", "Chapter 13: Mud", "Chapter 14", "", "Opening Credits"})
	for i, want := range []words{
		{"Prologue", "Prologue", "Prologue"},
		{"Chapter 13", "Chapter 13 of 2", "chapter 13"},
		{"Chapter 14", "Chapter 14 of 2", "chapter 14"},
		{"Chapter 4", "Chapter 4", "Chapter 4"}, // no title and no number: the place
		{"Opening Credits", "Opening Credits", "Opening Credits"},
	} {
		if got := (words{n.Name(i), n.Of(i), n.Inline(i)}); got != want {
			t.Errorf("chapter %d: %+v, want %+v", i, got, want)
		}
	}
}

func TestChapterShortName(t *testing.T) {
	for in, want := range map[string]string{
		"Prologue: 9:17 P.M.": "Prologue",
		"Interlude - The Sea": "Interlude",
		"Interlude – The Sea": "Interlude",
		"Opening Credits":     "Opening Credits",
		"  Epilogue  ":        "Epilogue",
		"Part 1: A - B":       "Part 1",
		"Well-known: a thing": "Well-known",
		"":                    "",
	} {
		if got := chapterShortName(in); got != want {
			t.Errorf("chapterShortName(%q) = %q, want %q", in, got, want)
		}
	}
}

// prologueBook gives the play-button book a Prologue track before its numbered chapters.
func prologueBook(t *testing.T) *progRouteEnv {
	t.Helper()
	e := playBtnEnv(t)
	chs := []library.Chapter{
		{StartMs: 0, EndMs: 60_000, Title: "Prologue: 9:17 P.M.", Source: library.ChapterFile},
		{StartMs: 60_000, EndMs: 120_000, Title: "Chapter 13: Mud", Source: library.ChapterFile},
		{StartMs: 120_000, EndMs: 180_000, Title: "Chapter 14: Rain", Source: library.ChapterFile},
	}
	if err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(e.book.ID, chs) }); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestBookPageSaysTheChaptersOwnNumber(t *testing.T) {
	e := prologueBook(t)
	region := func() string { return playBtnGet(e, "/hx/books/"+e.book.ID+"/play") }

	playBtnProgress(t, e, e.ownerID, e.book.ID, 90_000, 0, 1_700_000_000_000) // in the second track
	reg := region()
	for _, want := range []string{"Chapter 13 · 1 min left in chapter · ", `ts-book__whereshort">Chapter 13 · 1 min left<`, `aria-label="Book progress, chapter 13 of 2"`} {
		if !strings.Contains(reg, want) {
			t.Errorf("in the second track, the region lacks %q: %s", want, reg)
		}
	}
	playBtnProgress(t, e, e.ownerID, e.book.ID, 30_000, 0, 1_700_000_000_001) // in the Prologue
	reg = region()
	for _, want := range []string{"Prologue · 1 min left in chapter · ", `ts-book__whereshort">Prologue · 1 min left<`, `aria-label="Book progress, Prologue"`} {
		if !strings.Contains(reg, want) {
			t.Errorf("in the Prologue, the region lacks %q: %s", want, reg)
		}
	}
	// The chapters tab shows each chapter's own number and none for the Prologue.
	body := bookMain(playBtnGet(e, "/books/"+e.book.ID+"/chapters"))
	for _, want := range []string{">13<", ">14<"} {
		if !strings.Contains(body, want) {
			t.Errorf("chapters tab lacks the number %s", want)
		}
	}
	if strings.Contains(body, ">1<") || strings.Contains(body, ">2<") || strings.Contains(body, ">3<") {
		t.Error("chapters tab counts the tracks")
	}
}

func TestPlayerDataCarriesChapterNumbers(t *testing.T) {
	e := prologueBook(t)
	body := e.a.do(http.MethodGet, "/hx/player/"+e.book.ID, nil, nil, e.owner).Body.String()
	for _, want := range []string{`data-ts-chapters data-total="2"`, `data-idx="0" data-num="0"`, `data-idx="1" data-num="13"`, `data-idx="2" data-num="14"`} {
		if !strings.Contains(body, want) {
			t.Errorf("player data lacks %q", want)
		}
	}
	// Chapters that carry no number keep their places.
	chs := []library.Chapter{
		{StartMs: 0, EndMs: 60_000, Title: "One", Source: library.ChapterFile}, {StartMs: 60_000, EndMs: 180_000, Title: "", Source: library.ChapterFile},
	}
	if err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(e.book.ID, chs) }); err != nil {
		t.Fatal(err)
	}
	body = e.a.do(http.MethodGet, "/hx/player/"+e.book.ID, nil, nil, e.owner).Body.String()
	for _, want := range []string{`data-total="2"`, `data-idx="0" data-num="1"`, `data-idx="1" data-num="2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("unnumbered book: player data lacks %q", want)
		}
	}
}
