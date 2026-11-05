package dict

import (
	"html"
	"strings"
)

// stripMarkup turns an HTML, Pango or XDXF fragment into plain text. It is a tokenizer, not a parser: tags are
// dropped, the text inside <script> and <style> goes with them, tags that end a line (br, p, div, li, tr and the
// headings) become a line break, and character references are decoded. What comes out is text only; the page
// escapes it again, so nothing a dictionary holds can run.
func stripMarkup(src string) string {
	var out strings.Builder
	skip := "" // the element whose text is being dropped
	for len(src) > 0 {
		lt := strings.IndexByte(src, '<')
		if lt < 0 {
			if skip == "" {
				out.WriteString(html.UnescapeString(src))
			}
			break
		}
		if skip == "" {
			out.WriteString(html.UnescapeString(src[:lt]))
		}
		src = src[lt:]
		gt := strings.IndexByte(src, '>')
		if gt < 0 { // a tag that never ends: the rest is not text
			break
		}
		tag := strings.ToLower(strings.TrimSpace(strings.Trim(src[1:gt], "/")))
		closing := strings.HasPrefix(src[1:], "/")
		name := tag
		if i := strings.IndexAny(name, " \t\r\n/"); i >= 0 {
			name = name[:i]
		}
		src = src[gt+1:]
		switch {
		case skip != "":
			if closing && name == skip {
				skip = ""
			}
		case !closing && (name == "script" || name == "style"):
			skip = name
		case isBreak(name):
			out.WriteByte('\n')
		}
	}
	return squeeze(out.String())
}

func isBreak(name string) bool {
	switch name {
	case "br", "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "dd", "dt", "blockquote":
		return true
	}
	return false
}

// squeeze trims every line, joins runs of spaces and drops empty lines.
func squeeze(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
