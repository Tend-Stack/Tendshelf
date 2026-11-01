// Package wav writes small synthetic WAV files for tests. The audio is a sine tone that browsers
// play as it is, so no ffmpeg is needed to make test audio.
package wav

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
)

const (
	sampleRate = 8000
	amplitude  = 8000
	headerSize = 44
)

// Bytes returns a RIFF/WAVE file of ms milliseconds: PCM, 16-bit, mono, 8000 Hz, a sine at hz.
func Bytes(ms, hz int) []byte {
	if ms < 0 {
		ms = 0
	}
	samples := ms * sampleRate / 1000
	dataLen := samples * 2
	b := make([]byte, headerSize+dataLen)
	le := binary.LittleEndian
	copy(b[0:], "RIFF")
	le.PutUint32(b[4:], uint32(headerSize-8+dataLen))
	copy(b[8:], "WAVE")
	copy(b[12:], "fmt ")
	le.PutUint32(b[16:], 16) // fmt chunk size
	le.PutUint16(b[20:], 1)  // PCM
	le.PutUint16(b[22:], 1)  // mono
	le.PutUint32(b[24:], sampleRate)
	le.PutUint32(b[28:], sampleRate*2) // byte rate
	le.PutUint16(b[32:], 2)            // block align
	le.PutUint16(b[34:], 16)           // bits per sample
	copy(b[36:], "data")
	le.PutUint32(b[40:], uint32(dataLen))
	for i := 0; i < samples; i++ {
		v := int16(amplitude * math.Sin(2*math.Pi*float64(hz)*float64(i)/sampleRate))
		le.PutUint16(b[headerSize+2*i:], uint16(v))
	}
	return b
}

// Write writes Bytes(ms, hz) to path, creating its folder.
func Write(path string, ms, hz int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, Bytes(ms, hz), 0o644)
}
