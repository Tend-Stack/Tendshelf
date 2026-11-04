package ebook

import (
	"bytes"
	"io"
	"strings"
)

// sniffed is what the first bytes and the extension say a file is.
type sniffed struct {
	format string
	zb     *zipBox // set for zip-based formats
}

// sniff decides the format from magic bytes first, then the extension. It refuses copy-protected
// files here, before any content is parsed.
func sniff(ra io.ReaderAt, size int64, ext string, lim Limits) (*sniffed, error) {
	if ext == "kfx" || ext == "kfx-zip" {
		return nil, refuse(ReasonDRM, "kfx")
	}
	head := make([]byte, 68)
	n, _ := ra.ReadAt(head, 0)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		zb, err := openZip(ra, size, lim)
		if err != nil {
			return nil, err
		}
		switch {
		case zb.Has("mimetype") && zb.Has("META-INF/container.xml"), zb.Has("META-INF/container.xml"):
			if err := checkEPUBDRM(zb, lim); err != nil {
				return nil, err
			}
			return &sniffed{format: "epub", zb: zb}, nil
		case zb.Has("word/document.xml"):
			return &sniffed{format: "docx", zb: zb}, nil
		}
		if ext == "fb2.zip" || ext == "fb2" {
			return &sniffed{format: "fb2", zb: zb}, nil
		}
		return nil, refuse(ReasonUnsupported, "zip_unknown")
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return &sniffed{format: "pdf"}, nil
	case len(head) >= 68 && string(head[60:68]) == "BOOKMOBI":
		if ext == "azw3" {
			return &sniffed{format: "azw3"}, nil
		}
		return &sniffed{format: "mobi"}, nil
	case bytes.HasPrefix(head, oleMagic):
		if ext == "docx" {
			return nil, refuse(ReasonDRM, "ole_office")
		}
		return nil, refuse(ReasonUnsupported, "ole")
	case bytes.HasPrefix(head, []byte("CONT")), bytes.HasPrefix(head, []byte("\xeaDRMION\xee")):
		return nil, refuse(ReasonDRM, "kfx")
	}
	switch ext {
	case "txt":
		return &sniffed{format: "txt"}, nil
	case "md", "markdown":
		return &sniffed{format: "md"}, nil
	case "html", "htm", "xhtml":
		return &sniffed{format: "html"}, nil
	case "fb2":
		return &sniffed{format: "fb2"}, nil
	case "epub", "docx", "pdf", "mobi", "azw3", "azw", "fb2.zip":
		return nil, damaged("bad_magic")
	}
	return nil, refuse(ReasonUnsupported, "ext_"+strings.ReplaceAll(ext, ".", "_"))
}
