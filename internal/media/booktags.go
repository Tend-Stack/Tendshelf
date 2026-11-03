package media

import (
	"strconv"
	"strings"
)

// BookInfo is the book's details as they are written into a file.
type BookInfo struct {
	Title, Subtitle, Publisher, Description, Language, ASIN, ISBN, Series, Sequence string
	Year                                                                            int
	Authors, Narrators, Genres                                                      []string
}

// BookTags maps a book's details to ffmpeg tag names. A multi-file book keeps each part's own
// title, so title is left out; album always carries the book title. Empty values are left out.
func BookTags(b BookInfo, multiFile bool) map[string]string {
	tags := map[string]string{}
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			tags[k] = v
		}
	}
	set("album", b.Title)
	if !multiFile {
		set("title", b.Title)
	}
	authors := strings.Join(nonEmpty(b.Authors), ", ")
	set("artist", authors)
	set("album_artist", authors)
	set("composer", strings.Join(nonEmpty(b.Narrators), ", "))
	if b.Year > 0 {
		set("date", strconv.Itoa(b.Year))
	}
	set("genre", strings.Join(nonEmpty(b.Genres), "; "))
	set("publisher", b.Publisher)
	set("description", b.Description)
	set("language", b.Language)
	set("subtitle", b.Subtitle)
	set("series", b.Series)
	set("series-part", b.Sequence)
	set("asin", b.ASIN)
	set("isbn", b.ISBN)
	return tags
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
