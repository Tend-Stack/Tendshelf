package media

import (
	"path/filepath"
	"strings"
)

// Kind says what Tendshelf does with a file, judged by its extension alone.
type Kind int

const (
	// KindOther is any file Tendshelf does not treat as audio (covers, text, junk).
	KindOther Kind = iota
	// KindAudio is an extension Tendshelf scans and probes.
	KindAudio
	// KindProtected is a copy-protected audio format. It is listed as refused and never opened.
	KindProtected
	// KindEbook is an extension the scanner records as a text edition (internal/ebook). It is never
	// probed and never becomes part of a book.
	KindEbook
	// KindLooseEbook is a PDF. It is an ebook only when its directory holds no audio (a booklet in an
	// audiobook's folder stays ignored), so the scanner decides; everything else treats it like KindOther.
	KindLooseEbook
)

var kinds = map[string]Kind{
	"m4b": KindAudio, "m4a": KindAudio, "mp4": KindAudio, "mp3": KindAudio, "flac": KindAudio,
	"ogg": KindAudio, "opus": KindAudio, "wav": KindAudio, "aac": KindAudio, "webm": KindAudio,
	"aax": KindProtected, "aaxc": KindProtected, "aa": KindProtected, "m4p": KindProtected,
	"epub": KindEbook, "mobi": KindEbook, "azw3": KindEbook, "azw": KindEbook, "fb2": KindEbook,
	"kfx": KindEbook, "kfx-zip": KindEbook, "pdf": KindLooseEbook,
}

// Classify returns the Kind of a file name or path by its lower-cased extension.
func Classify(name string) Kind {
	return kinds[strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))]
}
