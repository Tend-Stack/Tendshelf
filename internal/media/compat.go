package media

import (
	"context"
	"path/filepath"
	"time"
)

// CompatRecipe is the version of the compatibility-copy recipe. It is part of the cache file name,
// so changing the arguments below means bumping it: old copies then age out of the cache.
const CompatRecipe = 1

// MayNeedCompat reports whether some browser may be unable to play a file of this container and
// codec as it is. Only the formats every current browser plays are exempt: AAC in MP4, MP3, FLAC
// and 16-bit PCM WAV. Opus and Vorbis (Safari before 17.4), ALAC, WMA, AC-3 and the rest may need
// a copy. The player decides whether to ask for one, from canPlayType.
func MayNeedCompat(container, codec string) bool {
	switch {
	case (container == "mp4" || container == "aac") && codec == "aac":
		return false
	case container == "mp3" && codec == "mp3":
		return false
	case container == "flac" && codec == "flac":
		return false
	case container == "wav" && codec == "pcm_s16le":
		return false
	}
	return true
}

// compatArgs are the ffmpeg arguments after the input: the first audio stream only, no metadata or
// chapters, AAC-LC 128 kbit/s (stream-copied when the source already is AAC), faststart MP4.
func compatArgs(src, dst, codec string) []string {
	audio := []string{"-c:a", "aac", "-profile:a", "aac_low", "-b:a", "128k"}
	if codec == "aac" {
		audio = []string{"-c:a", "copy"}
	}
	rest := []string{"-map", "0:a:0", "-vn", "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1"}
	rest = append(rest, audio...)
	rest = append(rest, "-movflags", "+faststart", "-f", "mp4", "file:"+dst)
	return ffmpegArgs(src, rest...)
}

// Compat writes a browser-friendly copy of src to dst (an .m4a in an MP4 container). The deadline is
// ten minutes plus a tenth of the audio's length. dst is written by ffmpeg directly, so callers pass
// a temporary name and rename it after checking the result. src is only read.
func (t Tools) Compat(ctx context.Context, src, dst string, codec string, durationMs int64) error {
	if !filepath.IsAbs(dst) {
		return newError(CodeFFmpegFailed, "compat output path must be absolute: "+dst, nil)
	}
	timeout := compatBase + time.Duration(durationMs/10)*time.Millisecond
	_, err := t.ffmpeg(ctx, src, timeout, stdoutCap, compatArgs(src, dst, codec))
	return err
}
