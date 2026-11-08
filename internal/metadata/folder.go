// Port of Audiobookshelf server/utils/scandir.js (getBookDataFromDir and its helpers) and TendVox
// Extractor extractor_details.py (parse_folder_name, parse_book_name, clean_sequence); see NOTICES.md.

package metadata

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	asinRe      = regexp.MustCompile(`(?: |^)\[([A-Z0-9]{10})\]( |$)`)
	narratorRe  = regexp.MustCompile(`^(.*) \{(.*)\}$`)
	sequenceRe  = regexp.MustCompile(`(?i)^(?P<label>vol\.? |volume |book )?(?P<seq>\d{0,3}(?:\.\d{1,2})?)(?P<dot>\.?)(?: (?P<suffix>.*))?$`)
	yearRe      = regexp.MustCompile(`^ *\(?([0-9]{4})\)? * - *(.+)`)
	styleSeries = regexp.MustCompile(`(?i)\s*\((?P<name>[^()]+?)(?:,?\s*(?:book|vol\.?|volume)\s*|\s+#)(?P<seq>\d+(?:\.\d+)?(?:\s*-\s*\d+(?:\.\d+)?)?)\)\s*`)
	styleAbr    = regexp.MustCompile(`(?i)\s*\((un)?abridged\)\s*$`)
	calibreRe   = regexp.MustCompile(`^(?P<author>[^-()]+?) - (?P<series>.+?) (?P<seq>\d{1,3}(?:\.\d)?) - (?P<title>.+)$`)
	byAuthorRe  = regexp.MustCompile(`^(?P<t>.+?) by (?P<a>[A-Z][^()]{2,60})$`)
	rangeSeqRe  = regexp.MustCompile(`^\s*#?\s*(\d+(?:\.\d+)?\s*-\s*\d+(?:\.\d+)?)\s*$`)
	numberSeqRe = regexp.MustCompile(`\.\d+|\d+(?:\.\d+)?`)
	plainNumRe  = regexp.MustCompile(`^\d+(\.\d+)?$`)
)

// formatNumber writes a decimal like Go's %g: "02" becomes "2", "0.50" becomes "0.5".
func formatNumber(s string) string {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// cleanSequence reduces "Book 3", "#3.5" or "1 - 4" to "3", "3.5" or "1-4".
func cleanSequence(text string) string {
	t := strings.TrimSpace(text)
	if m := rangeSeqRe.FindStringSubmatch(t); m != nil {
		return strings.Join(strings.Fields(m[1]), "")
	}
	m := numberSeqRe.FindString(t)
	if m == "" {
		return t
	}
	if plainNumRe.MatchString(m) {
		return formatNumber(m)
	}
	return m
}

// FolderName is what the name of a book's folder (or a single file, without its extension) says.
type FolderName struct {
	Title     string
	ASIN      string
	Narrators []string
	Sequence  string
	Year      int
}

// getSequence finds the series position in a name such as "Book 2 - Title", "Title - Vol 12",
// "Vol. 3 Title Here", "6. Title" or "0.5 - Book Title" and returns the name without it.
func getSequence(name string) (rest, seq string) {
	parts := strings.Split(name, " - ")
	for i, p := range parts {
		m := sequenceRe.FindStringSubmatch(p)
		if m == nil {
			continue
		}
		label, num, dot, suffix := m[1], m[2], m[3], m[4]
		if num == "" || (suffix != "" && label == "" && dot == "") {
			continue // "101 Dalmatians" is a title; "101. Dalmatians" is volume 101
		}
		seq = formatNumber(num)
		if suffix != "" {
			parts[i] = suffix
		} else {
			parts = append(parts[:i], parts[i+1:]...)
		}
		break
	}
	return strings.Join(parts, " - "), seq
}

// ParseFolderName strips, in the order Audiobookshelf does, the [ASIN], the {narrators}, the series
// position (only when hasSeriesDir) and the publication year from a name.
func ParseFolderName(name string, hasSeriesDir bool) FolderName {
	var out FolderName
	rest := strings.TrimSpace(name)
	if m := asinRe.FindStringSubmatchIndex(rest); m != nil {
		out.ASIN = rest[m[2]:m[3]]
		rest = strings.TrimSpace(rest[:m[0]] + rest[m[3]+1:])
	}
	if m := narratorRe.FindStringSubmatch(rest); m != nil {
		rest = m[1]
		out.Narrators = ParseNames(m[2])
	}
	if hasSeriesDir {
		rest, out.Sequence = getSequence(rest)
	}
	if m := yearRe.FindStringSubmatch(rest); m != nil {
		out.Year, _ = strconv.Atoi(m[1])
		rest = m[2]
	}
	out.Title = strings.TrimSpace(rest)
	return out
}

// BookName is what the words of a title say: "Title (Series #1) (Unabridged)", "Title by Author" or
// "Author - Series 02 - Title".
type BookName struct {
	Title, Author, Series, Sequence string
	Abridged                        *bool
}

// ParseBookName reads a title for a series, an author and the (Un)abridged mark.
func ParseBookName(name string) BookName { return parseBookName(name, false) }

// parseBookName is ParseBookName; haveAuthor stops "Title by Author" from cutting a title when the
// folders already name the author ("Surrounded by Idiots").
func parseBookName(name string, haveAuthor bool) BookName {
	var out BookName
	rest := strings.TrimSpace(name)
	if m := styleAbr.FindStringSubmatchIndex(rest); m != nil {
		out.Abridged = boolp(m[2] < 0)
		rest = strings.TrimSpace(rest[:m[0]])
	}
	if m := styleSeries.FindStringSubmatchIndex(rest); m != nil {
		out.Series = strings.TrimSpace(rest[m[styleSeries.SubexpIndex("name")*2]:m[styleSeries.SubexpIndex("name")*2+1]])
		seq := rest[m[styleSeries.SubexpIndex("seq")*2]:m[styleSeries.SubexpIndex("seq")*2+1]]
		out.Sequence = cleanSequence(seq)
		rest = strings.TrimSpace(rest[:m[0]] + " " + rest[m[1]:])
	}
	if m := calibreRe.FindStringSubmatch(rest); m != nil && out.Series == "" {
		out.Author = strings.Join(ParseNames(m[calibreRe.SubexpIndex("author")]), ", ")
		out.Series = strings.TrimSpace(m[calibreRe.SubexpIndex("series")])
		out.Sequence = cleanSequence(m[calibreRe.SubexpIndex("seq")])
		rest = strings.TrimSpace(m[calibreRe.SubexpIndex("title")])
	} else if m := byAuthorRe.FindStringSubmatch(rest); m != nil && !haveAuthor {
		out.Author = strings.Join(ParseNames(m[byAuthorRe.SubexpIndex("a")]), ", ")
		rest = strings.TrimSpace(m[byAuthorRe.SubexpIndex("t")])
	}
	out.Title = rest
	return out
}

// UnknownAuthorFolder is the folder name an upload with no author is placed under (internal/upload). It is a
// placeholder, so FromPath reads it as no author. It lives here because upload imports metadata.
const UnknownAuthorFolder = "Unknown author"

// FromPath reads a book's details from where it lies under its library folder: the directories
// above it name the author and the series, and its own name carries the rest. For a single-file
// book (single true) the name is the file name without its extension.
func FromPath(folderRoot, bookRoot string, single bool) Details {
	rel, err := filepath.Rel(folderRoot, bookRoot)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		rel = filepath.Base(bookRoot)
	}
	if rel == "." {
		rel = filepath.Base(folderRoot)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	name := parts[len(parts)-1]
	above := parts[:len(parts)-1]
	if single {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}

	var series, author string
	if n := len(above); n >= 2 {
		series, author = above[n-1], above[n-2]
	} else if n == 1 {
		author = above[0]
	}
	if author == UnknownAuthorFolder {
		// The upload layout's stand-in for "no author" is not a person.
		author = ""
	}

	var d Details
	fn := ParseFolderName(name, series != "")
	bn := parseBookName(fn.Title, author != "")
	d.Title = bn.Title
	if d.Title == "" {
		d.Title = name
	}
	d.ASIN = fn.ASIN
	d.Narrators = fn.Narrators
	d.Year = fn.Year
	d.Abridged = bn.Abridged
	switch {
	case series != "":
		d.Series = []SeriesRef{{Name: series, Sequence: fn.Sequence}}
	case bn.Series != "":
		d.Series = []SeriesRef{{Name: bn.Series, Sequence: bn.Sequence}}
	}
	switch {
	case author != "":
		d.Authors = ParseNames(author)
	case bn.Author != "":
		d.Authors = ParseNames(bn.Author)
	}
	return d
}
