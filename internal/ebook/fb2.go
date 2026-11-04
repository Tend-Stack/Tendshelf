package ebook

import (
	"encoding/xml"
	"io"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/textmatch"
)

func fb2Href(se xml.StartElement) string {
	for _, a := range se.Attr {
		if a.Name.Local == "href" {
			return a.Value
		}
	}
	return ""
}

// parseFB2 reads a FictionBook 2 file. Charsets other than UTF-8 come from the XML declaration.
func parseFB2(data []byte, lim Limits) (*Doc, error) {
	d := newXMLBytes(data, lim)
	b := newBuilder("fb2", lim)
	var meta Meta
	var stack []string
	in := func(name string) bool {
		for _, s := range stack {
			if s == name {
				return true
			}
		}
		return false
	}
	mode := "" // main, notes or skip, set by body
	mainSeen := false
	secDepth := 0
	quoteDepth := 0
	var styles []string
	var cur []seg
	inText := false // inside p, v, subtitle, text-author, td or th
	var titleBuf []string
	inTitle := false
	var notes [][]seg
	var author []string
	chapterIdx := -1

	emit := func(kind string, level int, segs []seg) {
		switch mode {
		case "main":
			b.Block(kind, level, segs, nil)
		case "notes":
			notes = append(notes, segs)
		}
	}
	for {
		t, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if b.err != nil {
			return nil, b.err
		}
		switch v := t.(type) {
		case xml.StartElement:
			name := v.Name.Local
			stack = append(stack, name)
			switch name {
			case "body":
				switch {
				case attr(v, "name") == "notes":
					mode = "notes"
				case !mainSeen:
					mode, mainSeen = "main", true
				default:
					mode = "skip"
				}
			case "book-title":
				if in("title-info") {
					txt, err := readElementText(d)
					if err != nil {
						return nil, err
					}
					stack = stack[:len(stack)-1]
					meta.Title = txt
				}
			case "lang":
				if in("title-info") {
					txt, err := readElementText(d)
					if err != nil {
						return nil, err
					}
					stack = stack[:len(stack)-1]
					meta.Language = strings.TrimSpace(txt)
				}
			case "isbn":
				if in("publish-info") {
					txt, err := readElementText(d)
					if err != nil {
						return nil, err
					}
					stack = stack[:len(stack)-1]
					meta.Identifiers.ISBN = textmatch.ISBN(txt)
				}
			case "author":
				if in("title-info") && !in("body") {
					author = nil
				}
			case "first-name", "middle-name", "last-name", "nickname":
				if in("title-info") && in("author") {
					txt, err := readElementText(d)
					if err != nil {
						return nil, err
					}
					stack = stack[:len(stack)-1]
					if n := normalizeString(txt); n != "" {
						if name == "nickname" {
							if len(author) == 0 {
								author = append(author, n)
							}
						} else {
							author = append(author, n)
						}
					}
				}
			case "section":
				if mode == "main" {
					secDepth++
					if secDepth == 1 {
						b.Chapter("", "", nil)
						chapterIdx = len(b.chapters) - 1
					}
				}
			case "epigraph", "cite":
				quoteDepth++
			case "title":
				if mode != "" && mode != "skip" && in("section") {
					inTitle = true
					titleBuf = nil
				}
			case "p", "v", "subtitle", "text-author", "td", "th":
				if mode != "" && mode != "skip" {
					inText = true
					cur = nil
				}
			case "emphasis":
				styles = append(styles, "em")
			case "strong":
				styles = append(styles, "strong")
			case "code":
				styles = append(styles, "code")
			case "image":
				if mode == "main" {
					if href := strings.TrimPrefix(fb2Href(v), "#"); href != "" {
						b.Image(href, firstNonEmpty(attr(v, "alt"), attr(v, "title")), nil)
					}
				}
			}
		case xml.CharData:
			if inText {
				st := ""
				if len(styles) > 0 {
					st = styles[len(styles)-1]
				}
				cur = append(cur, seg{Text: string(v), Style: st, ByteOff: -1})
			}
		case xml.EndElement:
			name := v.Name.Local
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			switch name {
			case "body":
				mode = ""
				secDepth = 0
			case "author":
				if len(author) > 0 && in("title-info") {
					meta.Authors = append(meta.Authors, strings.Join(author, " "))
				}
				author = nil
			case "section":
				if mode == "main" && secDepth > 0 {
					secDepth--
				}
			case "epigraph", "cite":
				if quoteDepth > 0 {
					quoteDepth--
				}
			case "emphasis", "strong", "code":
				if len(styles) > 0 {
					styles = styles[:len(styles)-1]
				}
			case "title":
				if inTitle {
					inTitle = false
					text := strings.Join(titleBuf, " ")
					if mode == "main" && secDepth > 0 {
						level := secDepth
						if level > 6 {
							level = 6
						}
						if secDepth == 1 && chapterIdx >= 0 {
							b.chapters[chapterIdx].Title = normalizeString(text)
						}
						b.Block(KindH, level, []seg{{Text: text, ByteOff: -1}}, nil)
					}
				}
			case "p", "v", "subtitle", "text-author", "td", "th":
				if !inText {
					continue
				}
				inText = false
				segs := cur
				cur = nil
				switch {
				case inTitle:
					titleBuf = append(titleBuf, joinSegs(segs))
				case mode == "notes":
					emit(KindNote, 0, segs)
				case quoteDepth > 0:
					emit(KindQuote, 0, segs)
				default:
					emit(KindP, 0, segs)
				}
			}
		}
	}
	if len(notes) > 0 {
		b.Chapter("Notes", "", nil)
		for _, n := range notes {
			b.Block(KindNote, 0, n, nil)
		}
	}
	return b.Finish(meta, 0)
}
