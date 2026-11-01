// Method ported from Audiobookshelf (GPL-3.0): server/utils/ffmpegHelpers.js (embedded cover extraction arguments); see NOTICES.md.

package media

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Limits of the ffmpeg operations. A picture goes to a pipe and is capped; everything else is
// written by ffmpeg to a file the caller names.
const (
	pictureTimeout   = 30 * time.Second
	pictureCap       = 20 << 20
	imageToPNGCap    = 40 << 20
	thumbnailTimeout = 30 * time.Second
	compatBase       = 10 * time.Minute
)

// ffmpegCommon starts every ffmpeg command: no stdin, quiet, the file and pipe protocols only, and
// overwrite the output (callers always write to a fresh temporary name).
var ffmpegCommon = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-y"}

// ffmpegArgs joins the common start, the input, and the rest.
func ffmpegArgs(in string, rest ...string) []string {
	a := append([]string{}, ffmpegCommon...)
	a = append(a, "-i", "file:"+in)
	return append(a, rest...)
}

// ffmpeg runs one ffmpeg command. Every media function goes through it, so the checks cannot be
// skipped: a protected input never reaches exec (code drm), a missing ffmpeg is reported by name, a
// deadline is code ffmpeg_timeout, and a failing exit or a flooded stdout is ffmpeg_failed with the
// capped stderr as Detail. If the parent ctx ends first, its cause is returned.
func (t Tools) ffmpeg(ctx context.Context, in string, timeout time.Duration, outCap int, args []string) ([]byte, error) {
	if Classify(in) == KindProtected {
		return nil, newError(CodeDRM, "protected file extension", ErrProtected)
	}
	if t.FFmpeg == "" {
		return nil, newError(CodeFFmpegMissing, "no ffmpeg configured", ErrNotFound)
	}
	if !filepath.IsAbs(in) {
		return nil, fmt.Errorf("media: ffmpeg input path must be absolute: %q", in)
	}
	if t.FFmpegTimeout > 0 {
		timeout = t.FFmpegTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res := runCapped(runCtx, t.FFmpeg, args, t.Env, outCap)
	if res.err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		if runCtx.Err() != nil {
			return nil, newError(CodeFFmpegTimeout, fmt.Sprintf("no answer within %s", timeout), runCtx.Err())
		}
		var exit *exec.ExitError
		if !errors.As(res.err, &exit) && (errors.Is(res.err, fs.ErrNotExist) || errors.Is(res.err, exec.ErrNotFound)) {
			return nil, newError(CodeFFmpegMissing, res.err.Error(), ErrNotFound)
		}
		return nil, newError(CodeFFmpegFailed, describe(res, res.err), res.err)
	}
	if res.capped {
		return nil, newError(CodeFFmpegFailed, fmt.Sprintf("ffmpeg wrote more than %d bytes", outCap), nil)
	}
	return res.stdout, nil
}

// ExtractPicture reads one attached picture (an embedded cover) out of an audio file. mjpeg and png
// pictures keep their bytes (stream copy); any other codec is re-encoded to PNG. ext is "jpg" or
// "png". The audio file is only read.
func (t Tools) ExtractPicture(ctx context.Context, audioPath string, pic Picture) (data []byte, ext string, err error) {
	codec, ext := "png", "png"
	switch pic.Codec {
	case "mjpeg":
		codec, ext = "copy", "jpg"
	case "png":
		codec = "copy"
	}
	args := ffmpegArgs(audioPath, "-map", "0:"+strconv.Itoa(pic.Index), "-frames:v", "1", "-c:v", codec, "-f", "image2pipe", "pipe:1")
	data, err = t.ffmpeg(ctx, audioPath, pictureTimeout, pictureCap, args)
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", newError(CodeFFmpegFailed, "ffmpeg wrote no picture", nil)
	}
	return data, ext, nil
}

// ImageToPNG converts an image file ffmpeg can read (a folder cover in WebP, say) to PNG.
func (t Tools) ImageToPNG(ctx context.Context, imgPath string) ([]byte, error) {
	args := ffmpegArgs(imgPath, "-map", "0:v:0", "-frames:v", "1", "-c:v", "png", "-f", "image2pipe", "pipe:1")
	data, err := t.ffmpeg(ctx, imgPath, pictureTimeout, imageToPNGCap, args)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, newError(CodeFFmpegFailed, "ffmpeg wrote no picture", nil)
	}
	return data, nil
}

// Thumbnail writes a JPEG of src to dst, fitted inside box by box pixels and never enlarged. dst is
// written by ffmpeg directly, so callers pass a temporary name and rename it afterwards.
func (t Tools) Thumbnail(ctx context.Context, src, dst string, box int) error {
	if !filepath.IsAbs(dst) {
		return fmt.Errorf("media: thumbnail path must be absolute: %q", dst)
	}
	b := strconv.Itoa(box)
	vf := "scale=w='min(" + b + ",iw)':h='min(" + b + ",ih)':force_original_aspect_ratio=decrease:flags=lanczos"
	args := ffmpegArgs(src, "-map", "0:v:0", "-frames:v", "1", "-vf", vf, "-c:v", "mjpeg", "-q:v", "3", "-f", "image2", "-update", "1", "file:"+dst)
	_, err := t.ffmpeg(ctx, src, thumbnailTimeout, stdoutCap, args)
	return err
}
