package ebook

import "io"

// parseMOBI (MOBI and AZW3) is replaced whole by package E2.
func parseMOBI(ra io.ReaderAt, size int64, format string, lim Limits) (*Doc, error) {
	return nil, refuse(ReasonUnsupported, "not_yet")
}
