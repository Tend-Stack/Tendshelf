package ebook

import (
	"bytes"
	"encoding/xml"
	"io"

	"golang.org/x/text/encoding/htmlindex"
)

// xmlDec is an encoding/xml decoder that counts element depth. encoding/xml never expands DOCTYPE
// entities (a DOCTYPE arrives as a Directive token), so entity attacks cannot grow the output.
type xmlDec struct {
	*xml.Decoder
	depth, max int
}

// newXMLDecoder reads lenient XML: HTML entities known, other charsets decoded, depth limited.
func newXMLDecoder(r io.Reader, lim Limits) *xmlDec {
	d := xml.NewDecoder(r)
	d.Strict = false
	d.Entity = xml.HTMLEntity
	d.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		enc, err := htmlindex.Get(label)
		if err != nil {
			return nil, err
		}
		return enc.NewDecoder().Reader(in), nil
	}
	return &xmlDec{Decoder: d, max: lim.MaxXMLDepth}
}

// newXMLBytes is newXMLDecoder over a byte slice, without a UTF-8 byte-order mark.
func newXMLBytes(b []byte, lim Limits) *xmlDec {
	return newXMLDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))), lim)
}

// Next returns the next token. At end of input it returns io.EOF; a syntax error comes back as a
// damaged refusal; too deep a tree as xml_depth.
func (d *xmlDec) Next() (xml.Token, error) {
	t, err := d.Decoder.Token()
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, damaged("bad_xml")
	}
	switch t.(type) {
	case xml.StartElement:
		d.depth++
		if d.depth > d.max {
			return nil, damaged("xml_depth")
		}
	case xml.EndElement:
		d.depth--
	}
	return t, nil
}

// attr returns an attribute by local name ("" if absent).
func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}
