package online

import (
	"context"
	"encoding/json"
	"net/url"
)

// maxChapterStores is how many Audnexus stores one chapter lookup asks before it gives up.
const maxChapterStores = 3

// maxChapterItems bounds what one answer may hold; longer lists are cut.
const maxChapterItems = 1000

// ChapterItem is one chapter as Audible lists it.
type ChapterItem struct {
	StartMs  int64  `json:"start_ms"`
	LengthMs int64  `json:"length_ms"`
	Title    string `json:"title"`
}

// ChapterList is Audible's chapter list for one ASIN, read from Audnexus /books/{asin}/chapters.
// Sent counts the requests really sent (not answered from the saved copy), for the daily count.
type ChapterList struct {
	ASIN      string        `json:"asin"`
	Region    string        `json:"region"`
	Accurate  bool          `json:"accurate"`
	RuntimeMs int64         `json:"runtime_ms"`
	IntroMs   int64         `json:"intro_ms"`
	OutroMs   int64         `json:"outro_ms"`
	Chapters  []ChapterItem `json:"chapters"`
	Sent      int           `json:"sent"`
}

// parseChapters reads the Audnexus chapters answer. It returns nil when there is no chapter in it.
func parseChapters(raw json.RawMessage) *ChapterList {
	var in struct {
		ASIN      string `json:"asin"`
		Region    string `json:"region"`
		Accurate  bool   `json:"isAccurate"`
		RuntimeMs int64  `json:"runtimeLengthMs"`
		IntroMs   int64  `json:"brandIntroDurationMs"`
		OutroMs   int64  `json:"brandOutroDurationMs"`
		Chapters  []struct {
			LengthMs int64  `json:"lengthMs"`
			StartMs  int64  `json:"startOffsetMs"`
			Title    string `json:"title"`
		} `json:"chapters"`
	}
	if json.Unmarshal(raw, &in) != nil || len(in.Chapters) == 0 {
		return nil
	}
	out := &ChapterList{ASIN: in.ASIN, Region: in.Region, Accurate: in.Accurate, RuntimeMs: in.RuntimeMs, IntroMs: in.IntroMs, OutroMs: in.OutroMs}
	for _, c := range in.Chapters {
		if len(out.Chapters) >= maxChapterItems {
			break
		}
		out.Chapters = append(out.Chapters, ChapterItem{StartMs: c.StartMs, LengthMs: c.LengthMs, Title: c.Title})
	}
	return out
}

// Chapters asks Audnexus for the chapters of one ASIN. The store that fits the language is asked
// first and an answer of "not sold here" tries the next, at most three stores. When none has the
// book the error is "no_chapters".
func (c *Client) Chapters(ctx context.Context, asin, language string) (ChapterList, error) {
	rec := &recorder{cur: -1}
	ctx = context.WithValue(ctx, recorderKey{}, rec)
	sent := func() int {
		n := 0
		for _, s := range rec.snapshot() {
			n += s.Sent
		}
		return n
	}
	for _, region := range regionOrder(language)[:maxChapterStores] {
		u := "https://api.audnex.us/books/" + url.PathEscape(asin) + "/chapters"
		if region != "" {
			u += "?region=" + region
		}
		rec.begin("audnexus", "Audnexus chapters", regionLabel(region), asin)
		raw, err := c.getJSON(ctx, "audnexus", u)
		if err != nil {
			rec.end(0, err)
			return ChapterList{Sent: sent()}, err
		}
		list := parseChapters(raw)
		if list == nil {
			rec.end(0, nil)
			continue
		}
		rec.end(len(list.Chapters), nil)
		list.Sent = sent()
		return *list, nil
	}
	return ChapterList{Sent: sent()}, &Error{Code: "no_chapters", Site: "Audnexus"}
}
