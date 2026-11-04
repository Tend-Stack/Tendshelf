package ebook

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"github.com/Tend-Stack/tendshelf/internal/ebookfile"
	"io"
	"os"
	"strings"
)

// Covers of ebooks (decision 0018). The picture inside a file is read, sniffed and handed on as bytes; the
// cover job decides what to store. Nothing here writes a file.

// MaxCoverBytes is the largest cover picture Tendshelf takes from an ebook.
const MaxCoverBytes = 20 << 20

// ErrNoCover means the file has no usable cover picture.
var ErrNoCover = ebookfile.ErrNoCover

// SniffImage is ebookfile.SniffImage.
func SniffImage(b []byte) string { return ebookfile.SniffImage(b) }

func checkCover(b []byte) ([]byte, error) {
	if len(b) == 0 || len(b) > MaxCoverBytes || SniffImage(b) == "" {
		return nil, ErrNoCover
	}
	return b, nil
}

// CoverBytes reads the picture an ebook carries as its cover. path is where the file's bytes live (a library
// path, or the stored upload). Formats with no cover to read give ErrNoCover. The file is opened read-only.
func CoverBytes(ctx context.Context, f File, path string, lim Limits) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() > lim.MaxFileBytes {
		return nil, ErrNoCover
	}
	switch f.Ext {
	case "epub":
		zb, err := openZip(fh, fi.Size(), lim)
		if err != nil {
			return nil, ErrNoCover
		}
		return epubCover(zb, lim)
	case "fb2":
		return fb2Cover(io.LimitReader(fh, lim.MaxFileBytes), lim)
	case "fb2.zip":
		zb, err := openZip(fh, fi.Size(), lim)
		if err != nil {
			return nil, ErrNoCover
		}
		for _, n := range zb.Names() {
			if strings.HasSuffix(strings.ToLower(n), ".fb2") {
				data, err := zb.Read(n)
				if err != nil {
					return nil, ErrNoCover
				}
				return fb2Cover(bytes.NewReader(data), lim)
			}
		}
		return nil, ErrNoCover
	case "mobi", "azw3", "azw":
		return mobiCover(fh, fi.Size())
	}
	return nil, ErrNoCover
}

// readImage reads one zip entry as a picture, refusing it before reading when its declared size is over the cap.
func readImage(zb *zipBox, name string) ([]byte, error) {
	zf := zb.find(name)
	if zf == nil || zf.UncompressedSize64 > MaxCoverBytes {
		return nil, ErrNoCover
	}
	data, err := zb.Read(name)
	if err != nil {
		return nil, ErrNoCover
	}
	return checkCover(data)
}

// epubCover tries, in order: the manifest item with properties="cover-image" (EPUB 3), the item named by
// <meta name="cover"> (EPUB 2), then the first picture on a spine page whose name says cover.
func epubCover(zb *zipBox, lim Limits) ([]byte, error) {
	cdata, err := zb.Read("META-INF/container.xml")
	if err != nil {
		return nil, ErrNoCover
	}
	opfPath := ""
	cd := newXMLBytes(cdata, lim)
	for opfPath == "" {
		t, err := cd.Next()
		if err != nil {
			break
		}
		if se, ok := t.(xml.StartElement); ok && se.Name.Local == "rootfile" {
			if p, ok := resolveRef(".", attr(se, "full-path")); ok {
				opfPath = p
			}
		}
	}
	if opfPath == "" {
		return nil, ErrNoCover
	}
	odata, err := zb.Read(opfPath)
	if err != nil {
		return nil, ErrNoCover
	}
	dir := dirOf(opfPath)
	o, err := parseOPF(odata, dir, lim)
	if err != nil {
		return nil, ErrNoCover
	}
	coverID := opfCoverMeta(odata, lim)
	pic := func(it opfItem) ([]byte, error) {
		p, ok := resolveRef(dir, it.Href)
		if !ok {
			return nil, ErrNoCover
		}
		return readImage(zb, p)
	}
	for _, it := range o.Items {
		if hasProp(it.Props, "cover-image") {
			if b, err := pic(it); err == nil {
				return b, nil
			}
		}
	}
	if coverID != "" {
		for _, it := range o.Items {
			if it.ID == coverID && strings.HasPrefix(strings.ToLower(it.Type), "image/") {
				if b, err := pic(it); err == nil {
					return b, nil
				}
			}
		}
	}
	byID := map[string]opfItem{}
	for _, it := range o.Items {
		byID[it.ID] = it
	}
	for _, id := range o.Spine {
		it := byID[id]
		if !strings.Contains(strings.ToLower(it.ID+" "+it.Href), "cover") {
			continue
		}
		page, ok := resolveRef(dir, it.Href)
		if !ok {
			continue
		}
		pdata, err := zb.Read(page)
		if err != nil {
			continue
		}
		if href := firstPageImage(pdata, lim); href != "" {
			if p, ok := resolveRef(dirOf(page), href); ok {
				if b, err := readImage(zb, p); err == nil {
					return b, nil
				}
			}
		}
	}
	return nil, ErrNoCover
}

// opfCoverMeta returns the manifest id named by <meta name="cover" content="...">.
func opfCoverMeta(opf []byte, lim Limits) string {
	d := newXMLBytes(opf, lim)
	for {
		t, err := d.Next()
		if err != nil {
			return ""
		}
		if se, ok := t.(xml.StartElement); ok && se.Name.Local == "meta" && strings.EqualFold(attr(se, "name"), "cover") {
			return attr(se, "content")
		}
	}
}

// firstPageImage returns the href of the first <img src> or SVG <image href> on a cover page.
func firstPageImage(page []byte, lim Limits) string {
	d := newXMLBytes(page, lim)
	for {
		t, err := d.Next()
		if err != nil {
			return ""
		}
		se, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "img":
			if v := attr(se, "src"); v != "" {
				return v
			}
		case "image":
			if v := attr(se, "href"); v != "" {
				return v
			}
		}
	}
}

// fb2Cover reads <coverpage><image href="#id"/></coverpage> and the <binary id="id"> it names.
func fb2Cover(r io.Reader, lim Limits) ([]byte, error) {
	d := newXMLDecoder(r, lim)
	want := ""
	inCover := false
	for {
		t, err := d.Next()
		if err != nil {
			return nil, ErrNoCover
		}
		switch x := t.(type) {
		case xml.StartElement:
			switch x.Name.Local {
			case "coverpage":
				inCover = true
			case "image":
				if inCover && want == "" {
					want = strings.TrimPrefix(fb2Href(x), "#")
				}
			case "binary":
				if want == "" || attr(x, "id") != want {
					continue
				}
				txt, err := readElementText(d)
				if err != nil {
					return nil, ErrNoCover
				}
				if len(txt) > MaxCoverBytes*4/3+16 {
					return nil, ErrNoCover
				}
				b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(txt), ""))
				if err != nil {
					return nil, ErrNoCover
				}
				return checkCover(b)
			}
		case xml.EndElement:
			if x.Name.Local == "coverpage" {
				inCover = false
			}
		}
	}
}

// mobiCover reads the picture record EXTH 201 points at, counted from the first image record. A file whose
// header does not say, or whose record is not a JPEG, PNG or WebP, has no cover here.
func mobiCover(ra io.ReaderAt, size int64) ([]byte, error) {
	db, err := openPalmDB(ra, size)
	if err != nil {
		return nil, ErrNoCover
	}
	r0, err := db.record(0, 1<<20)
	if err != nil {
		return nil, ErrNoCover
	}
	h, err := parseMobiHead(r0)
	if err != nil {
		return nil, ErrNoCover
	}
	vs := h.exth[201]
	if len(vs) == 0 || len(vs[0]) != 4 {
		return nil, ErrNoCover
	}
	first, ok := u32(r0, 108)
	if !ok || first == 0 || first == 0xFFFFFFFF {
		return nil, ErrNoCover
	}
	idx := int64(first) + int64(binary.BigEndian.Uint32(vs[0]))
	if idx <= 0 || idx >= int64(db.count()) {
		return nil, ErrNoCover
	}
	rec, err := db.record(int(idx), MaxCoverBytes)
	if err != nil {
		return nil, ErrNoCover
	}
	return checkCover(rec)
}

// CoverFor reads the cover of a book's text: its default ready file, else any ready one. It returns
// ErrNoCover when the book has no file with a picture.
func (s *Service) CoverFor(ctx context.Context, bookID string) ([]byte, error) {
	files, err := s.store.FilesForBook(ctx, bookID)
	if err != nil {
		return nil, err
	}
	// The default first, then the others in their usual order.
	ordered := files[:0:0]
	for _, f := range files {
		if f.BookDefault {
			ordered = append(ordered, f)
		}
	}
	for _, f := range files {
		if !f.BookDefault {
			ordered = append(ordered, f)
		}
	}
	for _, f := range ordered {
		if f.State != "ready" || f.MissingSince != 0 {
			continue
		}
		p := f.Path
		if f.Origin == "upload" {
			p = s.storedPath(f.SHA256, f.Ext)
		}
		b, err := CoverBytes(ctx, f, p, s.limits)
		if err == nil {
			return b, nil
		}
		if !errors.Is(err, ErrNoCover) && ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, ErrNoCover
}
