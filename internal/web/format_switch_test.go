package web

import (
	"reflect"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

func TestMapChapters(t *testing.T) {
	front := fmtChapter{Title: "Title Page", Front: true}
	tests := []struct {
		name  string
		audio []string
		text  []fmtChapter
		want  chapterMap // zero value means no match
	}{
		{"exact titles", []string{"The Wind", "The Sea"},
			[]fmtChapter{front, {Title: "The Wind"}, {Title: "The Sea"}},
			chapterMap{AudioToText: []int{2, 3}, TextToAudio: []int{-1, 0, 1}}},
		{"number against longer title", []string{"Chapter 3", "Chapter 4"},
			[]fmtChapter{{Title: "Chapter 3: The Wind"}, {Title: "Chapter 4: The Sea"}},
			chapterMap{AudioToText: []int{1, 2}, TextToAudio: []int{0, 1}}},
		{"accents and punctuation", []string{"Café, Noir!"}, []fmtChapter{{Title: "Cafe Noir"}},
			chapterMap{AudioToText: []int{1}, TextToAudio: []int{0}}},
		{"prologue and epilogue", []string{"Prologue", "Chapter 1", "Epilogue"},
			[]fmtChapter{{Title: "Prologue: Before"}, {Title: "Chapter 1 - Start"}, {Title: "Epilogue: After"}},
			chapterMap{AudioToText: []int{1, 2, 3}, TextToAudio: []int{0, 1, 2}}},
		{"front matter is never matched", []string{"Title Page", "One"},
			[]fmtChapter{front, {Title: "One"}},
			chapterMap{AudioToText: []int{0, 2}, TextToAudio: []int{-1, 1}}},
		{"numbers that restart in part two stay unmatched", []string{"Chapter 1", "Chapter 2", "Chapter 1", "Chapter 2"},
			[]fmtChapter{{Title: "Chapter 1"}, {Title: "Chapter 2"}, {Title: "Chapter 1"}, {Title: "Chapter 2"}}, chapterMap{}},
		{"an order inversion gives nothing", []string{"Alpha", "Beta"},
			[]fmtChapter{{Title: "Beta"}, {Title: "Alpha"}}, chapterMap{}},
		{"track names give nothing", []string{"Track 01", "Track 02"},
			[]fmtChapter{{Title: "Chapter 1"}, {Title: "Chapter 2"}}, chapterMap{}},
		{"empty audio", nil, []fmtChapter{{Title: "One"}}, chapterMap{}},
		{"empty text", []string{"One"}, nil, chapterMap{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapChapters(tc.audio, tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAudioChapterAt(t *testing.T) {
	chs := []library.Chapter{{StartMs: 0}, {StartMs: 1000}, {StartMs: 5000}}
	for ms, want := range map[int64]int{-1: -1, 0: 0, 999: 0, 1000: 1, 7000: 2} {
		if got := audioChapterAt(chs, ms); got != want {
			t.Errorf("ms %d: %d, want %d", ms, got, want)
		}
	}
	if audioChapterAt(nil, 5) != -1 {
		t.Error("no chapters")
	}
}

func TestOrderOptions(t *testing.T) {
	older := formatOption{Label: "older", at: 10}
	newer := formatOption{Label: "newer", at: 20}
	start := formatOption{Label: "start"}
	got := orderOptions([]formatOption{older, newer}, start)
	var labels []string
	for _, o := range got {
		labels = append(labels, o.Label)
	}
	if !reflect.DeepEqual(labels, []string{"newer", "older", "start"}) {
		t.Errorf("order %v", labels)
	}
	if !got[0].Primary || got[1].Primary || got[2].Primary {
		t.Errorf("only the first is primary: %+v", got)
	}
	if only := orderOptions(nil, start); len(only) != 1 || !only[0].Primary {
		t.Errorf("start alone: %+v", only)
	}
}
