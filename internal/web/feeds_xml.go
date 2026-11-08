package web

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// The feed document of a podcast feed (decision 0031): RSS 2.0 with the iTunes tags podcast apps read and the
// Podcasting 2.0 chapters link. Colon tags are literal: encoding/xml writes the name as given.

const (
	feedNSItunes  = "http://www.itunes.com/dtds/podcast-1.0.dtd"
	feedNSPodcast = "https://podcastindex.org/namespace/1.0"
	feedLead      = 7 * 24 * time.Hour // episodes are dated this long before the feed was made
	feedDescMax   = 4000
)

type feedRSS struct {
	XMLName   xml.Name    `xml:"rss"`
	Version   string      `xml:"version,attr"`
	NSItunes  string      `xml:"xmlns:itunes,attr"`
	NSPodcast string      `xml:"xmlns:podcast,attr"`
	Channel   feedChannel `xml:"channel"`
}

type feedChannel struct {
	Title       string     `xml:"title"`
	Link        string     `xml:"link"`
	Description string     `xml:"description"`
	Language    string     `xml:"language,omitempty"`
	Author      string     `xml:"itunes:author,omitempty"`
	Image       *feedImage `xml:"itunes:image"`
	Block       string     `xml:"itunes:block"`
	Explicit    string     `xml:"itunes:explicit"`
	Type        string     `xml:"itunes:type"`
	Items       []feedItem `xml:"item"`
}

type feedImage struct {
	Href string `xml:"href,attr"`
}

type feedItem struct {
	Title       string        `xml:"title"`
	GUID        feedGUID      `xml:"guid"`
	PubDate     string        `xml:"pubDate"`
	Enclosure   feedEnclosure `xml:"enclosure"`
	Duration    string        `xml:"itunes:duration,omitempty"`
	Episode     int           `xml:"itunes:episode"`
	Image       *feedImage    `xml:"itunes:image"`
	Explicit    string        `xml:"itunes:explicit"`
	Description string        `xml:"description"`
	Chapters    *feedChapters `xml:"podcast:chapters"`
}

type feedGUID struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type feedEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type feedChapters struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

// writeFeedXML sends the document. A HEAD request gets the headers only.
func writeFeedXML(w http.ResponseWriter, r *http.Request, v any) {
	body, err := xml.MarshalIndent(v, "", " ")
	if err != nil {
		http.Error(w, "Something went wrong on our side.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
}

func feedBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// feedClock writes a duration as HH:MM:SS, whole seconds.
func feedClock(ms int64) string {
	sec := ms / 1000
	return fmt.Sprintf("%02d:%02d:%02d", sec/3600, sec/60%60, sec%60)
}

func feedCut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// feedExt is the extension an audio URL carries: that of the file's path, else its container, else "audio".
func feedExt(f library.AudioFile) string {
	ext := ""
	if i := strings.LastIndexByte(f.Path, '.'); i >= 0 {
		ext = f.Path[i+1:]
	}
	if ext == "" {
		ext = f.Container
	}
	ext = strings.ToLower(ext)
	for _, c := range ext {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return "audio"
		}
	}
	if ext == "" {
		return "audio"
	}
	return ext
}

// feedCoverURL is the cover address of a book, or "" when it has none. The suffix follows the stored type.
func (s *server) feedCoverURL(ctx context.Context, root string, b library.Book) string {
	if b.CoverID == "" {
		return ""
	}
	c, err := s.lib.Cover(ctx, b.CoverID)
	if err != nil {
		return ""
	}
	ext := "jpg"
	switch c.Mime {
	case "image/png":
		ext = "png"
	case "image/webp":
		ext = "webp"
	}
	return root + "cover/" + b.ID + "." + ext
}

// feedEpisodeTitles names the episodes of a multi-file book: the chapter that starts the file, else "Part n of N".
func feedEpisodeTitles(chapters []library.Chapter, files []library.AudioFile) []string {
	out := make([]string, len(files))
	used := map[string]bool{}
	for i, f := range files {
		title := ""
		for _, c := range chapters {
			if c.StartMs <= f.OffsetMs+1000 && c.EndMs > f.OffsetMs {
				title = strings.TrimSpace(c.Title)
				break
			}
		}
		if title == "" || used[title] {
			title = fmt.Sprintf("Part %d of %d", i+1, len(files))
		}
		used[title] = true
		out[i] = title
	}
	return out
}

// feedRoot is a feed's own address up to and including the slash before its files; the feed document, the shown
// address and every URL inside the document start from it.
func feedRoot(base, token string) string { return base + feedPrefix + token + "/" }

// buildFeed makes the document for the feed's books. Every URL starts with the feed's own address, token included,
// because a podcast app needs absolute addresses.
func (s *server) buildFeed(ctx context.Context, fr feedReq, books []library.Book) (feedRSS, error) {
	root := feedRoot(fr.base, fr.token)
	series := fr.feed.SeriesID != ""
	ch := feedChannel{Link: fr.base + "/", Block: "yes", Type: "serial"}
	created := time.UnixMilli(fr.feed.CreatedAt).UTC().Add(-feedLead)
	nFiles := 0
	for bi, book := range books {
		files, err := s.lib.AudioFiles(ctx, book.ID)
		if err != nil {
			return feedRSS{}, err
		}
		files = feedFiles(book, files)
		if len(files) == 0 {
			continue
		}
		chapters, err := s.lib.Chapters(ctx, book.ID)
		if err != nil {
			return feedRSS{}, err
		}
		people, err := s.lib.BookPeople(ctx, book.ID)
		if err != nil {
			return feedRSS{}, err
		}
		var authors []string
		for _, p := range people {
			if p.Role == library.RoleAuthor {
				authors = append(authors, p.Name)
			}
		}
		cover := s.feedCoverURL(ctx, root, book)
		if bi == 0 {
			ch.Author = strings.Join(authors, ", ")
			if cover != "" {
				ch.Image = &feedImage{Href: cover}
			}
		}
		titles := feedEpisodeTitles(chapters, files)
		for fi, f := range files {
			if nFiles >= feedMaxFiles {
				break
			}
			nFiles++
			title := book.Title
			if len(files) > 1 {
				title = titles[fi]
				if series {
					title = book.Title + " · " + title
				}
			}
			it := feedItem{
				Title:       title,
				GUID:        feedGUID{IsPermaLink: "false", Value: "urn:tendshelf:audio:" + f.ID},
				PubDate:     created.Add(time.Duration(bi*1000+fi) * time.Second).Format(time.RFC1123Z),
				Enclosure:   feedEnclosure{URL: root + "audio/" + f.ID + "." + feedExt(f), Length: f.Size, Type: audioContentType(f.Container, f.Path)},
				Episode:     nFiles,
				Explicit:    feedBool(book.Explicit),
				Description: feedCut(book.Description, feedDescMax),
			}
			if f.DurationMs > 0 {
				it.Duration = feedClock(f.DurationMs)
			}
			if series && cover != "" {
				it.Image = &feedImage{Href: cover}
			}
			if len(feedFileChapters(chapters, f)) >= 2 {
				it.Chapters = &feedChapters{URL: root + "chapters/" + f.ID + ".json", Type: "application/json+chapters"}
			}
			ch.Items = append(ch.Items, it)
		}
	}
	if len(ch.Items) == 0 {
		return feedRSS{}, errFeedGone
	}
	first := books[0]
	if series {
		var name, desc string
		err := s.lib.Store().Reader().QueryRowContext(ctx, `SELECT name, description FROM series WHERE id = ?`, fr.feed.SeriesID).Scan(&name, &desc)
		if err != nil {
			return feedRSS{}, errFeedGone
		}
		ch.Title, ch.Description = name, desc
	} else {
		ch.Title, ch.Description, ch.Language = first.Title, first.Description, first.Language
	}
	if strings.TrimSpace(ch.Description) == "" {
		ch.Description = ch.Title
	}
	ch.Explicit = feedBool(first.Explicit)
	for _, b := range books {
		if b.Explicit {
			ch.Explicit = "true"
		}
	}
	return feedRSS{Version: "2.0", NSItunes: feedNSItunes, NSPodcast: feedNSPodcast, Channel: ch}, nil
}
