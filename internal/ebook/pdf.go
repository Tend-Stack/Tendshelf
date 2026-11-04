package ebook

import "io"

// parsePDF is replaced whole by package E2.
func parsePDF(ra io.ReaderAt, size int64, lim Limits) (*Doc, error) {
	return nil, refuse(ReasonUnsupported, "not_yet")
}
