package ebook

import (
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

type docxStyle struct {
	name    string
	outline int // -1 when unset
}

func parseDocxStyles(zb *zipBox, lim Limits) (map[string]docxStyle, error) {
	styles := map[string]docxStyle{}
	if !zb.Has("word/styles.xml") {
		return styles, nil
	}
	data, err := zb.Read("word/styles.xml")
	if err != nil {
		return nil, err
	}
	d := newXMLBytes(data, lim)
	cur := ""
	for {
		t, err := d.Next()
		if err == io.EOF {
			return styles, nil
		}
		if err != nil {
			return nil, err
		}
		se, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "style":
			cur = attr(se, "styleId")
			styles[cur] = docxStyle{outline: -1}
		case "name":
			if cur != "" {
				s := styles[cur]
				s.name = strings.ToLower(attr(se, "val"))
				styles[cur] = s
			}
		case "outlineLvl":
			if cur != "" {
				if n, err := strconv.Atoi(attr(se, "val")); err == nil {
					s := styles[cur]
					s.outline = n
					styles[cur] = s
				}
			}
		}
	}
}

// ditem is one paragraph of a Word document.
type ditem struct {
	chapter bool // starts a chapter
	kind    string
	level   int
	segs    []seg
	cites   []string // footnote ids cited in the paragraph
}

func onVal(se xml.StartElement) bool {
	switch strings.ToLower(attr(se, "val")) {
	case "0", "false", "off", "none":
		return false
	}
	return true
}

// docxParagraphs streams a WordprocessingML part into paragraphs.
func docxParagraphs(data []byte, styles map[string]docxStyle, lim Limits) ([]ditem, error) {
	d := newXMLBytes(data, lim)
	var items []ditem
	var cur ditem
	inP, inR, inPPr, inT := false, false, false, false
	em, strong := false, false
	pStyle, outline, numbered := "", -1, false
	var runText strings.Builder
	flushRun := func() {
		if runText.Len() == 0 {
			return
		}
		st := ""
		switch {
		case strong:
			st = "strong"
		case em:
			st = "em"
		}
		cur.segs = append(cur.segs, seg{Text: runText.String(), Style: st, ByteOff: -1})
		runText.Reset()
	}
	for {
		t, err := d.Next()
		if err == io.EOF {
			return items, nil
		}
		if err != nil {
			return nil, err
		}
		switch v := t.(type) {
		case xml.StartElement:
			switch v.Name.Local {
			case "p":
				inP = true
				cur = ditem{kind: KindP}
				pStyle, outline, numbered = "", -1, false
			case "pPr":
				inPPr = true
			case "pStyle":
				if inPPr {
					pStyle = attr(v, "val")
				}
			case "outlineLvl":
				if inPPr {
					if n, err := strconv.Atoi(attr(v, "val")); err == nil {
						outline = n
					}
				}
			case "numPr":
				if inPPr {
					numbered = true
				}
			case "r":
				if inP {
					inR, em, strong = true, false, false
				}
			case "i":
				if inR {
					em = onVal(v)
				}
			case "b":
				if inR {
					strong = onVal(v)
				}
			case "t":
				if inR {
					inT = true
				}
			case "tab", "br", "cr":
				if inR {
					runText.WriteString(" ")
				}
			case "footnoteReference":
				if inR {
					cur.cites = append(cur.cites, attr(v, "id"))
				}
			}
		case xml.CharData:
			if inT {
				runText.Write(v)
			}
		case xml.EndElement:
			switch v.Name.Local {
			case "t":
				inT = false
			case "r":
				if inR {
					flushRun()
					inR = false
				}
			case "pPr":
				inPPr = false
			case "p":
				if !inP {
					continue
				}
				inP = false
				kind, level, chapter := KindP, 0, false
				name := strings.ToLower(pStyle)
				if s, ok := styles[pStyle]; ok {
					name = s.name
					if outline < 0 {
						outline = s.outline
					}
				}
				switch {
				case name == "title" || name == "heading 1" || name == "heading1" || outline == 0:
					kind, level, chapter = KindH, 1, true
				case strings.HasPrefix(name, "heading "), strings.HasPrefix(name, "heading"):
					n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(name, "heading")))
					if err == nil && n >= 2 && n <= 6 {
						kind, level = KindH, n
					}
				case numbered:
					kind = KindLI
				}
				if kind == KindP && outline >= 1 && outline <= 5 {
					kind, level = KindH, outline+1
				}
				cur.kind, cur.level, cur.chapter = kind, level, chapter
				items = append(items, cur)
				if len(items) > lim.MaxBlocks {
					return nil, damaged("too_many_blocks")
				}
			}
		}
	}
}

func docxMeta(zb *zipBox, lim Limits) (Meta, error) {
	var m Meta
	if !zb.Has("docProps/core.xml") {
		return m, nil
	}
	data, err := zb.Read("docProps/core.xml")
	if err != nil {
		return m, err
	}
	d := newXMLBytes(data, lim)
	for {
		t, err := d.Next()
		if err != nil {
			return m, nil
		}
		se, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "title", "creator", "language":
			txt, err := readElementText(d)
			if err != nil {
				return m, nil
			}
			txt = normalizeString(txt)
			switch se.Name.Local {
			case "title":
				m.Title = txt
			case "creator":
				if txt != "" {
					m.Authors = append(m.Authors, txt)
				}
			case "language":
				m.Language = txt
			}
		}
	}
}

func parseDOCX(zb *zipBox, lim Limits) (*Doc, error) {
	styles, err := parseDocxStyles(zb, lim)
	if err != nil {
		return nil, err
	}
	data, err := zb.Read("word/document.xml")
	if err != nil {
		if r, ok := err.(*Refusal); ok {
			return nil, r
		}
		return nil, damaged("no_document")
	}
	items, err := docxParagraphs(data, styles, lim)
	if err != nil {
		return nil, err
	}
	notes := map[string][]ditem{}
	if zb.Has("word/footnotes.xml") {
		fdata, err := zb.Read("word/footnotes.xml")
		if err != nil {
			return nil, err
		}
		notes, err = docxFootnotes(fdata, styles, lim)
		if err != nil {
			return nil, err
		}
	}
	b := newBuilder("docx", lim)
	// Chapter groups: items between chapter starts. Notes go to the chapter that first cites them.
	emitted := map[string]bool{}
	var pending []string
	closeChapter := func() {
		for _, id := range pending {
			for _, n := range notes[id] {
				b.Block(KindNote, 0, n.segs, nil)
			}
		}
		pending = nil
	}
	for _, it := range items {
		if it.chapter {
			closeChapter()
			b.Chapter(joinSegs(it.segs), "", nil)
		}
		b.Block(it.kind, it.level, it.segs, nil)
		for _, id := range it.cites {
			if _, ok := notes[id]; ok && !emitted[id] {
				emitted[id] = true
				pending = append(pending, id)
			}
		}
		if b.err != nil {
			return nil, b.err
		}
	}
	closeChapter()
	meta, err := docxMeta(zb, lim)
	if err != nil {
		return nil, err
	}
	return b.Finish(meta, 0)
}

// docxFootnotes reads word/footnotes.xml into paragraphs by note id; separators are left out.
func docxFootnotes(data []byte, styles map[string]docxStyle, lim Limits) (map[string][]ditem, error) {
	return docxParagraphsByNote(data, styles, lim)
}

// docxParagraphsByNote groups paragraphs by the id of the enclosing w:footnote.
func docxParagraphsByNote(data []byte, styles map[string]docxStyle, lim Limits) (map[string][]ditem, error) {
	out := map[string][]ditem{}
	d := newXMLBytes(data, lim)
	id, skip := "", false
	var cur []seg
	inT, inR := false, false
	em, strong := false, false
	var run strings.Builder
	flushRun := func() {
		if run.Len() == 0 {
			return
		}
		st := ""
		switch {
		case strong:
			st = "strong"
		case em:
			st = "em"
		}
		cur = append(cur, seg{Text: run.String(), Style: st, ByteOff: -1})
		run.Reset()
	}
	for {
		t, err := d.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch v := t.(type) {
		case xml.StartElement:
			switch v.Name.Local {
			case "footnote":
				id = attr(v, "id")
				ty := attr(v, "type")
				skip = ty == "separator" || ty == "continuationSeparator" || ty == "continuationNotice"
			case "p":
				cur = nil
			case "r":
				inR, em, strong = true, false, false
			case "i":
				if inR {
					em = onVal(v)
				}
			case "b":
				if inR {
					strong = onVal(v)
				}
			case "t":
				inT = inR
			case "tab", "br", "cr":
				if inR {
					run.WriteString(" ")
				}
			}
		case xml.CharData:
			if inT {
				run.Write(v)
			}
		case xml.EndElement:
			switch v.Name.Local {
			case "t":
				inT = false
			case "r":
				flushRun()
				inR = false
			case "p":
				if !skip && id != "" && len(cur) > 0 {
					out[id] = append(out[id], ditem{kind: KindNote, segs: cur})
				}
				cur = nil
			}
		}
	}
}
