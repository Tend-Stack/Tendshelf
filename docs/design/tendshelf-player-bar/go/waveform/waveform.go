// Package waveform computes the loudness values behind the player bar's chapter waveform.
//
// It decodes audio with ffmpeg (already a Tendshelf dependency for scanning), mixes to mono
// at 8 kHz, and stores one byte per 1/Rate second: the RMS loudness of that slice, scaled so
// the book's 95th percentile is 255 and square-rooted so quiet speech still shows.
//
// Run it once per book in the background after a scan and save the result next to the
// book's other derived data. For a 17-hour book at Rate 5 that's about 300 KB.
package waveform

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sort"
	"strconv"
)

// Rate is how many values Peaks returns per second of audio.
const Rate = 5

const sampleRate = 8000

// Peaks returns loudness values (0–255), Rate per second, for one audio file.
// For a book made of several files, call it per file in book order and append the results.
func Peaks(ctx context.Context, path string) ([]byte, error) {
	rms, err := decodeRMS(ctx, path)
	if err != nil {
		return nil, err
	}
	return Normalize(rms), nil
}

// PeaksForFiles computes one continuous series for a multi-file book, normalised together
// so loudness is comparable across chapters.
func PeaksForFiles(ctx context.Context, paths []string) ([]byte, error) {
	var all []float64
	for _, p := range paths {
		rms, err := decodeRMS(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		all = append(all, rms...)
	}
	return Normalize(all), nil
}

func decodeRMS(ctx context.Context, path string) ([]float64, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin",
		"-i", path, "-vn", "-ac", "1", "-ar", strconv.Itoa(sampleRate), "-f", "s16le", "-")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	window := sampleRate / Rate
	buf := make([]byte, 2*window)
	r := bufio.NewReaderSize(out, 1<<16)
	var rms []float64
	for {
		n, readErr := io.ReadFull(r, buf)
		if samples := n / 2; samples > 0 {
			var sum float64
			for i := 0; i < samples; i++ {
				s := float64(int16(binary.LittleEndian.Uint16(buf[2*i:]))) / 32768
				sum += s * s
			}
			rms = append(rms, math.Sqrt(sum/float64(samples)))
		}
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
		if readErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, readErr
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	return rms, nil
}

// Normalize scales RMS values to bytes: the 95th percentile maps to 255, values above it clip,
// and a square root lifts quiet passages so pauses read as dips rather than gaps.
func Normalize(v []float64) []byte {
	out := make([]byte, len(v))
	if len(v) == 0 {
		return out
	}
	sorted := append([]float64(nil), v...)
	sort.Float64s(sorted)
	ref := sorted[int(float64(len(sorted)-1)*0.95)]
	if ref <= 0 {
		return out
	}
	for i, x := range v {
		y := math.Sqrt(math.Min(x/ref, 1))
		out[i] = byte(math.Round(y * 255))
	}
	return out
}

// Slice returns the values between two book times in seconds, for one chapter.
func Slice(peaks []byte, from, to float64) []byte {
	a := int(math.Floor(from * Rate))
	b := int(math.Ceil(to * Rate))
	if a < 0 {
		a = 0
	}
	if b > len(peaks) {
		b = len(peaks)
	}
	if a >= b {
		return nil
	}
	return peaks[a:b]
}
