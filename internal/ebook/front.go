package ebook

import "strings"

var frontKinds = map[string]bool{
	"cover": true, "titlepage": true, "title-page": true, "halftitlepage": true, "copyright-page": true,
	"imprint": true, "toc": true, "dedication": true, "frontmatter": true,
}

var frontTitles = map[string]bool{
	"contents": true, "table of contents": true, "copyright": true, "dedication": true,
	"title page": true, "índice": true, "contenido": true, "dedicatoria": true, "derechos": true,
}

func isFrontKind(k string) bool { return frontKinds[strings.ToLower(k)] }

func isFrontTitle(title string) bool {
	t := strings.ToLower(normalizeString(title))
	t = strings.TrimRight(t, " .:")
	return frontTitles[t]
}

const (
	frontBodyWords  = 300
	frontShortWords = 120
)

// markFront sets Front on chapters by the three rules of the plan. words[i] is chapter i's word
// count and kinds[i] its source kinds.
func markFront(chs []Chapter, kinds [][]string, words []int) {
	firstBody := -1
	for i := range chs {
		if words[i] >= frontBodyWords {
			firstBody = i
			break
		}
	}
	for i := range chs {
		front := isFrontTitle(chs[i].Title)
		for _, k := range kinds[i] {
			if isFrontKind(k) {
				front = true
			}
		}
		if firstBody >= 0 && i < firstBody && words[i] < frontShortWords {
			front = true
		}
		chs[i].Front = front
	}
}
