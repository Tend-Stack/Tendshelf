// Package rss parses podcast feeds: RSS 2.0 with iTunes and Podcasting 2.0 tags, and Atom
// (decision 0038). It does no network I/O; the feed bytes come from podnet.
package rss

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
)

// ErrNotFeed means the bytes are not an RSS or Atom feed.
var ErrNotFeed = errors.New("rss: not a feed")

const (
	maxItems     = 3000
	maxText      = 500
	maxTitle     = 300
	maxDepth     = 64
	maxNodeCount = 2_000_000
)

// Feed is a parsed show.
type Feed struct {
	Title       string
	Author      string
	Description string // cleaned HTML
	Link        string
	Language    string
	ImageURL    string
	NewFeedURL  string
	Explicit    bool
	Episodes    []Item
}

// Item is one episode. PublishedAt is Unix milliseconds, 0 when unknown.
type Item struct {
	GUID        string
	GUIDKey     string
	Title       string
	NotesHTML   string // cleaned HTML
	Link        string
	AudioURL    string
	AudioType   string
	AudioBytes  int64
	DurationMs  int64
	PublishedAt int64
	Season      int
	Number      int
	Kind        string // full, trailer or bonus
	ChaptersURL string
	ImageURL    string
}

// node is a tolerant element tree: names are reduced to a short namespace tag plus the local name.
type node struct {
	ns       string // "", "itunes", "podcast", "atom", "content", "dc", or "other"
	name     string
	attrs    map[string]string
	text     strings.Builder
	children []*node
}

func nsTag(space string) string {
	l := strings.ToLower(space)
	switch {
	case l == "":
		return ""
	case strings.Contains(l, "itunes"):
		return "itunes"
	case strings.Contains(l, "podcastindex") || l == "podcast":
		return "podcast"
	case strings.Contains(l, "w3.org/2005/atom") || l == "atom":
		return "atom"
	case strings.Contains(l, "purl.org/rss/1.0/modules/content") || l == "content":
		return "content"
	case strings.Contains(l, "purl.org/dc/elements") || l == "dc":
		return "dc"
	}
	return "other"
}

func (n *node) child(ns, name string) *node {
	for _, c := range n.children {
		if c.ns == ns && c.name == name {
			return c
		}
	}
	return nil
}

func (n *node) all(ns, name string) []*node {
	var out []*node
	for _, c := range n.children {
		if c.ns == ns && c.name == name {
			out = append(out, c)
		}
	}
	return out
}

// str is the trimmed, whitespace-collapsed text of the first matching child.
func (n *node) str(ns, name string) string {
	if c := n.child(ns, name); c != nil {
		return oneLine(c.text.String())
	}
	return ""
}

// raw is the untouched text of the first matching child.
func (n *node) raw(ns, name string) string {
	if c := n.child(ns, name); c != nil {
		return c.text.String()
	}
	return ""
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func cut(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:max]))
}

// build reads the whole document into a tree, keeping what was read when the XML breaks halfway.
func build(body []byte) *node {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = charset.NewReaderLabel
	var root *node
	var stack []*node
	count := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			if err != io.EOF && root == nil {
				return nil
			}
			return root
		}
		switch t := tok.(type) {
		case xml.StartElement:
			count++
			n := &node{ns: nsTag(t.Name.Space), name: strings.ToLower(t.Name.Local)}
			if len(t.Attr) > 0 {
				n.attrs = make(map[string]string, len(t.Attr))
				for _, a := range t.Attr {
					n.attrs[strings.ToLower(a.Name.Local)] = a.Value
				}
			}
			if root == nil {
				root = n
			} else if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.children = append(p.children, n)
			}
			stack = append(stack, n)
			if len(stack) > maxDepth || count > maxNodeCount {
				return root
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(t)
			}
		}
	}
}

// Parse reads a feed. feedURL resolves relative addresses.
func Parse(body []byte, feedURL string) (Feed, error) {
	root := build(body)
	if root == nil || root.ns != "" && root.ns != "atom" {
		return Feed{}, ErrNotFeed
	}
	base, _ := url.Parse(feedURL)
	switch root.name {
	case "rss":
		ch := root.child("", "channel")
		if ch == nil {
			return Feed{}, ErrNotFeed
		}
		return parseChannel(ch, base), nil
	case "feed":
		if root.ns == "" {
			retag(root) // an Atom feed with no xmlns still reads as Atom
		}
		return parseAtom(root, base), nil
	}
	return Feed{}, ErrNotFeed
}

// retag marks every element with no namespace as Atom.
func retag(n *node) {
	if n.ns == "" {
		n.ns = "atom"
	}
	for _, c := range n.children {
		retag(c)
	}
}

func resolve(base *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return ""
	}
	if u.Host == "" {
		return ""
	}
	return u.String()
}

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "true", "explicit":
		return true
	}
	return false
}

func parseChannel(ch *node, base *url.URL) Feed {
	f := Feed{
		Title:    cut(ch.str("", "title"), maxTitle),
		Language: cut(ch.str("", "language"), 35),
		Author:   cut(firstNonEmpty(ch.str("itunes", "author"), ch.str("", "managingeditor"), ch.str("dc", "creator")), maxText),
		Explicit: isTrue(ch.str("itunes", "explicit")),
	}
	f.Link = resolve(base, ch.str("", "link"))
	if f.Link == "" {
		for _, l := range ch.all("atom", "link") {
			if rel := l.attrs["rel"]; rel == "alternate" {
				f.Link = resolve(base, l.attrs["href"])
			}
		}
	}
	f.Description = CleanHTML(firstNonEmpty(ch.raw("", "description"), ch.raw("itunes", "summary"), ch.raw("itunes", "subtitle")), NotesMax)
	if img := ch.child("itunes", "image"); img != nil {
		f.ImageURL = resolve(base, img.attrs["href"])
	}
	if f.ImageURL == "" {
		if img := ch.child("", "image"); img != nil {
			f.ImageURL = resolve(base, img.str("", "url"))
		}
	}
	f.NewFeedURL = resolve(base, ch.str("itunes", "new-feed-url"))
	for _, it := range ch.all("", "item") {
		if item, ok := parseItem(it, base, false); ok {
			f.Episodes = append(f.Episodes, item)
		}
	}
	f.Episodes = finish(f.Episodes)
	return f
}

func parseAtom(root *node, base *url.URL) Feed {
	f := Feed{
		Title:    cut(oneLine(root.raw("atom", "title")), maxTitle),
		Language: cut(root.attrs["lang"], 35),
		Explicit: isTrue(root.str("itunes", "explicit")),
	}
	if a := root.child("atom", "author"); a != nil {
		f.Author = cut(a.str("atom", "name"), maxText)
	}
	if f.Author == "" {
		f.Author = cut(root.str("itunes", "author"), maxText)
	}
	for _, l := range root.all("atom", "link") {
		if rel := l.attrs["rel"]; rel == "" || rel == "alternate" {
			f.Link = resolve(base, l.attrs["href"])
			break
		}
	}
	f.Description = CleanHTML(firstNonEmpty(root.raw("atom", "subtitle"), root.raw("itunes", "summary")), NotesMax)
	if img := root.child("itunes", "image"); img != nil {
		f.ImageURL = resolve(base, img.attrs["href"])
	}
	if f.ImageURL == "" {
		f.ImageURL = resolve(base, firstNonEmpty(root.str("atom", "logo"), root.str("atom", "icon")))
	}
	f.NewFeedURL = resolve(base, root.str("itunes", "new-feed-url"))
	for _, e := range root.all("atom", "entry") {
		if item, ok := parseItem(e, base, true); ok {
			f.Episodes = append(f.Episodes, item)
		}
	}
	f.Episodes = finish(f.Episodes)
	return f
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

var audioExt = map[string]bool{".mp3": true, ".m4a": true, ".aac": true, ".ogg": true, ".oga": true, ".opus": true, ".wav": true, ".flac": true, ".m4b": true}

type enclosure struct {
	url, typ string
	bytes    int64
}

func parseItem(it *node, base *url.URL, atom bool) (Item, bool) {
	var encs []enclosure
	if atom {
		for _, l := range it.all("atom", "link") {
			if l.attrs["rel"] == "enclosure" {
				encs = append(encs, enclosure{l.attrs["href"], l.attrs["type"], atoi64(l.attrs["length"])})
			}
		}
	} else {
		for _, e := range it.all("", "enclosure") {
			encs = append(encs, enclosure{e.attrs["url"], e.attrs["type"], atoi64(e.attrs["length"])})
		}
	}
	var pick *enclosure
	var pickURL string
	rank := 0
	for i := range encs {
		e := &encs[i]
		u := resolve(base, e.url)
		if u == "" {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(strings.SplitN(e.typ, ";", 2)[0]))
		r := 0
		switch {
		case strings.HasPrefix(typ, "audio/"):
			r = 3
		case strings.HasPrefix(typ, "video/"):
			r = 2
		case typ == "" || typ == "application/octet-stream":
			if p, err := url.Parse(u); err == nil && audioExt[strings.ToLower(path.Ext(p.Path))] {
				r = 1
			}
		}
		if r > rank {
			rank, pick, pickURL = r, e, u
		}
	}
	if pick == nil {
		return Item{}, false
	}
	var item Item
	item.AudioURL = pickURL
	item.AudioType = cut(strings.TrimSpace(pick.typ), 100)
	item.AudioBytes = pick.bytes

	var title, notes, guid, pub string
	if atom {
		title = oneLine(it.raw("atom", "title"))
		notes = firstNonEmpty(it.raw("atom", "content"), it.raw("atom", "summary"), it.raw("itunes", "summary"))
		guid = it.str("atom", "id")
		pub = firstNonEmpty(it.str("atom", "published"), it.str("atom", "updated"))
		for _, l := range it.all("atom", "link") {
			if rel := l.attrs["rel"]; rel == "" || rel == "alternate" {
				item.Link = resolve(base, l.attrs["href"])
				break
			}
		}
	} else {
		title = it.str("", "title")
		notes = firstNonEmpty(it.raw("content", "encoded"), it.raw("", "description"), it.raw("itunes", "summary"), it.raw("itunes", "subtitle"))
		guid = it.str("", "guid")
		pub = firstNonEmpty(it.str("", "pubdate"), it.str("dc", "date"))
		item.Link = resolve(base, it.str("", "link"))
	}
	if title == "" {
		title = oneLine(it.raw("itunes", "title"))
	}
	item.Title = cut(title, maxTitle)
	item.NotesHTML = CleanHTML(notes, NotesMax)
	item.PublishedAt = parseDate(pub)
	item.DurationMs = parseDuration(it.str("itunes", "duration"))
	item.Season = int(clampInt(atoi64(it.str("itunes", "season"))))
	item.Number = int(clampInt(atoi64(it.str("itunes", "episode"))))
	switch k := strings.ToLower(it.str("itunes", "episodetype")); k {
	case "trailer", "bonus":
		item.Kind = k
	default:
		item.Kind = "full"
	}
	if img := it.child("itunes", "image"); img != nil {
		item.ImageURL = resolve(base, img.attrs["href"])
	}
	for _, c := range it.all("podcast", "chapters") {
		if t := strings.ToLower(c.attrs["type"]); t == "" || strings.Contains(t, "json") {
			if u := resolve(base, c.attrs["url"]); u != "" {
				item.ChaptersURL = u
				break
			}
		}
	}
	item.GUID = cut(guid, maxText)
	switch {
	case guid != "":
		item.GUIDKey = hashKey(guid)
	case item.AudioURL != "":
		item.GUIDKey = hashKey(item.AudioURL)
	default:
		item.GUIDKey = hashKey(item.Title + "|" + strconv.FormatInt(item.PublishedAt, 10))
	}
	return item, true
}

func hashKey(s string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(sum[:])
}

func atoi64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func clampInt(n int64) int64 {
	if n > 1_000_000 {
		return 0
	}
	return n
}

// finish drops repeated keys (the first stays) and keeps the newest 3,000.
func finish(items []Item) []Item {
	seen := make(map[string]bool, len(items))
	out := items[:0]
	for _, it := range items {
		if seen[it.GUIDKey] {
			continue
		}
		seen[it.GUIDKey] = true
		out = append(out, it)
	}
	if len(out) > maxItems {
		sort.SliceStable(out, func(i, j int) bool { return out[i].PublishedAt > out[j].PublishedAt })
		out = out[:maxItems]
	}
	return out
}

// parseDuration reads ss, mm:ss or hh:mm:ss (seconds may have a fraction) into milliseconds; 0 when unreadable.
func parseDuration(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0
	}
	var total float64
	for _, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0
		}
		total = total*60 + v
	}
	ms := int64(total * 1000)
	if ms > 7*24*3600*1000 {
		return 0
	}
	return ms
}

var dateLayouts = []string{
	time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC3339, time.RFC3339Nano,
	"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 06 15:04:05 -0700", "Mon, 2 Jan 06 15:04:05 MST",
	"2 Jan 2006 15:04:05 -0700", "2 Jan 2006 15:04:05 MST", "2 Jan 06 15:04 -0700", "2 Jan 06 15:04 MST",
	"Mon, 02 Jan 2006 15:04 -0700", "Mon, 2 Jan 2006 15:04 -0700", "Mon, 2 Jan 2006 15:04 MST",
	"Mon, 02 Jan 2006 15:04 MST", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
}

// parseDate returns Unix milliseconds, or 0 when no layout fits.
func parseDate(s string) int64 {
	s = oneLine(s)
	if s == "" {
		return 0
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			if ms := t.UnixMilli(); ms > 0 {
				return ms
			}
			return 0
		}
	}
	return 0
}
