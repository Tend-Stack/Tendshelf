package online

// Lookup sites an admin added (decision 0046). They answer the format Audiobookshelf's custom
// providers use: GET {base}/search?mediaType=book&query=<title>&author=<author>, with an optional
// Authorization header, and {"matches":[...]}. The format is read for understanding only.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

const (
	customPrefix       = "custom:"
	maxCustomKeyID     = 40
	maxCustomGenres    = 20
	maxShortFieldRunes = 500
	maxDescriptionRune = 20000
)

// CustomProvider is one lookup site. Auth is the Authorization header value, sent only to the
// site's own search address, and never shown or logged.
type CustomProvider struct{ ID, Name, BaseURL, Auth string }

// Place is somewhere a lookup may ask, as the "Also search" list shows it.
type Place struct{ Key, Label string }

// CustomKey is the lookup key of a provider.
func CustomKey(id string) string { return customPrefix + id }

// IsCustomKey reports whether s is shaped like a custom lookup key: the prefix, then 1-40 of
// letters, digits, underscore and hyphen.
func IsCustomKey(s string) bool {
	id, ok := strings.CutPrefix(s, customPrefix)
	if !ok || len(id) < 1 || len(id) > maxCustomKeyID {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// SetCustom replaces the configured lookup sites, in the order given.
func (c *Client) SetCustom(list []CustomProvider) {
	m := make(map[string]CustomProvider, len(list))
	order := make([]string, 0, len(list))
	for _, p := range list {
		k := CustomKey(p.ID)
		if _, dup := m[k]; dup {
			continue
		}
		m[k] = p
		order = append(order, k)
	}
	c.mu.Lock()
	c.custom, c.customOrder = m, order
	c.mu.Unlock()
}

func (c *Client) customProvider(key string) (CustomProvider, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.custom[key]
	return p, ok
}

func (c *Client) hasCustom(key string) bool {
	_, ok := c.customProvider(key)
	return ok
}

// Places lists the optional lookup places: Open Library, Apple Books, then the added sites.
func (c *Client) Places() []Place {
	out := []Place{{"openlibrary", label("openlibrary")}, {"itunes", label("itunes")}}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range c.customOrder {
		out = append(out, Place{Key: k, Label: c.custom[k].Name})
	}
	return out
}

// label is the name a step shows: a custom site's own name, else the fixed label.
func (c *Client) label(provider string) string {
	if p, ok := c.customProvider(provider); ok {
		return p.Name
	}
	return label(provider)
}

// customOrigin is the scheme, host and port of a configured site, for the cover rule.
func (c *Client) customOrigin(origin string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.custom {
		if u, err := url.Parse(p.BaseURL); err == nil && originOf(u) == origin {
			return true
		}
	}
	return false
}

func (c *Client) customCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.custom)
}

// searchCustom asks one added site. Only mediaType, the title and the author are sent, and only
// when there is a title. Answers are neither cached nor counted.
func (c *Client) searchCustom(ctx context.Context, key string, q Query) ([]Book, error) {
	p, ok := c.customProvider(key)
	if !ok || q.Title == "" {
		return nil, nil
	}
	v := url.Values{"mediaType": {"book"}, "query": {q.Title}}
	if q.Author != "" {
		v.Set("author", q.Author)
	}
	target := strings.TrimSuffix(p.BaseURL, "/") + "/search?" + v.Encode()
	recorderFrom(ctx).request(false)
	status, body, err := c.fetchWith(withPolicy(ctx, target, true), c.chc, target, "application/json", p.Auth, maxJSONBytes)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, withSite(err, p.Name)
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil, &Error{Code: "unauthorized", Site: p.Name}
	case status == http.StatusNotFound:
		return nil, &Error{Code: "bad_answer", Site: p.Name}
	case status == http.StatusTooManyRequests:
		return nil, &Error{Code: "busy", Site: p.Name}
	case status < 200 || status > 299:
		return nil, &Error{Code: "server", Site: p.Name}
	}
	books, err := parseCustomSearch(body, key, p.Name, p.BaseURL)
	if err != nil {
		return nil, &Error{Code: "bad_answer", Site: p.Name}
	}
	return books, nil
}

// withSite names the site in an *Error; any other error becomes an offline one.
func withSite(err error, site string) error {
	e := toError(err, site)
	if e.Site == "" {
		e.Site = site
	}
	return e
}

// TestCustom runs a fixed test search against one site and says how many books came back.
func (c *Client) TestCustom(ctx context.Context, id string) (int, error) {
	books, err := c.searchCustom(ctx, CustomKey(id), Query{Title: "The Hobbit", Author: "J. R. R. Tolkien"})
	return len(books), err
}

// parseCustomSearch reads an answer: an object with a "matches" list. A match without a title is
// skipped. base is the site's address, so a plain-http cover on its own origin is kept.
func parseCustomSearch(raw json.RawMessage, key, label, base string) ([]Book, error) {
	top, ok := decode(raw).(map[string]any)
	if !ok {
		return nil, &Error{Code: "bad_answer"}
	}
	matches, ok := top["matches"].([]any)
	if !ok {
		return nil, &Error{Code: "bad_answer"}
	}
	baseURL, _ := url.Parse(base)
	var out []Book
	for _, mv := range matches {
		m := asObj(mv)
		if m == nil || m.s("title") == "" {
			continue
		}
		b := newBook(key)
		b.ProviderLabel = label
		b.Title = cutRunes(m.s("title"), maxShortFieldRunes)
		b.Subtitle = cutRunes(m.s("subtitle"), maxShortFieldRunes)
		b.Publisher = cutRunes(m.s("publisher"), maxShortFieldRunes)
		b.Description = cutRunes(m.s("description"), maxDescriptionRune)
		for _, n := range commaNames(m.s("author")) {
			b.Authors = append(b.Authors, Person{Name: n})
		}
		b.Narrators = commaNames(m.s("narrator"))
		b.Year = year(m.s("publishedYear"))
		if first := asObj(firstOf(m["series"])); first != nil {
			b.SeriesName = cutRunes(first.s("series"), maxShortFieldRunes)
			b.Sequence = first.s("sequence")
		}
		var genres []string
		for _, g := range asList(m["genres"]) {
			if s, ok := g.(string); ok {
				genres = append(genres, strings.TrimSpace(s))
			}
		}
		if genres = dedupe(genres); len(genres) > maxCustomGenres {
			genres = genres[:maxCustomGenres]
		}
		b.Genres = genres
		b.ISBN = cleanISBN(m.s("isbn"))
		b.ASIN = cleanASIN(m.s("asin"))
		switch {
		case b.ASIN != "":
			b.ID = b.ASIN
		case b.ISBN != "":
			b.ID = b.ISBN
		}
		b.Language = capitalize(m.s("language"))
		b.CoverURL = customCover(m.s("cover"), baseURL)
		out = append(out, b)
	}
	return capBooks(out), nil
}

func firstOf(v any) any {
	if l := asList(v); len(l) > 0 {
		return l[0]
	}
	return nil
}

// commaNames splits "A, B" into trimmed names.
func commaNames(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// customCover keeps an https address, or an http one on the site's own origin.
func customCover(raw string, base *url.URL) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return raw
	case "http":
		if base != nil && originOf(u) == originOf(base) {
			return raw
		}
	}
	return ""
}
