package media

import "errors"

var (
	// ErrNotFound means ffprobe or ffmpeg could not be located; see NotFoundError.
	ErrNotFound = errors.New("media: tool not found")
	// ErrProtected means the file is copy-protected. Tendshelf never opens, decodes or remuxes it.
	ErrProtected = errors.New("media: copy-protected file")
	// ErrNotRegular means the path is a directory, device, socket or pipe, so it was not opened.
	ErrNotRegular = errors.New("media: not a regular file")
)

// Error codes of Error.Code. Code is stable (stored in job errors and tested); Text is the plain
// sentence a listener or the owner may see; Detail is for the server log only.
const (
	CodeDRM               = "drm"
	CodeFFprobeMissing    = "ffprobe_missing"
	CodeFileMissing       = "file_missing"
	CodeFileUnreadable    = "file_unreadable"
	CodeNotAudio          = "not_audio"
	CodeProbeTimeout      = "probe_timeout"
	CodeProbeFailed       = "probe_failed"
	CodeProbeOutputTooBig = "probe_output_too_large"
	CodeFFmpegMissing     = "ffmpeg_missing"
	CodeFFmpegFailed      = "ffmpeg_failed"
	CodeFFmpegTimeout     = "ffmpeg_timeout"
	CodeImageTooLarge     = "image_too_large"
	CodeCompatNotNeeded   = "compat_not_needed"
)

var errorTexts = map[string]string{
	CodeDRM:               "This file is copy-protected, so Tendshelf will not open it.",
	CodeFFprobeMissing:    "Tendshelf cannot find ffprobe. Install FFmpeg or set its location.",
	CodeFileMissing:       "This file is no longer in the folder.",
	CodeFileUnreadable:    "Tendshelf is not allowed to read this file.",
	CodeNotAudio:          "This file is damaged or is not an audio file.",
	CodeProbeTimeout:      "Reading this file took too long. It may be on a slow or disconnected drive.",
	CodeProbeFailed:       "Tendshelf could not read this file.",
	CodeProbeOutputTooBig: "Tendshelf could not read this file.",
	CodeFFmpegMissing:     "Tendshelf cannot find ffmpeg. Install FFmpeg or set its location.",
	CodeFFmpegFailed:      "Tendshelf could not convert this file.",
	CodeFFmpegTimeout:     "Converting this file took too long.",
	CodeImageTooLarge:     "This picture is too large to use as a cover.",
	CodeCompatNotNeeded:   "Browsers can play this file as it is.",
}

// ErrorText returns the plain sentence for an error code, or "" for an unknown code.
func ErrorText(code string) string { return errorTexts[code] }

// Error is a failure of the media runner with a plain-words Text for the interface and a Detail
// (capped ffprobe stderr or the cause) that belongs in the log only.
type Error struct {
	Code   string
	Text   string
	Detail string
	Err    error
}

func (e *Error) Error() string {
	d := e.Detail
	if d == "" {
		d = e.Text
	}
	return "media: " + e.Code + ": " + d
}

func (e *Error) Unwrap() error { return e.Err }

func newError(code, detail string, cause error) *Error {
	return &Error{Code: code, Text: errorTexts[code], Detail: detail, Err: cause}
}

// NotFoundError says which tool is missing and where Tendshelf looked.
type NotFoundError struct {
	Tool  string // "ffprobe" or "ffmpeg"
	Tried string // the configured value, or "PATH"
}

func (e *NotFoundError) Error() string {
	return "media: " + e.Tool + " not found (looked in " + e.Tried + ")"
}

func (e *NotFoundError) Is(target error) bool { return target == ErrNotFound }
