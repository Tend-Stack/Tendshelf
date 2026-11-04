package ebooktest

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

// PDFPageW and PDFPageH are the page size in points (US Letter) the writer uses.
const (
	PDFPageW = 612
	PDFPageH = 792
)

// Line is one line of text placed at X, Y (the baseline, from the bottom left) at Size points.
type Line struct {
	Text string
	X, Y float64
	Size float64
}

// PDFOptions describes a synthetic PDF 1.4 (or 1.5 with XRefStream) file.
type PDFOptions struct {
	Title, Author string
	Pages         [][]Line
	// Type0ToUnicode writes the font as Type0 Identity-H with a ToUnicode CMap, so glyph codes are
	// not characters.
	Type0ToUnicode bool
	// XRefStream puts the non-stream objects in an object stream and ends with a cross-reference stream.
	XRefStream bool
	// Incremental appends an update that replaces the content of the last page; the first version of
	// that page says "Superseded text".
	Incremental bool
	// TwoColumns lays the lines of each page out in two columns, the first half left, the rest right.
	TwoColumns bool
	// ImageOnly draws one image per page and no text.
	ImageOnly bool
	// Encrypt adds a standard-handler /Encrypt dictionary to the trailer (the bytes stay readable;
	// tests only need the marker).
	Encrypt bool
	// Header and Footer are repeated on every page; PageNumbers adds the page number below the footer.
	Header, Footer string
	PageNumbers    bool
}

// Lines lays texts out top down from y=700 at x=72, with a line step of 1.4 times size.
func Lines(size float64, texts ...string) []Line {
	out := make([]Line, len(texts))
	y := 700.0
	for i, t := range texts {
		out[i] = Line{Text: t, X: 72, Y: y, Size: size}
		y -= size * 1.4
	}
	return out
}

type pdfObj struct {
	num    int
	dict   string // dictionary text (for streams: without Length and Filter)
	stream []byte // compressed stream data when isStm
	isStm  bool
}

func deflate(b []byte) []byte {
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

// utf16Hex is a PDF text string: a hex string of UTF-16BE with a byte order mark.
func utf16Hex(s string) string {
	var sb strings.Builder
	sb.WriteString("<FEFF")
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&sb, "%04X", u)
	}
	sb.WriteString(">")
	return sb.String()
}

func pdfEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`).Replace(s)
}

// PDF builds the file bytes.
func PDF(t testing.TB, o PDFOptions) []byte {
	t.Helper()
	pages := make([][]Line, len(o.Pages))
	for i, p := range o.Pages {
		pages[i] = append([]Line(nil), p...)
		if o.TwoColumns {
			half := (len(p) + 1) / 2
			for j := range pages[i] {
				if j < half {
					pages[i][j].X = 50
					pages[i][j].Y = 700 - float64(j)*15
				} else {
					pages[i][j].X = 330
					pages[i][j].Y = 700 - float64(j-half)*15
				}
			}
		}
		if o.Header != "" {
			pages[i] = append(pages[i], Line{Text: o.Header, X: 72, Y: 760, Size: 9})
		}
		if o.Footer != "" {
			pages[i] = append(pages[i], Line{Text: o.Footer, X: 72, Y: 40, Size: 9})
		}
		if o.PageNumbers {
			pages[i] = append(pages[i], Line{Text: fmt.Sprint(i + 1), X: 300, Y: 25, Size: 9})
		}
	}

	// Glyph codes for Type0: runes in sorted order, code = index + 1.
	codes := map[rune]int{}
	if o.Type0ToUnicode {
		set := map[rune]bool{}
		for _, p := range pages {
			for _, l := range p {
				for _, r := range l.Text {
					set[r] = true
				}
			}
		}
		var rs []rune
		for r := range set {
			rs = append(rs, r)
		}
		sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
		for i, r := range rs {
			codes[r] = i + 1
		}
	}
	content := func(p []Line) []byte {
		if o.ImageOnly {
			return []byte("q 500 0 0 600 50 100 cm /Im1 Do Q\n")
		}
		var sb strings.Builder
		for _, l := range p {
			fmt.Fprintf(&sb, "BT /F1 %g Tf %g %g Td ", l.Size, l.X, l.Y)
			if o.Type0ToUnicode {
				sb.WriteString("<")
				for _, r := range l.Text {
					fmt.Fprintf(&sb, "%04X", codes[r])
				}
				sb.WriteString("> Tj ET\n")
			} else {
				enc, err := charmap.Windows1252.NewEncoder().String(l.Text)
				if err != nil {
					t.Fatalf("pdf text %q: %v", l.Text, err)
				}
				sb.WriteString("(" + pdfEscape(enc) + ") Tj ET\n")
			}
		}
		return []byte(sb.String())
	}

	var objs []*pdfObj
	next := 1
	add := func(dict string) *pdfObj {
		ob := &pdfObj{num: next, dict: dict}
		next++
		objs = append(objs, ob)
		return ob
	}
	addStm := func(dict string, raw []byte) *pdfObj {
		ob := add(dict)
		ob.isStm = true
		ob.stream = deflate(raw)
		return ob
	}
	catalog := add("")
	pagesObj := add("")
	font := add("")
	var info *pdfObj
	if o.Title != "" || o.Author != "" {
		info = add(fmt.Sprintf("<< /Title %s /Author %s >>", utf16Hex(o.Title), utf16Hex(o.Author)))
	}
	if o.Type0ToUnicode {
		var cm strings.Builder
		cm.WriteString("/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n")
		cm.WriteString("/CMapName /Adobe-Identity-UCS def /CMapType 2 def\n")
		cm.WriteString("1 begincodespacerange <0000> <FFFF> endcodespacerange\n")
		var rs []rune
		for r := range codes {
			rs = append(rs, r)
		}
		sort.Slice(rs, func(i, j int) bool { return codes[rs[i]] < codes[rs[j]] })
		fmt.Fprintf(&cm, "%d beginbfchar\n", len(rs))
		for _, r := range rs {
			fmt.Fprintf(&cm, "<%04X> <%04X>\n", codes[r], r)
		}
		cm.WriteString("endbfchar\nendcmap CMapName currentdict /CMap defineresource pop end end\n")
		tu := addStm("", []byte(cm.String()))
		cid := add("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /Helvetica /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /DW 500 >>")
		font.dict = fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /Helvetica /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>", cid.num, tu.num)
	} else {
		font.dict = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"
	}
	res := fmt.Sprintf("<< /Font << /F1 %d 0 R >>", font.num)
	if o.ImageOnly {
		img := addStm("<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceGray /BitsPerComponent 8 >>", []byte{0, 80, 160, 255})
		res += fmt.Sprintf(" /XObject << /Im1 %d 0 R >>", img.num)
	}
	res += " >>"

	var kids []string
	replNum := 0
	var replStream []byte
	for i, p := range pages {
		c := addStm("", content(p))
		if o.Incremental && i == len(pages)-1 {
			// The page first points at a stream that says something else; the update replaces it.
			replNum, replStream = c.num, c.stream
			c.stream = deflate([]byte("BT /F1 12 Tf 72 700 Td (Superseded text) Tj ET\n"))
		}
		pg := add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %d %d] /Resources %s /Contents %d 0 R >>", pagesObj.num, PDFPageW, PDFPageH, res, c.num))
		kids = append(kids, fmt.Sprintf("%d 0 R", pg.num))
	}
	pagesObj.dict = fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(pages), strings.Join(kids, " "))
	catalog.dict = fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesObj.num)
	var enc *pdfObj
	if o.Encrypt {
		enc = add("<< /Filter /Standard /V 1 /R 2 /Length 40 /P -4 /O <" + strings.Repeat("00", 32) + "> /U <" + strings.Repeat("11", 32) + "> >>")
	}

	trailerExtra := fmt.Sprintf("/Root %d 0 R", catalog.num)
	if info != nil {
		trailerExtra += fmt.Sprintf(" /Info %d 0 R", info.num)
	}
	if enc != nil {
		trailerExtra += fmt.Sprintf(" /Encrypt %d 0 R", enc.num)
	}
	trailerExtra += " /ID [<00112233445566778899aabbccddeeff> <00112233445566778899aabbccddeeff>]"

	var out bytes.Buffer
	if o.XRefStream {
		out.WriteString("%PDF-1.5\n%\xe2\xe3\xcf\xd3\n")
	} else {
		out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	}
	offsets := map[int]int{}
	writeObj := func(ob *pdfObj) {
		offsets[ob.num] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", ob.num)
		if ob.isStm {
			d := strings.TrimSpace(ob.dict)
			extra := fmt.Sprintf("/Length %d /Filter /FlateDecode", len(ob.stream))
			if d == "" {
				d = "<< " + extra + " >>"
			} else {
				d = strings.TrimSuffix(d, ">>") + extra + " >>"
			}
			out.WriteString(d + "\nstream\n")
			out.Write(ob.stream)
			out.WriteString("\nendstream")
		} else {
			out.WriteString(ob.dict)
		}
		out.WriteString("\nendobj\n")
	}

	if o.XRefStream {
		return pdfXRefStream(&out, objs, enc, next, trailerExtra, offsets, writeObj, replNum, replStream)
	}

	size := next
	for _, ob := range objs {
		writeObj(ob)
	}
	writeXref := func(nums []int, prev int) int {
		off := out.Len()
		out.WriteString("xref\n")
		if nums == nil {
			fmt.Fprintf(&out, "0 %d\n0000000000 65535 f \n", size)
			for n := 1; n < size; n++ {
				fmt.Fprintf(&out, "%010d 00000 n \n", offsets[n])
			}
		} else {
			for _, n := range nums {
				fmt.Fprintf(&out, "%d 1\n%010d 00000 n \n", n, offsets[n])
			}
		}
		fmt.Fprintf(&out, "trailer\n<< /Size %d %s", size, trailerExtra)
		if prev > 0 {
			fmt.Fprintf(&out, " /Prev %d", prev)
		}
		fmt.Fprintf(&out, " >>\nstartxref\n%d\n%%%%EOF\n", off)
		return off
	}
	first := writeXref(nil, 0)
	if o.Incremental {
		writeObj(&pdfObj{num: replNum, isStm: true, stream: replStream})
		writeXref([]int{replNum}, first)
	}
	return out.Bytes()
}

// pdfXRefStream finishes a PDF 1.5 file: stream objects first, one object stream with the others, then a
// cross-reference stream. With an update (replNum > 0) it appends the new stream and a second
// cross-reference stream with /Prev.
func pdfXRefStream(out *bytes.Buffer, objs []*pdfObj, enc *pdfObj, next int, trailerExtra string, offsets map[int]int, writeObj func(*pdfObj), replNum int, replStream []byte) []byte {
	var packed []*pdfObj
	for _, ob := range objs {
		if ob.isStm || ob == enc {
			writeObj(ob)
		} else {
			packed = append(packed, ob)
		}
	}
	var head, body bytes.Buffer
	inStm := map[int]int{}
	for i, ob := range packed {
		fmt.Fprintf(&head, "%d %d ", ob.num, body.Len())
		body.WriteString(ob.dict + "\n")
		inStm[ob.num] = i
	}
	osNum := next
	xrefNum := next + 1
	size := next + 2
	z := deflate(append(append([]byte{}, head.Bytes()...), body.Bytes()...))
	offsets[osNum] = out.Len()
	fmt.Fprintf(out, "%d 0 obj\n<< /Type /ObjStm /N %d /First %d /Length %d /Filter /FlateDecode >>\nstream\n", osNum, len(packed), head.Len(), len(z))
	out.Write(z)
	out.WriteString("\nendstream\nendobj\n")

	entry := func(xr *bytes.Buffer, n, xrefOff int) {
		switch {
		case n == 0:
			xr.Write([]byte{0, 0, 0, 0, 0, 0xff, 0xff})
		case n == xrefNum:
			xr.Write([]byte{1, byte(xrefOff >> 24), byte(xrefOff >> 16), byte(xrefOff >> 8), byte(xrefOff), 0, 0})
		default:
			if idx, ok := inStm[n]; ok {
				xr.Write([]byte{2, 0, 0, byte(osNum >> 8), byte(osNum), byte(idx >> 8), byte(idx)})
				return
			}
			off := offsets[n]
			xr.Write([]byte{1, byte(off >> 24), byte(off >> 16), byte(off >> 8), byte(off), 0, 0})
		}
	}
	xrefOff := out.Len()
	var xr bytes.Buffer
	for n := 0; n < size; n++ {
		entry(&xr, n, xrefOff)
	}
	z = deflate(xr.Bytes())
	fmt.Fprintf(out, "%d 0 obj\n<< /Type /XRef /Size %d /W [1 4 2] %s /Length %d /Filter /FlateDecode >>\nstream\n", xrefNum, size, trailerExtra, len(z))
	out.Write(z)
	fmt.Fprintf(out, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", xrefOff)
	if replNum == 0 {
		return out.Bytes()
	}
	// Update: the replaced stream is no longer in the object stream (it is a stream, so never was).
	writeObj(&pdfObj{num: replNum, isStm: true, stream: replStream})
	upNum := size
	upOff := out.Len()
	var up bytes.Buffer
	// Index covers the replaced object and this stream.
	e := func(off int) { up.Write([]byte{1, byte(off >> 24), byte(off >> 16), byte(off >> 8), byte(off), 0, 0}) }
	e(offsets[replNum])
	e(upOff)
	z = deflate(up.Bytes())
	fmt.Fprintf(out, "%d 0 obj\n<< /Type /XRef /Size %d /W [1 4 2] /Index [%d 1 %d 1] %s /Prev %d /Length %d /Filter /FlateDecode >>\nstream\n", upNum, upNum+1, replNum, upNum, trailerExtra, xrefOff, len(z))
	out.Write(z)
	fmt.Fprintf(out, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", upOff)
	return out.Bytes()
}
