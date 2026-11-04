package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// pcmBase is the fixed part of the PCM decode deadline; a tenth of the requested length is added.
const pcmBase = 60 * time.Second

var errPCMTooLong = errors.New("ffmpeg wrote more audio than requested")

// capWriter passes bytes to w until limit is reached, then drops the rest and remembers it.
type capWriter struct {
	w     io.Writer
	limit int64
	n     int64
	over  bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	room := c.limit - c.n
	if room < int64(len(p)) {
		c.over = true
		if room <= 0 {
			return len(p), nil
		}
		if _, err := c.w.Write(p[:room]); err != nil {
			return 0, err
		}
		c.n += room
		return len(p), nil
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// pcmSeconds formats milliseconds as ffmpeg seconds with three decimals.
func pcmSeconds(ms int64) string { return fmt.Sprintf("%d.%03d", ms/1000, ms%1000) }

// pcm16kArgs is the ffmpeg command line: mono 16 kHz signed 16 bit little-endian PCM on stdout for the
// span [fromMs, fromMs+durMs) of the first audio stream.
func pcm16kArgs(src string, fromMs, durMs int64) []string {
	a := append([]string{}, ffmpegCommon...)
	return append(a, "-ss", pcmSeconds(fromMs), "-i", "file:"+src, "-t", pcmSeconds(durMs),
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
}

// PCM16k decodes durMs of src from fromMs to mono 16 kHz s16le samples and streams them to w, at a lower
// priority. The deadline is 60 s plus a tenth of durMs; output past durMs*32 bytes plus 1 MiB is an
// error. A protected file is refused before ffmpeg runs. The audio file is only read.
func (t Tools) PCM16k(ctx context.Context, src string, fromMs, durMs int64, w io.Writer) error {
	if fromMs < 0 || durMs <= 0 {
		return fmt.Errorf("media: bad PCM span %d+%d ms", fromMs, durMs)
	}
	cw := &capWriter{w: w, limit: durMs*32 + 1<<20}
	timeout := pcmBase + time.Duration(durMs/10)*time.Millisecond
	if err := t.ffmpegTo(ctx, src, timeout, pcm16kArgs(src, fromMs, durMs), cw, true); err != nil {
		return err
	}
	if cw.over {
		return newError(CodeFFmpegFailed, errPCMTooLong.Error(), errPCMTooLong)
	}
	return nil
}
