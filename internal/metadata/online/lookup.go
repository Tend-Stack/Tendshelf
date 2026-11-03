// Ports the Extractor extractor_online.py (clean_query, search_provider, lookup, REGIONS, LANG_REGION); see NOTICES.md.

package online

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
)

// Providers are the lookup sources, in the order a "search everywhere" run asks them.
var Providers = []string{"audnexus", "google", "openlibrary", "itunes"}

// audnexusRegions are the Audnexus stores; "" is the default (US).
var audnexusRegions = []string{"", "fr", "de", "uk", "ca", "au", "it", "es", "in", "jp"}

var langRegion = map[string]string{
	"french": "fr", "fra": "fr", "fre": "fr", "fr": "fr",
	"german": "de", "deu": "de", "ger": "de", "de": "de",
	"italian": "it", "ita": "it", "it": "it",
	"spanish": "es", "spa": "es", "es": "es",
	"japanese": "jp", "jpn": "jp", "ja": "jp",
}

const (
	maxCandidates      = 10
	maxOpenLibWorks    = 3
	maxQueryRunes      = 200
	openLibraryFields  = "key,title,subtitle,author_name,first_publish_year,isbn,publisher,language,subject,cover_edition_key,ratings_average,ratings_count"
	perProviderResults = 5
)

// ErrEmptyQuery is returned when there is nothing to look up.
var ErrEmptyQuery = errors.New("online: nothing to look up")

// Query is what a lookup may send out. Language only orders the Audnexus stores and is never sent.
type Query struct{ Title, Author, ASIN, ISBN, Language string }

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (q Query) clean() Query {
	return Query{
		Title:    cutRunes(strings.TrimSpace(q.Title), maxQueryRunes),
		Author:   cutRunes(strings.TrimSpace(q.Author), maxQueryRunes),
		ASIN:     cleanASIN(q.ASIN),
		ISBN:     cleanISBN(q.ISBN),
		Language: strings.ToLower(cutRunes(strings.TrimSpace(q.Language), 20)),
	}
}

// Result is the answer to a lookup.
type Result struct {
	Candidates []Book
	Errors     map[string]string // provider -> error code
	Searched   []string
	Steps      []Step // the log of the lookup, in the order it ran
}

// quote percent-encodes everything except unreserved characters and the bytes in safe.
func quote(s, safe string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-._~", c) >= 0 || strings.IndexByte(safe, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

// params builds a query string from key, value pairs in order, leaving out empty values.
func params(kv ...string) string {
	var parts []string
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			parts = append(parts, quote(kv[i], "")+"="+quote(kv[i+1], ""))
		}
	}
	return strings.Join(parts, "&")
}

// Lookup asks the named providers (unknown names are ignored) and returns the best matches first.
func (c *Client) Lookup(ctx context.Context, q Query, providers []string) (Result, error) {
	return c.LookupSteps(ctx, q, providers, nil)
}

// LookupSteps is Lookup that also tells onStep the log so far, a copy each time a step starts or
// ends. The finished log is in Result.Steps.
func (c *Client) LookupSteps(ctx context.Context, q Query, providers []string, onStep func([]Step)) (Result, error) {
	return c.LookupWith(ctx, q, providers, LookupOptions{OnStep: onStep})
}

// LookupOptions tunes one lookup. MaxAudnexusBooks limits how many Audible matches get a detail
// request each (0 means all, at most ten); OnStep is told the log as in LookupSteps.
type LookupOptions struct {
	MaxAudnexusBooks int
	OnStep           func([]Step)
}

type maxBooksKey struct{}

// SentBy is how many requests were really sent (not answered from the saved copy), by provider.
func (r Result) SentBy() map[string]int {
	out := map[string]int{}
	for _, s := range r.Steps {
		if s.Sent > 0 {
			out[s.Provider] += s.Sent
		}
	}
	return out
}

// LookupWith is LookupSteps with options.
func (c *Client) LookupWith(ctx context.Context, q Query, providers []string, o LookupOptions) (Result, error) {
	onStep := o.OnStep
	if o.MaxAudnexusBooks > 0 {
		ctx = context.WithValue(ctx, maxBooksKey{}, o.MaxAudnexusBooks)
	}
	q = q.clean()
	if q.Title == "" && q.ASIN == "" && q.ISBN == "" {
		return Result{}, ErrEmptyQuery
	}
	res := Result{Errors: map[string]string{}}
	var planned []string
	for _, p := range providers {
		if contains(Providers, p) && !contains(planned, p) {
			planned = append(planned, p)
		}
	}
	rec := &recorder{emit: onStep, cur: -1}
	rec.plan(planned)
	ctx = context.WithValue(ctx, recorderKey{}, rec)
	for _, p := range planned {
		res.Searched = append(res.Searched, p)
		found, err := c.searchProvider(ctx, p, q)
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			code := "server"
			var oe *Error
			if errors.As(err, &oe) {
				code = oe.Code
			}
			res.Errors[p] = code
			continue
		}
		for i := range found {
			b := &found[i]
			if (q.ASIN != "" && b.ASIN == q.ASIN) || (q.ISBN != "" && b.ISBN == q.ISBN) {
				b.Score, b.Exact = 1, true
			} else {
				b.Score = Score(*b, q.Title, q.Author)
			}
		}
		res.Candidates = append(res.Candidates, found...)
	}
	res.Steps = rec.snapshot()
	sort.SliceStable(res.Candidates, func(i, j int) bool { return res.Candidates[i].Score > res.Candidates[j].Score })
	if len(res.Candidates) > maxCandidates {
		res.Candidates = res.Candidates[:maxCandidates]
	}
	return res, nil
}

// searchProvider asks one provider and records it as a step. Audnexus records its own steps.
func (c *Client) searchProvider(ctx context.Context, provider string, q Query) ([]Book, error) {
	if provider == "audnexus" {
		return c.searchAudnexus(ctx, q)
	}
	rec := recorderFrom(ctx)
	rec.begin(provider, label(provider), "", q.askFor(provider))
	books, err := c.searchOther(ctx, provider, q)
	rec.end(len(books), err)
	return books, err
}

func (c *Client) searchOther(ctx context.Context, provider string, q Query) ([]Book, error) {
	switch provider {
	case "google":
		var query string
		switch {
		case q.ISBN != "":
			query = "isbn:" + q.ISBN
		case q.Title != "":
			query = "intitle:" + q.Title
			if q.Author != "" {
				query += "+inauthor:" + q.Author
			}
		default:
			return nil, nil
		}
		raw, err := c.getJSON(ctx, provider, "https://www.googleapis.com/books/v1/volumes?q="+quote(query, ":+")+"&maxResults=5")
		if err != nil {
			return nil, err
		}
		return capBooks(parseGoogleBooks(raw)), nil
	case "openlibrary":
		if q.Title == "" && q.ISBN == "" {
			return nil, nil
		}
		search := params("title", q.Title, "author", q.Author)
		if q.ISBN != "" {
			search = params("isbn", q.ISBN)
		}
		raw, err := c.getJSON(ctx, provider, "https://openlibrary.org/search.json?"+search+"&limit=5&fields="+openLibraryFields)
		if err != nil {
			return nil, err
		}
		out := capBooks(parseOpenLibrarySearch(raw))
		for i := range out {
			if i >= maxOpenLibWorks {
				break
			}
			if !strings.HasPrefix(out[i].ID, "/works/") || strings.ContainsAny(out[i].ID[len("/works/"):], "/?#% ") {
				continue
			}
			work, err := c.getJSON(ctx, provider, "https://openlibrary.org"+out[i].ID+".json")
			if err != nil {
				return nil, err
			}
			out[i].Description = parseOpenLibraryWork(work)
		}
		return out, nil
	case "itunes":
		if q.Title == "" {
			return nil, nil
		}
		term := strings.TrimSpace(q.Title + " " + q.Author)
		raw, err := c.getJSON(ctx, provider, "https://itunes.apple.com/search?"+params("term", term, "media", "audiobook", "entity", "audiobook", "limit", "5"))
		if err != nil {
			return nil, err
		}
		return parseITunes(raw), nil
	}
	return nil, nil
}

func capBooks(b []Book) []Book {
	if len(b) > perProviderResults {
		return b[:perProviderResults]
	}
	return b
}

func (c *Client) searchAudnexus(ctx context.Context, q Query) ([]Book, error) {
	rec := recorderFrom(ctx)
	asins := []string{}
	if q.ASIN != "" {
		asins = append(asins, q.ASIN)
	} else if q.Title != "" {
		rec.begin("audnexus", "Audible catalogue", "", q.titleAsk())
		raw, err := c.getJSON(ctx, "audnexus", "https://api.audible.com/1.0/catalog/products?"+
			params("num_results", "10", "products_sort_by", "Relevance", "title", q.Title, "author", q.Author))
		if err != nil {
			rec.end(0, err)
			return nil, err
		}
		asins = parseAudibleProducts(raw)
		if n, _ := ctx.Value(maxBooksKey{}).(int); n > 0 && len(asins) > n {
			asins = asins[:n]
		}
		rec.end(len(asins), nil)
	}
	order := append([]string(nil), audnexusRegions...)
	if first, ok := langRegion[q.Language]; ok {
		order = append([]string{first}, removeString(order, first)...)
	}
	// An ASIN from another country's store answers 404 in the default one, so with a given ASIN
	// every store is tried in turn; otherwise only the first.
	regions := order[:1]
	if q.ASIN != "" {
		regions = order
	}
	var out []Book
	for _, asin := range asins {
		for _, region := range regions {
			u := "https://api.audnex.us/books/" + url.PathEscape(asin)
			if region != "" {
				u += "?region=" + region
			}
			rec.begin("audnexus", "Audnexus", regionLabel(region), asin)
			raw, err := c.getJSON(ctx, "audnexus", u)
			if err != nil {
				rec.end(0, err)
				return nil, err
			}
			if b := parseAudnexusBook(json.RawMessage(raw)); b != nil {
				rec.end(1, nil)
				out = append(out, *b)
				break
			}
			rec.end(0, nil)
		}
	}
	return out, nil
}

func removeString(l []string, s string) []string {
	var out []string
	for _, x := range l {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
