package podcast

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// Chapter is one chapter of an episode on its own timeline. Only the start and the title are kept from a
// Podcasting 2.0 chapters file; pictures and links in it are never stored or sent on.
type Chapter struct {
	StartMs int64  `json:"startMs"`
	Title   string `json:"title"`
}

const (
	maxChapters     = 500
	maxChapterTitle = 200
)

// chaptersFile is the part of a Podcasting 2.0 chapters file Tendshelf reads.
type chaptersFile struct {
	Chapters []struct {
		StartTime float64 `json:"startTime"`
		Title     string  `json:"title"`
		Toc       *bool   `json:"toc"`
	} `json:"chapters"`
}

// ParseChapters reads a chapters file (Podcasting 2.0 JSON) into chapters in time order. Entries the file
// marks as not part of the table of contents ("toc": false), negative or unreadable starts and repeated
// starts are dropped; titles are trimmed and cut. An empty result means the file had nothing usable.
func ParseChapters(raw []byte) []Chapter {
	var f chaptersFile
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	out := make([]Chapter, 0, len(f.Chapters))
	for _, c := range f.Chapters {
		if (c.Toc != nil && !*c.Toc) || c.StartTime < 0 || math.IsNaN(c.StartTime) || c.StartTime > 7*24*3600 {
			continue
		}
		title := strings.TrimSpace(c.Title)
		if utf8.RuneCountInString(title) > maxChapterTitle {
			title = string([]rune(title)[:maxChapterTitle])
		}
		out = append(out, Chapter{StartMs: int64(math.Round(c.StartTime * 1000)), Title: title})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	kept := out[:0]
	for _, c := range out {
		if len(kept) > 0 && kept[len(kept)-1].StartMs == c.StartMs {
			continue
		}
		kept = append(kept, c)
		if len(kept) == maxChapters {
			break
		}
	}
	return kept
}

// ChaptersJSON is the stored form of an episode's chapters: Podcasting 2.0 "chapters[].startTime,title" only.
func ChaptersJSON(chapters []Chapter) []byte {
	type entry struct {
		StartTime float64 `json:"startTime"`
		Title     string  `json:"title"`
	}
	list := make([]entry, 0, len(chapters))
	for _, c := range chapters {
		list = append(list, entry{StartTime: float64(c.StartMs) / 1000, Title: c.Title})
	}
	b, _ := json.Marshal(map[string]any{"version": "1.2.0", "chapters": list})
	return b
}
