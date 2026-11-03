package online

import (
	"context"
	"net/url"
	"strings"
)

// Author is what Audnexus knows about a person who wrote a book.
// The response shape is read from the Audnexus author routes as understood from its open-source
// code; it has not been checked against the live API yet.
type Author struct {
	ASIN        string
	Name        string
	Description string
	ImageURL    string
	Genres      []string
}

// Author looks a person up by ASIN, or by name when the ASIN is empty.
func (c *Client) Author(ctx context.Context, asin, name string) (Author, error) {
	asin = cleanASIN(asin)
	name = cutRunes(strings.TrimSpace(name), maxQueryRunes)
	if asin == "" {
		if name == "" {
			return Author{}, ErrEmptyQuery
		}
		raw, err := c.getJSON(ctx, "audnexus", "https://api.audnex.us/authors?"+params("name", name))
		if err != nil {
			return Author{}, err
		}
		want := norm(name)
		for _, e := range asList(decode(raw)) {
			o := asObj(e)
			if o != nil && norm(o.s("name")) == want {
				asin = cleanASIN(o.s("asin"))
				break
			}
		}
		if asin == "" {
			return Author{}, nil
		}
	}
	raw, err := c.getJSON(ctx, "audnexus", "https://api.audnex.us/authors/"+url.PathEscape(asin))
	if err != nil {
		return Author{}, err
	}
	o := asObj(decode(raw))
	if o == nil {
		return Author{}, nil
	}
	a := Author{ASIN: cleanASIN(o.s("asin")), Name: o.s("name"), Description: o.s("description"), ImageURL: o.s("image")}
	for _, g := range asList(o["genres"]) {
		if gv := asObj(g); gv != nil {
			a.Genres = append(a.Genres, gv.s("name"))
		}
	}
	a.Genres = dedupe(a.Genres)
	return a, nil
}
