package ebook

import (
	"encoding/xml"
	"io"
	"strings"
)

// Copy protection is detected before any content is parsed. A protected file is refused with reason
// "drm" and never decrypted, decoded or worked around.

var oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// allowedEncryption are the EPUB font-obfuscation algorithms: they change fonts only, never text.
var allowedEncryption = map[string]bool{
	"http://www.idpf.org/2008/embedding": true,
	"http://ns.adobe.com/pdf/enc#RC":     true,
}

// checkEPUBDRM looks for the markers of Adobe ADEPT, Apple FairPlay, Readium LCP and for any
// encryption of content in META-INF.
func checkEPUBDRM(zb *zipBox, lim Limits) error {
	if zb.Has("META-INF/rights.xml") {
		return refuse(ReasonDRM, "adept")
	}
	if zb.Has("META-INF/license.lcpl") {
		return refuse(ReasonDRM, "lcp")
	}
	for _, n := range zb.Names() {
		if strings.EqualFold(n[strings.LastIndexByte(n, '/')+1:], "sinf.xml") {
			return refuse(ReasonDRM, "apple")
		}
	}
	if zb.Has("META-INF/encryption.xml") {
		data, err := zb.Read("META-INF/encryption.xml")
		if err != nil {
			return err
		}
		d := newXMLBytes(data, lim)
		for {
			t, err := d.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return refuse(ReasonDRM, "epub_encryption")
			}
			if se, ok := t.(xml.StartElement); ok && se.Name.Local == "EncryptionMethod" {
				if !allowedEncryption[attr(se, "Algorithm")] {
					return refuse(ReasonDRM, "epub_encryption")
				}
			}
		}
	}
	return nil
}
