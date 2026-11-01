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
)

var kinds = map[string]Kind{
	"m4b": KindAudio, "m4a": KindAudio, "mp4": KindAudio, "mp3": KindAudio, "flac": KindAudio,
	"ogg": KindAudio, "opus": KindAudio, "wav": KindAudio, "aac": KindAudio, "webm": KindAudio,
	"aax": KindProtected, "aaxc": KindProtected, "aa": KindProtected, "m4p": KindProtected,
}

// Classify returns the Kind of a file name or path by its lower-cased extension.
func Classify(name string) Kind {
	return kinds[strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))]
}
