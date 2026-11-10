// Package hls reads HTTP Live Streaming radio (RFC 8216): it parses playlists and turns the audio
// segments (MPEG-TS, packed audio, fragmented MP4) into one plain ADTS or MPEG audio stream that a
// browser or the Android player can play as an ordinary live radio stream (decision 0039).
//
// This file set holds only the parsers. They take bytes, never touch the network or ffmpeg, are
// bounded in size, count and loops, and never panic on any input (the fuzz targets check that).
package hls

import "errors"

// Errors a caller can tell apart. The wording is for logs; the radio package maps them to the words
// a listener sees.
var (
	ErrNotPlaylist = errors.New("hls: not a playlist")
	ErrLocked      = errors.New("hls: stream is encrypted")
	ErrVideo       = errors.New("hls: stream has no audio-only variant")
	ErrCodec       = errors.New("hls: unsupported audio codec")
	ErrUnsupported = errors.New("hls: unsupported stream feature")
	ErrChanged     = errors.New("hls: audio format changed")
	ErrTooLarge    = errors.New("hls: too large")
)

// Bounds that hold everywhere (plan invariant 6).
const (
	MaxSegment  = 4 << 20 // one segment
	MaxInit     = 1 << 20 // one fMP4 initialisation section
	MaxSegments = 2000    // segments in one media playlist
	StartBack   = 3       // a live session starts this many segments before the end
	MaxFailures = 3       // consecutive failures that end a session

	maxPlaylist = 256 << 10 // one playlist body
	maxLine     = 8 << 10   // one playlist line
)

// Format is what the listener's player needs to know about the audio. Codec is "aac" or "mp3".
type Format struct {
	Codec      string
	SampleRate int
	Channels   int
}

// ContentType is the answer type for a relayed stream: only these two are ever sent.
func (f Format) ContentType() string {
	if f.Codec == "mp3" {
		return "audio/mpeg"
	}
	return "audio/aac"
}
