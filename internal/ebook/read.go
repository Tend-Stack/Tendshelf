package ebook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

// parseHook is called with the format just before parsing; tests use it to inject a panic or a delay.
var parseHook func(format string)

// ctxReader stops a long hash when the deadline passes.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

type readResult struct {
	doc *Doc
	err error
}

// Read parses the ebook at path into a canonical document. The file is opened read-only. ext is the
// extension as ExtOf reports it. The whole read runs under lim.ParseTimeout and recovers from panics.
// Errors are *Refusal.
func Read(ctx context.Context, path, ext string, lim Limits) (*Doc, error) {
	ctx, cancel := context.WithTimeout(ctx, lim.ParseTimeout)
	defer cancel()
	ch := make(chan readResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- readResult{err: damaged("parser_panic")}
			}
		}()
		doc, err := readFile(ctx, path, ext, lim)
		ch <- readResult{doc, err}
	}()
	select {
	case res := <-ch:
		return res.doc, asRefusal(res.err)
	case <-ctx.Done():
		return nil, damaged("timeout")
	}
}

// asRefusal turns any other error into a damaged refusal, so callers see one error type.
func asRefusal(err error) error {
	if err == nil {
		return nil
	}
	var r *Refusal
	if errors.As(err, &r) {
		return r
	}
	return damaged("read_error")
}

func readFile(ctx context.Context, path, ext string, lim Limits) (*Doc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() > lim.MaxFileBytes {
		return nil, refuse(ReasonTooLarge, "file_too_big")
	}
	h := sha256.New()
	if _, err := io.Copy(h, ctxReader{ctx, io.NewSectionReader(f, 0, fi.Size())}); err != nil {
		return nil, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	sn, err := sniff(f, fi.Size(), ext, lim)
	if err != nil {
		return nil, err
	}
	if parseHook != nil {
		parseHook(sn.format)
	}
	if err := ctx.Err(); err != nil {
		return nil, damaged("timeout")
	}
	doc, err := parseSniffed(f, fi.Size(), sn, lim)
	if err != nil {
		return nil, err
	}
	doc.SourceSHA256 = sum
	return doc, nil
}

func readAllCapped(ra io.ReaderAt, size int64, lim Limits) ([]byte, error) {
	if size > lim.MaxEntryBytes {
		return nil, damaged("entry_too_big")
	}
	return io.ReadAll(io.NewSectionReader(ra, 0, size))
}

func parseSniffed(ra io.ReaderAt, size int64, sn *sniffed, lim Limits) (*Doc, error) {
	switch sn.format {
	case "epub":
		return parseEPUB(sn.zb, lim)
	case "docx":
		return parseDOCX(sn.zb, lim)
	case "fb2":
		if sn.zb != nil {
			var name string
			for _, n := range sn.zb.Names() {
				if strings.HasSuffix(strings.ToLower(n), ".fb2") {
					if name != "" {
						return nil, refuse(ReasonUnsupported, "fb2_zip_entries")
					}
					name = n
				}
			}
			if name == "" {
				return nil, refuse(ReasonUnsupported, "fb2_zip_entries")
			}
			data, err := sn.zb.Read(name)
			if err != nil {
				return nil, err
			}
			return parseFB2(data, lim)
		}
		data, err := readAllCapped(ra, size, lim)
		if err != nil {
			return nil, err
		}
		return parseFB2(data, lim)
	case "pdf":
		return parsePDF(ra, size, lim)
	case "mobi", "azw3":
		return parseMOBI(ra, size, sn.format, lim)
	case "txt", "md", "html":
		data, err := readAllCapped(ra, size, lim)
		if err != nil {
			return nil, err
		}
		switch sn.format {
		case "html":
			return parseHTMLFile(data, lim)
		default:
			return parseText(data, sn.format == "md", lim)
		}
	}
	return nil, refuse(ReasonUnsupported, "format")
}
