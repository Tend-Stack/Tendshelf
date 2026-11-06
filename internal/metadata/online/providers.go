// Ports the Extractor extractor_online.py (parse_audnexus_book, parse_audible_products, parse_openlibrary_search, parse_openlibrary_work, parse_itunes); see NOTICES.md.

package online

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const maxAudibleProducts = 5

var (
	asinRE   = regexp.MustCompile(`^[A-Z0-9]{10}$`)
	isbnRE   = regexp.MustCompile(`^(\d{9}[\dX]|\d{13})$`)
	artSizeR = regexp.MustCompile(`/\d+x\d+bb`)
)

// obj is a decoded JSON object; every accessor tolerates a missing or odd field.
type obj map[string]any

func decode(raw json.RawMessage) any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

func asObj(v any) obj {
	o, _ := v.(map[string]any)
	return obj(o)
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// str returns strings and numbers as text, and "" for everything else.
func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

func (o obj) s(k string) string { return str(o[k]) }

func cleanASIN(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if asinRE.MatchString(s) {
		return s
	}
	return ""
}

func cleanISBN(s string) string {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
	if isbnRE.MatchString(s) {
		return s
	}
	return ""
}

// year reads a four-digit year from the start of a date such as "2023-05-09T00:00:00Z".
func year(s string) int {
	if len(s) > 10 {
		s = s[:10]
	}
	if len(s) < 4 {
		return 0
	}
	n, err := strconv.Atoi(s[:4])
	if err != nil || n < 1000 || n > 2999 {
		return 0
	}
	return n
}

// ratingFields keeps a provider's own rating: ("4.5", "1234"). A rating outside 0-5 is dropped.
func ratingFields(avg, count any) (string, string) {
	var f float64
	switch x := avg.(type) {
	case float64:
		f = x
	case string:
		v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), ",", "."), 64)
		if err != nil {
			return "", ""
		}
		f = v
	default:
		return "", ""
	}
	if !(f > 0 && f <= 5) {
		return "", ""
	}
	rating := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", f), "0"), ".")
	n := 0
	switch x := count.(type) {
	case float64:
		n = int(x)
	case string:
		v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), ",", ""), 64)
		if err == nil {
			n = int(v)
		}
	}
	if n > 0 {
		return rating, strconv.Itoa(n)
	}
	return rating, ""
}

func people(v any) []Person {
	var out []Person
	for _, x := range asList(v) {
		o := asObj(x)
		if o == nil {
			continue
		}
		if n := o.s("name"); n != "" {
			out = append(out, Person{Name: n, ASIN: cleanASIN(o.s("asin"))})
		}
	}
	return out
}

func plainNames(v any) []string {
	var out []string
	for _, p := range people(v) {
		out = append(out, p.Name)
	}
	return out
}

func newBook(provider string) Book {
	return Book{Provider: provider, ProviderLabel: label(provider)}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// parseAudnexusBook reads Audnexus /books/{asin}. It returns nil without an ASIN or a title.
func parseAudnexusBook(raw json.RawMessage) *Book {
	item := asObj(decode(raw))
	if item == nil || item.s("asin") == "" || item.s("title") == "" {
		return nil
	}
	b := newBook("audnexus")
	b.ID = item.s("asin")
	b.ASIN = cleanASIN(item.s("asin"))
	b.Title = item.s("title")
	b.Subtitle = item.s("subtitle")
	b.Authors = people(item["authors"])
	b.Narrators = plainNames(item["narrators"])
	b.Publisher = item.s("publisherName")
	b.Year = year(item.s("releaseDate"))
	b.Description = item.s("summary")
	b.CoverURL = item.s("image")
	b.ISBN = cleanISBN(item.s("isbn"))
	var genres []string
	for _, g := range asList(item["genres"]) {
		if o := asObj(g); o != nil && o.s("type") == "genre" {
			genres = append(genres, o.s("name"))
		}
	}
	b.Genres = dedupe(genres)
	if sp := asObj(item["seriesPrimary"]); sp != nil {
		b.SeriesName = sp.s("name")
		b.Sequence = sp.s("position")
	}
	b.Language = capitalize(item.s("language"))
	b.Format = strings.ToLower(item.s("formatType"))
	b.Rating, b.RatingCount = ratingFields(item["rating"], nil)
	return &b
}

// parseAudibleProducts returns the first valid ASINs from the Audible catalogue search.
func parseAudibleProducts(raw json.RawMessage) []string {
	var out []string
	for _, p := range asList(asObj(decode(raw))["products"]) {
		a := cleanASIN(asObj(p).s("asin"))
		if a != "" && !contains(out, a) {
			out = append(out, a)
		}
		if len(out) == maxAudibleProducts {
			break
		}
	}
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func parseOpenLibrarySearch(raw json.RawMessage) []Book {
	var out []Book
	for _, dv := range asList(asObj(decode(raw))["docs"]) {
		d := asObj(dv)
		if d == nil {
			continue
		}
		b := newBook("openlibrary")
		b.ID = d.s("key")
		b.Title = d.s("title")
		b.Subtitle = d.s("subtitle")
		for _, n := range asList(d["author_name"]) {
			if s := str(n); s != "" {
				b.Authors = append(b.Authors, Person{Name: s})
			}
		}
		if pubs := asList(d["publisher"]); len(pubs) > 0 {
			b.Publisher = str(pubs[0])
		}
		b.Year = year(d.s("first_publish_year"))
		for _, x := range asList(d["isbn"]) {
			if v := cleanISBN(str(x)); v != "" {
				b.ISBN = v
				break
			}
		}
		if langs := asList(d["language"]); len(langs) > 0 {
			b.Language = str(langs[0])
		}
		if key := d.s("cover_edition_key"); key != "" {
			b.CoverURL = "https://covers.openlibrary.org/b/olid/" + key + "-M.jpg"
		}
		for i, s := range asList(d["subject"]) {
			if i == 4 {
				break
			}
			b.Genres = append(b.Genres, str(s))
		}
		b.Genres = dedupe(b.Genres)
		b.Rating, b.RatingCount = ratingFields(d["ratings_average"], d["ratings_count"])
		out = append(out, b)
	}
	return out
}

// parseOpenLibraryWork reads a work's description, which is a string or {"value": ...}.
func parseOpenLibraryWork(raw json.RawMessage) string {
	d := asObj(decode(raw))["description"]
	if o := asObj(d); o != nil {
		d = o["value"]
	}
	s, _ := d.(string)
	return strings.TrimSpace(s)
}

func parseITunes(raw json.RawMessage) []Book {
	var out []Book
	for _, rv := range asList(asObj(decode(raw))["results"]) {
		r := asObj(rv)
		if r == nil || r.s("collectionName") == "" {
			continue
		}
		art := r.s("artworkUrl100")
		if art == "" {
			art = r.s("artworkUrl60")
		}
		if art != "" {
			art = artSizeR.ReplaceAllString(art, "/600x600bb")
		}
		b := newBook("itunes")
		b.ID = r.s("collectionId")
		b.Title = r.s("collectionName")
		for _, part := range strings.Split(strings.ReplaceAll(r.s("artistName"), " & ", ","), ",") {
			if p := strings.TrimSpace(part); p != "" {
				b.Authors = append(b.Authors, Person{Name: p})
			}
		}
		b.Year = year(r.s("releaseDate"))
		if g := r.s("primaryGenreName"); g != "" {
			b.Genres = []string{g}
		}
		b.Description = r.s("description")
		b.CoverURL = art
		b.Rating, b.RatingCount = ratingFields(r["averageUserRating"], r["userRatingCount"])
		out = append(out, b)
	}
	return out
}
