// Package ebook reads ebook files into one canonical, deterministic text document
// (docs/reader.md). Files are only ever opened read-only.
package ebook

import "time"

// Limits bound every parse so a hostile file cannot exhaust memory or time.
type Limits struct {
	MaxFileBytes         int64
	MaxEntries           int
	MaxTotalUncompressed int64
	MaxEntryBytes        int64
	// MaxRatio applies to entries whose uncompressed size is over RatioFloor, so small, highly
	// compressible XML cannot trip it.
	MaxRatio     int64
	ParseTimeout time.Duration
	MaxWords     int
	MaxXMLDepth  int
	MaxBlocks    int
}

// RatioFloor is the uncompressed size under which the compression ratio is not checked.
const RatioFloor = 1 << 20

// DefaultLimits are the limits of the plan (docs/plans/m5-r1-ebooks.md).
func DefaultLimits() Limits {
	return Limits{
		MaxFileBytes:         200 << 20,
		MaxEntries:           10000,
		MaxTotalUncompressed: 1 << 30,
		MaxEntryBytes:        64 << 20,
		MaxRatio:             100,
		ParseTimeout:         60 * time.Second,
		MaxWords:             2000000,
		MaxXMLDepth:          256,
		MaxBlocks:            500000,
	}
}
