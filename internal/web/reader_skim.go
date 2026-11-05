package web

import (
	"encoding/json"
	"fmt"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

const (
	skimTitleRunes = 60  // a chapter title in the tip is cut here
	skimMaxEntries = 300 // the list on the page never has more entries than this
)

// rsSkim is the data-skim attribute of the reader: JSON [[chars_before, "title"], ...], one entry per chapter in
// reading order. reader-skim.js shows the title of the last entry at or before the thumb. A book with more than
// skimMaxEntries chapters keeps every nth, so the tip names the nearest chapter before the thumb.
func rsSkim(d *ebook.Doc) string {
	type entry struct {
		before int
		title  string
	}
	var all []entry
	before := 0
	for _, c := range d.Chapters {
		title := c.Title
		if title == "" {
			title = fmt.Sprintf(readerChapter, c.Idx)
		}
		if r := []rune(title); len(r) > skimTitleRunes {
			title = string(r[:skimTitleRunes])
		}
		all = append(all, entry{before, title})
		for _, b := range c.Blocks {
			before += rsUnits(b.Text)
		}
	}
	step := (len(all) + skimMaxEntries - 1) / skimMaxEntries
	out := make([][2]any, 0, min(len(all), skimMaxEntries))
	for i := 0; i < len(all); i += max(step, 1) {
		out = append(out, [2]any{all[i].before, all[i].title})
	}
	b, _ := json.Marshal(out)
	return string(b)
}
