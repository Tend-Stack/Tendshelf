package media

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"time"
)

// Waveform peaks: one byte per half second of audio, the RMS level in dBFS mapped linearly from
// -60..0 dB to 0..255 (0 is silence or anything below -60 dB). The level is RMS and on a dB scale
// because peak values give a flat wall on mastered audiobooks, while RMS shows the pauses of speech.
// A 17 hour book is 122,400 bytes. The bytes are a disposable cache (see internal/mediajobs).
const (
	PeaksFormat  = 1
	PeaksBinMs   = 500
	PeaksRate    = 8000
	PeaksFloorDB = 60

	peaksBase      = 5 * time.Minute
	peaksMaxMs     = 500 * 3600 * 1000 // 3.6 MB of bins; omnibus editions run past 48 hours
	peaksMagic     = "TSPK"
	peaksHeaderLen = 16
	peaksBinSample = PeaksRate * PeaksBinMs / 1000
)

// ErrBadPeaks is returned by DecodePeaks for bytes that are not a peaks file of a known format.
var ErrBadPeaks = errors.New("media: not a peaks file")

// errPeaksTooLong stops ffmpeg when it decodes far more audio than the file's duration promised.
var errPeaksTooLong = errors.New("media: decoded audio is longer than expected")

// Peaks is the loudness of one audio file, one byte per BinMs of audio.
type Peaks struct {
	BinMs  int
	Values []byte
}

// Encode returns the file form: a 16 byte header and the values. Header: "TSPK"; byte 4 format (1);
// byte 5 zero; bytes 6-7 bin length in ms (uint16 LE); bytes 8-11 value count (uint32 LE); byte 12
// the floor in dB; bytes 13-15 zero.
func (p Peaks) Encode() []byte {
	out := make([]byte, peaksHeaderLen, peaksHeaderLen+len(p.Values))
	copy(out, peaksMagic)
	out[4] = PeaksFormat
	binary.LittleEndian.PutUint16(out[6:], uint16(p.BinMs))
	binary.LittleEndian.PutUint32(out[8:], uint32(len(p.Values)))
	out[12] = PeaksFloorDB
	return append(out, p.Values...)
}

// DecodePeaks parses Encode's output. A wrong magic, version or length, or a count of 0, is
// ErrBadPeaks.
func DecodePeaks(b []byte) (Peaks, error) {
	if len(b) < peaksHeaderLen || string(b[:4]) != peaksMagic || b[4] != PeaksFormat {
		return Peaks{}, ErrBadPeaks
	}
	n := binary.LittleEndian.Uint32(b[8:])
	binMs := int(binary.LittleEndian.Uint16(b[6:]))
	if n == 0 || binMs == 0 || uint64(len(b)-peaksHeaderLen) != uint64(n) {
		return Peaks{}, ErrBadPeaks
	}
	return Peaks{BinMs: binMs, Values: append([]byte(nil), b[peaksHeaderLen:]...)}, nil
}

// peaksArgs is the ffmpeg command line: mono 8 kHz signed 16 bit little-endian PCM on stdout, one
// decoder thread so a background job takes one core at most.
func peaksArgs(in string) []string {
	a := append([]string{}, ffmpegCommon...)
	return append(a, "-filter_threads", "1", "-threads", "1", "-i", "file:"+in,
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-ac", "1", "-ar", strconv.Itoa(PeaksRate),
		"-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
}

// peakReducer is an io.Writer that turns s16le PCM into bins as it streams, so memory stays flat.
type peakReducer struct {
	odd     []byte // a trailing half sample carried to the next write
	sum     int64  // sum of squares of the current bin
	n       int    // samples in the current bin
	values  []byte
	maxBins int
	over    bool
}

func newPeakReducer(maxBins int) *peakReducer { return &peakReducer{maxBins: maxBins} }

func (r *peakReducer) Write(p []byte) (int, error) {
	total := len(p)
	if len(r.odd) > 0 {
		p = append(r.odd, p...)
		r.odd = nil
	}
	i := 0
	for ; i+1 < len(p); i += 2 {
		s := int64(int16(binary.LittleEndian.Uint16(p[i:])))
		r.sum += s * s
		r.n++
		if r.n == peaksBinSample {
			if len(r.values) >= r.maxBins {
				r.over = true
				return 0, errPeaksTooLong
			}
			r.values = append(r.values, peakLevel(r.sum, r.n))
			r.sum, r.n = 0, 0
		}
	}
	if i < len(p) {
		r.odd = append([]byte(nil), p[i])
	}
	return total, nil
}

// finish emits the partial last bin, if any.
func (r *peakReducer) finish() {
	if r.n > 0 {
		if len(r.values) >= r.maxBins {
			r.over = true
			return
		}
		r.values = append(r.values, peakLevel(r.sum, r.n))
		r.sum, r.n = 0, 0
	}
}

// peakLevel maps the RMS of n samples with the given sum of squares to 0..255.
func peakLevel(sum int64, n int) byte {
	if sum == 0 || n == 0 {
		return 0
	}
	rms := math.Sqrt(float64(sum) / float64(n))
	db := 20 * math.Log10(rms/32768)
	v := math.Round((db + PeaksFloorDB) / PeaksFloorDB * 255)
	return byte(math.Max(0, math.Min(255, v)))
}

// peaksCapBins is how many bins Peaks keeps before it stops ffmpeg: twice the probed duration plus
// ten minutes, so a wrong probe still finishes, but never past peaksMaxMs, which bounds memory for a
// file that decodes forever.
func peaksCapBins(durationMs int64) int {
	capMs := min(2*durationMs+600000, peaksMaxMs)
	return int(capMs/PeaksBinMs) + 1
}

// Peaks decodes src once and reduces it to loudness bins. durationMs is the probed duration; it
// sizes the deadline and the cap on decoded audio. A protected file is refused before ffmpeg runs.
func (t Tools) Peaks(ctx context.Context, src string, durationMs int64) (Peaks, error) {
	timeout := peaksBase + time.Duration(durationMs/20)*time.Millisecond
	red := newPeakReducer(peaksCapBins(durationMs))
	err := t.ffmpegTo(ctx, src, timeout, peaksArgs(src), red, true)
	if red.over {
		return Peaks{}, newError(CodeFFmpegFailed, errPeaksTooLong.Error(), errPeaksTooLong)
	}
	if err != nil {
		return Peaks{}, err
	}
	red.finish()
	if red.over {
		return Peaks{}, newError(CodeFFmpegFailed, errPeaksTooLong.Error(), errPeaksTooLong)
	}
	if len(red.values) == 0 {
		return Peaks{}, newError(CodeFFmpegFailed, "no audio decoded", nil)
	}
	return Peaks{BinMs: PeaksBinMs, Values: red.values}, nil
}
