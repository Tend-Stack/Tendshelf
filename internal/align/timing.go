package align

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// TimingV is the version of the timing file format "tendshelf-timing-v1".
const TimingV = 1

// Word marks (Timing.WM).
const (
	WordTimed    = 0 // measured by the aligner
	WordBetween  = 1 // a wildcard between two timed neighbours; never a sentence edge
	WordNotTimed = 2
)

// maxTimingBytes bounds what ReadTiming decompresses.
const maxTimingBytes = 64 << 20

// Timing is one chapter's result as flat arrays. Times are milliseconds on the book's timeline, -1 for none.
// Per sentence: SID, SS (start), SE (end), SC (confidence, -1 for none), Gap (null or the reason). Per source
// word: WS (sentence index), WO0/WO1 (UTF-16 offsets in its block), W0/W1 (times) and WM (a Word mark).
type Timing struct {
	V         int       `json:"v"`
	Alignment string    `json:"alignment,omitempty"`
	Chapter   int       `json:"chapter"`
	SID       []string  `json:"sid"`
	SS        []int64   `json:"ss"`
	SE        []int64   `json:"se"`
	SC        []float64 `json:"sc"`
	Gap       []*string `json:"gap"`
	WS        []int     `json:"ws"`
	WO0       []int     `json:"wo0"`
	WO1       []int     `json:"wo1"`
	W0        []int64   `json:"w0"`
	W1        []int64   `json:"w1"`
	WM        []int     `json:"wm"`
}

// TimingPath is where a chapter's file lives.
func TimingPath(dir, key string) string { return filepath.Join(dir, key+".json.gz") }

func (t *Timing) valid() error {
	n, w := len(t.SID), len(t.WS)
	if len(t.SS) != n || len(t.SE) != n || len(t.SC) != n || len(t.Gap) != n {
		return errors.New("align: timing sentence arrays differ in length")
	}
	if len(t.WO0) != w || len(t.WO1) != w || len(t.W0) != w || len(t.W1) != w || len(t.WM) != w {
		return errors.New("align: timing word arrays differ in length")
	}
	return nil
}

// WriteTiming stores t as dir/<key>.json.gz: gzip with an empty header (so equal input gives equal bytes),
// through a temporary file, a sync and a rename. A file that already has the name is replaced.
func WriteTiming(dir, key string, t *Timing) error {
	if !cleanName(key) {
		return errors.New("align: bad timing key")
	}
	if err := t.valid(); err != nil {
		return err
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err = zw.Write(raw); err == nil {
		err = zw.Close()
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".timing-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(buf.Bytes())
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = os.Rename(name, TimingPath(dir, key))
	}
	if err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("align: write timing: %w", err)
	}
	return nil
}

// ReadTiming loads a chapter file; a missing file returns an error satisfying errors.Is(err, fs.ErrNotExist).
func ReadTiming(dir, key string) (*Timing, error) {
	if !cleanName(key) {
		return nil, errors.New("align: bad timing key")
	}
	f, err := os.Open(TimingPath(dir, key))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("align: timing file: %w", err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, maxTimingBytes+1))
	if err != nil || len(raw) > maxTimingBytes {
		return nil, errors.New("align: timing file damaged or too large")
	}
	var t Timing
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("align: timing file: %w", err)
	}
	if t.V != TimingV {
		return nil, errors.New("align: unknown timing version")
	}
	if err := t.valid(); err != nil {
		return nil, err
	}
	return &t, nil
}

// OpenTiming opens a chapter file for serving as it is (gzip bytes). The key is checked like ReadTiming's;
// a missing file satisfies errors.Is(err, fs.ErrNotExist).
func OpenTiming(dir, key string) (*os.File, error) {
	if !cleanName(key) {
		return nil, errors.New("align: bad timing key")
	}
	return os.Open(TimingPath(dir, key))
}
