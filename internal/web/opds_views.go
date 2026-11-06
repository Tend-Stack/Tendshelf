package web

import (
	"encoding/xml"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// OPDS 1.2 as Atom, written with encoding/xml so every value is escaped. Only the parts reading apps
// use are here: navigation and acquisition feeds, acquisition and image links, paging, OpenSearch.

const (
	opdsNS        = "http://opds-spec.org/2010/catalog"
	atomNS        = "http://www.w3.org/2005/Atom"
	openSearchNS  = "http://a9.com/-/spec/opensearch/1.1/"
	opdsNav       = "application/atom+xml;profile=opds-catalog;kind=navigation;charset=utf-8"
	opdsAcq       = "application/atom+xml;profile=opds-catalog;kind=acquisition;charset=utf-8"
	opdsPageSize  = 50
	relAcquire    = "http://opds-spec.org/acquisition"
	relImage      = "http://opds-spec.org/image"
	relThumbnail  = "http://opds-spec.org/image/thumbnail"
	opdsThumbWide = 200
)

type atomLink struct {
	Rel   string `xml:"rel,attr,omitempty"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr,omitempty"`
	Title string `xml:"title,attr,omitempty"`
}

type atomText struct {
	Type string `xml:"type,attr,omitempty"`
	Text string `xml:",chardata"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomEntry struct {
	ID      string       `xml:"id"`
	Title   string       `xml:"title"`
	Updated string       `xml:"updated"`
	Authors []atomAuthor `xml:"author"`
	Summary *atomText    `xml:"summary,omitempty"`
	Content *atomText    `xml:"content,omitempty"`
	Links   []atomLink   `xml:"link"`
}

type atomFeed struct {
	XMLName    xml.Name    `xml:"feed"`
	NS         string      `xml:"xmlns,attr"`
	OpenSearch string      `xml:"xmlns:opensearch,attr,omitempty"`
	ID         string      `xml:"id"`
	Title      string      `xml:"title"`
	Updated    string      `xml:"updated"`
	Author     atomAuthor  `xml:"author"`
	Links      []atomLink  `xml:"link"`
	Total      string      `xml:"opensearch:totalResults,omitempty"`
	PerPage    string      `xml:"opensearch:itemsPerPage,omitempty"`
	Start      string      `xml:"opensearch:startIndex,omitempty"`
	Entries    []atomEntry `xml:"entry"`
}

type openSearchURL struct {
	Type     string `xml:"type,attr"`
	Template string `xml:"template,attr"`
}

type openSearchDescription struct {
	XMLName     xml.Name      `xml:"OpenSearchDescription"`
	NS          string        `xml:"xmlns,attr"`
	ShortName   string        `xml:"ShortName"`
	Description string        `xml:"Description"`
	URL         openSearchURL `xml:"Url"`
}

func atomTime(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }

// opdsFeed starts a feed with the links every feed carries.
func opdsFeed(id, title, self, selfType string, now time.Time) atomFeed {
	return atomFeed{
		NS: atomNS, ID: id, Title: title, Updated: now.UTC().Format(time.RFC3339), Author: atomAuthor{Name: "Tendshelf"},
		Links: []atomLink{
			{Rel: "self", Href: self, Type: selfType},
			{Rel: "start", Href: "/opds", Type: opdsNav},
			{Rel: "search", Href: "/opds/search.xml", Type: "application/opensearchdescription+xml", Title: "Search"},
		},
	}
}

// opdsNavFeed is the front page: the two lists and the search link.
func opdsNavFeed(now time.Time) atomFeed {
	f := opdsFeed("urn:tendshelf:opds:root", "Tendshelf", "/opds", opdsNav, now)
	stamp := now.UTC().Format(time.RFC3339)
	f.Entries = []atomEntry{
		{ID: "urn:tendshelf:opds:all", Title: "All books", Updated: stamp, Content: &atomText{Type: "text", Text: "Every book you can take as an ebook, by title."},
			Links: []atomLink{{Rel: "subsection", Href: "/opds/all", Type: opdsAcq}}},
		{ID: "urn:tendshelf:opds:new", Title: "Newest", Updated: stamp, Content: &atomText{Type: "text", Text: "The books added most recently."},
			Links: []atomLink{{Rel: "http://opds-spec.org/sort/new", Href: "/opds/new", Type: opdsAcq}}},
	}
	return f
}

// opdsBooksFeed is one page of an acquisition feed. base is the feed's own path and query without page.
func opdsBooksFeed(id, title, base string, page, total int, books []library.CatalogueBook, now time.Time) atomFeed {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	at := func(p int) string {
		if p <= 1 {
			return base
		}
		return base + sep + "page=" + strconv.Itoa(p)
	}
	f := opdsFeed(id, title, at(page), opdsAcq, now)
	f.OpenSearch = openSearchNS
	f.Total, f.PerPage, f.Start = strconv.Itoa(total), strconv.Itoa(opdsPageSize), strconv.Itoa((page-1)*opdsPageSize+1)
	last := max(1, (total+opdsPageSize-1)/opdsPageSize)
	f.Links = append(f.Links, atomLink{Rel: "first", Href: at(1), Type: opdsAcq})
	if page > 1 {
		f.Links = append(f.Links, atomLink{Rel: "previous", Href: at(min(page-1, last)), Type: opdsAcq})
	}
	if page < last {
		f.Links = append(f.Links, atomLink{Rel: "next", Href: at(page + 1), Type: opdsAcq})
	}
	for _, b := range books {
		f.Entries = append(f.Entries, opdsEntry(b))
	}
	return f
}

func opdsEntry(b library.CatalogueBook) atomEntry {
	e := atomEntry{ID: "urn:tendshelf:book:" + b.ID, Title: b.Title, Updated: atomTime(max(b.UpdatedAt, b.AddedAt))}
	if b.Subtitle != "" {
		e.Title = b.Title + ": " + b.Subtitle
	}
	for _, a := range b.Authors {
		e.Authors = append(e.Authors, atomAuthor{Name: a})
	}
	if b.Description != "" {
		e.Summary = &atomText{Type: "text", Text: b.Description}
	}
	for _, f := range b.Files {
		e.Links = append(e.Links, atomLink{Rel: relAcquire, Href: "/opds/books/" + b.ID + "/file/" + f.ID, Type: ebookMime(f.Ext), Title: strings.ToUpper(strings.TrimPrefix(f.Ext, "."))})
	}
	if b.CoverID != "" {
		e.Links = append(e.Links,
			atomLink{Rel: relImage, Href: "/opds/covers/" + b.ID},
			atomLink{Rel: relThumbnail, Href: "/opds/covers/" + b.ID + "?w=" + strconv.Itoa(opdsThumbWide)})
	}
	return e
}
