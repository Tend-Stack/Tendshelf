// Port of TendVox Extractor extractor_details.py (meta_from_tags, clean_text, clean_asin, clean_isbn,
// clean_year) with the author tag order of Audiobookshelf AudioFileScanner; see NOTICES.md.

package metadata

import (
	"regexp"
	"strconv"
	"strings"
)

const descriptionLimit = 10000

var (
	htmlTagRe     = regexp.MustCompile(`<[^>]+>`)
	narratedByRe  = regexp.MustCompile(`(?i)(?:narrated|read)\s+by[:\s]+([^\n.;|<]{2,80})`)
	seriesBookRe  = regexp.MustCompile(`(?i)^(.*?)[,;:]?\s*(?:book|volume|vol\.?|#)\s*(\d+(?:\.\d+)?)\s*$`)
	asinTagRe     = regexp.MustCompile(`^[A-Z0-9]{10}$`)
	isbnTagRe     = regexp.MustCompile(`^(?:\d{13}|\d{9}[\dX])$`)
	digitRunRe    = regexp.MustCompile(`\d+`)
	isbnStripRe   = regexp.MustCompile(`[\s-]`)
	genreSplitter = regexp.MustCompile(`[;/]`)
)

// cleanText removes markup, folds white space and cuts to limit characters (0 = no limit).
func cleanText(s string, limit int) string {
	s = strings.Join(strings.Fields(htmlTagRe.ReplaceAllString(s, " ")), " ")
	if limit > 0 {
		if r := []rune(s); len(r) > limit {
			s = strings.TrimRight(string(r[:limit]), " ")
		}
	}
	return s
}

func cleanASIN(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if asinTagRe.MatchString(s) {
		return s
	}
	return ""
}

func cleanISBN(s string) string {
	s = strings.ToUpper(isbnStripRe.ReplaceAllString(s, ""))
	if isbnTagRe.MatchString(s) {
		return s
	}
	return ""
}

// cleanYear returns the first run of exactly four digits between 1000 and 2999, or 0.
func cleanYear(s string) int {
	for _, run := range digitRunRe.FindAllString(s, -1) {
		if len(run) != 4 {
			continue
		}
		if y, _ := strconv.Atoi(run); y >= 1000 && y <= 2999 {
			return y
		}
	}
	return 0
}

// splitGenres splits on ";" and "/", drops blanks and case-insensitive repeats, and keeps ten.
func splitGenres(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range genreSplitter.Split(s, -1) {
		g = strings.TrimSpace(g)
		k := strings.ToLower(g)
		if g == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, g)
		if len(out) == 10 {
			break
		}
	}
	return out
}

// FromTags reads a book's details from the tags of its first file (keys lower-case, as media.Probe
// gives them). single says the book is one file, whose title tag is then the book's title; in a
// multi-file book it is a track title and only the album names the book. The file's creation time
// is when it was muxed, not when the book came out, so it is never the year.
func FromTags(tags map[string]string, single bool) Details {
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := cleanText(tags[k], 0); v != "" {
				return v
			}
		}
		return ""
	}
	var d Details
	var cm []string
	for _, k := range []string{"comment", "description", "synopsis", "ldes", "desc"} {
		cm = append(cm, tags[k])
	}
	comment := strings.Join(cm, " ")

	d.Title = first("album")
	if d.Title == "" && single {
		d.Title = first("title")
	}
	d.Subtitle = first("subtitle", "tit3")
	d.Authors = ParseNames(first("album_artist", "albumartist", "artist", "author", "writer"))
	narr := first("narrator", "©nrt", "nrt", "reader", "read_by", "performer")
	if narr == "" {
		narr = first("composer")
	}
	d.Narrators = ParseNames(narr)
	if len(d.Narrators) == 0 {
		if m := narratedByRe.FindStringSubmatch(comment); m != nil {
			d.Narrators = ParseNames(cleanText(m[1], 0))
		}
	}
	d.Genres = splitGenres(first("genre"))
	for _, k := range []string{"date", "year", "originaldate", "originalyear", "tdrc", "tyer"} {
		if d.Year = cleanYear(first(k)); d.Year != 0 {
			break
		}
	}
	d.Publisher = first("publisher", "label", "©pub", "organization")
	d.Language = first("language", "lang")
	d.ASIN = cleanASIN(first("asin", "audible_asin", "cdek"))
	d.ISBN = cleanISBN(first("isbn"))
	d.Description = cleanText(first("description", "synopsis", "comment", "desc", "ldes"), descriptionLimit)
	switch strings.TrimSpace(tags["itunesadvisory"]) {
	case "1":
		d.Explicit = boolp(true)
	case "2":
		d.Explicit = boolp(false)
	}
	if name, seq := seriesFromTags(first, first("series_part", "series-part", "series_index", "series_number", "mvin", "episode_id", "part")); name != "" {
		d.Series = []SeriesRef{{Name: name, Sequence: seq}}
	}
	d.fillProduction(tags["album"], comment)
	return d
}

// seriesFromTags finds the series and the position in a file's tags.
func seriesFromTags(first func(...string) string, part string) (series, number string) {
	if sTag := first("series", "series_name", "show", "tvsh", "mvnm"); sTag != "" {
		if strings.Contains(sTag, ";") && strings.Contains(part, ";") {
			names, seqs := splitTrim(sTag), splitTrim(part)
			if len(names) > 1 && len(names) == len(seqs) {
				series, number = names[0], seqs[0]
			}
		}
		if series == "" {
			series, number = sTag, part
		}
	} else {
		for _, piece := range strings.Split(first("grouping", "grp1", "©grp"), ";") {
			if name, seq, ok := ParseSeriesString(piece); ok {
				series, number = name, seq
				if number == "" {
					number = part
				}
				break
			}
		}
	}
	if m := seriesBookRe.FindStringSubmatch(series); m != nil && strings.TrimSpace(m[1]) != "" {
		series = strings.TrimSpace(m[1])
		if number == "" {
			number = m[2]
		}
	}
	return series, number
}

func splitTrim(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ";") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
