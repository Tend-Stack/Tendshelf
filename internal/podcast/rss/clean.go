package rss

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

// NotesMax is the size cap for cleaned show notes.
const NotesMax = 64 << 10

const linkRel = "nofollow noopener noreferrer ugc"

// allowedTags are the only tags that survive in show notes (decision 0038, point 8).
var allowedTags = map[string]bool{
	"p": true, "br": true, "a": true, "ul": true, "ol": true, "li": true,
	"strong": true, "b": true, "em": true, "i": true, "blockquote": true,
}

// droppedWithContent are removed together with everything inside them.
var droppedWithContent = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true, "embed": true, "noscript": true,
	"template": true, "svg": true, "math": true, "head": true, "title": true, "textarea": true,
	"select": true, "button": true, "form": true, "audio": true, "video": true, "applet": true,
}

var (
	tagLike    = regexp.MustCompile(`<[a-zA-Z/!]`)
	paraSplit  = regexp.MustCompile(`\n[ \t]*\n+`)
	urlRun     = regexp.MustCompile(`https?://[^\s<>"']+`)
	trailPunct = ".,;:!?)]}"
)

// CleanHTML reduces show notes to the allowed tags. A text with no tags becomes paragraphs, with
// https?:// runs turned into links. The output is at most max bytes and ends on a tag boundary.
func CleanHTML(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	s = strings.TrimSpace(s)
	if s == "" || max <= 0 {
		return ""
	}
	if !tagLike.MatchString(s) {
		return cleanPlain(s, max)
	}
	return cleanMarkup(s, max)
}

// safeHref returns the address when it is http, https or mailto, else "".
func safeHref(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return ""
		}
	case "mailto":
		if u.Opaque == "" && u.Path == "" {
			return ""
		}
	default:
		return ""
	}
	return raw
}

func anchor(href, text string) string {
	return `<a href="` + html.EscapeString(href) + `" rel="` + linkRel + `">` + text + `</a>`
}

func cleanPlain(s string, max int) string {
	var out strings.Builder
	for _, para := range paraSplit.Split(s, -1) {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		var p strings.Builder
		p.WriteString("<p>")
		last := 0
		for _, loc := range urlRun.FindAllStringIndex(para, -1) {
			raw := para[loc[0]:loc[1]]
			trimmed := strings.TrimRight(raw, trailPunct)
			end := loc[0] + len(trimmed)
			p.WriteString(plainText(para[last:loc[0]]))
			if h := safeHref(trimmed); h != "" {
				p.WriteString(anchor(h, html.EscapeString(trimmed)))
			} else {
				p.WriteString(html.EscapeString(trimmed))
			}
			last = end
		}
		p.WriteString(plainText(para[last:]))
		p.WriteString("</p>")
		if out.Len()+p.Len() > max {
			break
		}
		out.WriteString(p.String())
	}
	if out.Len() == 0 && strings.TrimSpace(s) != "" {
		// One paragraph larger than the cap: keep a cut piece of it.
		return "<p>" + fit(s, max-7) + "</p>"
	}
	return out.String()
}

// plainText escapes text and turns single newlines into <br>.
func plainText(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}

// fit escapes as much of s as fits in n bytes without splitting a rune or an entity.
func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		e := html.EscapeString(string(r))
		if b.Len()+len(e) > n {
			break
		}
		b.WriteString(e)
	}
	return b.String()
}

type openTag struct {
	name    string
	emitted bool
}

func cleanMarkup(s string, max int) string {
	z := xhtml.NewTokenizer(strings.NewReader(s))
	var (
		out   strings.Builder
		stack []openTag
		skip  string // name of a dropped container whose content is being skipped
		depth int
	)
	closeLen := func() int {
		n := 0
		for _, t := range stack {
			if t.emitted {
				n += len(t.name) + 3
			}
		}
		return n
	}
	fits := func(piece string) bool { return out.Len()+len(piece)+closeLen() <= max }
	closeAll := func() {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].emitted {
				out.WriteString("</" + stack[i].name + ">")
			}
		}
		stack = nil
	}
loop:
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			break loop
		case xhtml.TextToken:
			if skip != "" {
				continue
			}
			text := string(z.Text())
			piece := html.EscapeString(text)
			if !fits(piece) {
				room := max - out.Len() - closeLen()
				out.WriteString(fit(text, room))
				break loop
			}
			out.WriteString(piece)
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			tt := z.Token()
			name := strings.ToLower(tt.Data)
			if skip != "" {
				if name == skip && tt.Type == xhtml.StartTagToken {
					depth++
				}
				continue
			}
			if droppedWithContent[name] {
				if tt.Type == xhtml.StartTagToken {
					skip, depth = name, 1
				}
				continue
			}
			if !allowedTags[name] {
				continue
			}
			if name == "br" {
				if !fits("<br>") {
					break loop
				}
				out.WriteString("<br>")
				continue
			}
			open := "<" + name + ">"
			emitted := true
			if name == "a" {
				href := ""
				for _, a := range tt.Attr {
					if strings.EqualFold(a.Key, "href") {
						href = safeHref(a.Val)
						break
					}
				}
				if href == "" {
					emitted = false
				} else {
					open = `<a href="` + html.EscapeString(href) + `" rel="` + linkRel + `">`
				}
			}
			if emitted {
				if out.Len()+len(open)+closeLen()+len(name)+3 > max {
					break loop
				}
				out.WriteString(open)
			}
			if tt.Type == xhtml.SelfClosingTagToken {
				if emitted {
					out.WriteString("</" + name + ">")
				}
				continue
			}
			stack = append(stack, openTag{name, emitted})
		case xhtml.EndTagToken:
			name, _ := z.TagName()
			n := strings.ToLower(string(name))
			if skip != "" {
				if n == skip {
					depth--
					if depth == 0 {
						skip = ""
					}
				}
				continue
			}
			idx := -1
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].name == n {
					idx = i
					break
				}
			}
			if idx < 0 {
				continue
			}
			for i := len(stack) - 1; i >= idx; i-- {
				if stack[i].emitted {
					out.WriteString("</" + stack[i].name + ">")
				}
			}
			stack = stack[:idx]
		}
	}
	closeAll()
	res := strings.TrimSpace(out.String())
	if utf8.ValidString(res) {
		return res
	}
	return strings.ToValidUTF8(res, "")
}
