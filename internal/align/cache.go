package align

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// emMagic starts every .em file: "TSEM", format 0, version 1.
var emMagic = []byte{'T', 'S', 'E', 'M', 0, 1}

const emHeader = 6 + 4 + 2 + 2 + 4

// Quantise turns a log-probability into one byte: steps of 0.1 nats below zero, clamped at 25.5.
func Quantise(logp float32) uint8 {
	q := math.Round(float64(-logp) / QStep)
	if q < 0 || math.IsNaN(q) {
		return 0
	}
	if q > 255 {
		return 255
	}
	return uint8(q)
}

// Dequantise is the log-probability a byte stands for.
func Dequantise(q uint8) float32 { return -float32(q) * QStep }

// Em is one cached window of emissions: Frames rows of Vocab bytes.
type Em struct {
	Frames, Vocab, FrameMs int
	Data                   []uint8
}

// QuantiseMatrix turns a matrix into an Em.
func QuantiseMatrix(m Matrix) Em {
	e := Em{Frames: m.T, Vocab: m.V, FrameMs: FrameMs, Data: make([]uint8, len(m.LogP))}
	for i, x := range m.LogP {
		e.Data[i] = Quantise(x)
	}
	return e
}

// Matrix expands an Em into log-probabilities.
func (e Em) Matrix() Matrix {
	m := Matrix{T: e.Frames, V: e.Vocab, LogP: make([]float32, len(e.Data))}
	for i, q := range e.Data {
		m.LogP[i] = Dequantise(q)
	}
	return m
}

// Cache is the emissions cache: <Root>/<fileHash>/<modelSHA>/<window:05d>.em. It is reconstructible, so a
// damaged file is deleted and reported as missing.
type Cache struct{ Root string }

func cleanName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`+"\x00")
}

// Path is the file of one window, or "" when a name is not a plain file name.
func (c Cache) Path(fileHash, modelSHA string, window int) string {
	if !cleanName(fileHash) || !cleanName(modelSHA) || window < 0 {
		return ""
	}
	return filepath.Join(c.Root, fileHash, modelSHA, fmt.Sprintf("%05d.em", window))
}

// Has reports whether the window is cached (without checking its checksum).
func (c Cache) Has(fileHash, modelSHA string, window int) bool {
	p := c.Path(fileHash, modelSHA, window)
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// Read returns the window. A missing file, a bad magic or a bad checksum gives ok=false (and the bad file is
// removed).
func (c Cache) Read(fileHash, modelSHA string, window int) (Em, bool, error) {
	p := c.Path(fileHash, modelSHA, window)
	if p == "" {
		return Em{}, false, errors.New("align: bad cache name")
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Em{}, false, nil
	}
	if err != nil {
		return Em{}, false, err
	}
	e, ok := decodeEm(b)
	if !ok {
		_ = os.Remove(p)
		return Em{}, false, nil
	}
	return e, true, nil
}

func decodeEm(b []byte) (Em, bool) {
	if len(b) < emHeader || !bytes.Equal(b[:6], emMagic) {
		return Em{}, false
	}
	frames := int(binary.LittleEndian.Uint32(b[6:10]))
	vocab := int(binary.LittleEndian.Uint16(b[10:12]))
	frameMs := int(binary.LittleEndian.Uint16(b[12:14]))
	sum := binary.LittleEndian.Uint32(b[14:18])
	payload := b[emHeader:]
	if vocab == 0 || len(payload) != frames*vocab || crc32.ChecksumIEEE(payload) != sum {
		return Em{}, false
	}
	return Em{Frames: frames, Vocab: vocab, FrameMs: frameMs, Data: append([]uint8(nil), payload...)}, true
}

// Write stores the window through a temporary file, a sync and a rename.
func (c Cache) Write(fileHash, modelSHA string, window int, e Em) error {
	p := c.Path(fileHash, modelSHA, window)
	if p == "" {
		return errors.New("align: bad cache name")
	}
	if len(e.Data) != e.Frames*e.Vocab || e.Vocab == 0 || e.Vocab > math.MaxUint16 || e.FrameMs > math.MaxUint16 {
		return errors.New("align: bad emissions shape")
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	head := make([]byte, emHeader)
	copy(head, emMagic)
	binary.LittleEndian.PutUint32(head[6:], uint32(e.Frames))
	binary.LittleEndian.PutUint16(head[10:], uint16(e.Vocab))
	binary.LittleEndian.PutUint16(head[12:], uint16(e.FrameMs))
	binary.LittleEndian.PutUint32(head[14:], crc32.ChecksumIEEE(e.Data))
	tmp, err := os.CreateTemp(dir, ".em-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(head)
	if err == nil {
		_, err = tmp.Write(e.Data)
	}
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
		err = os.Rename(name, p)
	}
	if err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// Touch marks a file's windows as used now, so Prune removes them last.
func (c Cache) Touch(fileHash, modelSHA string) {
	if !cleanName(fileHash) || !cleanName(modelSHA) {
		return
	}
	now := time.Now()
	_ = os.Chtimes(filepath.Join(c.Root, fileHash, modelSHA), now, now)
}

// Prune removes the least recently used <fileHash>/<modelSHA> folders until the cache holds at most maxBytes.
// keep names folders ("fileHash/modelSHA") that stay whatever their age. It returns the bytes removed.
func (c Cache) Prune(maxBytes int64, keep map[string]bool) (int64, error) {
	type entry struct {
		key, dir string
		size     int64
		mtime    time.Time
	}
	var all []entry
	var total int64
	files, err := os.ReadDir(c.Root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		models, err := os.ReadDir(filepath.Join(c.Root, f.Name()))
		if err != nil {
			return 0, err
		}
		for _, m := range models {
			if !m.IsDir() {
				continue
			}
			dir := filepath.Join(c.Root, f.Name(), m.Name())
			info, err := m.Info()
			if err != nil {
				continue
			}
			var size int64
			_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					if i, e := d.Info(); e == nil {
						size += i.Size()
					}
				}
				return nil
			})
			all = append(all, entry{key: f.Name() + "/" + m.Name(), dir: dir, size: size, mtime: info.ModTime()})
			total += size
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].mtime.Equal(all[j].mtime) {
			return all[i].mtime.Before(all[j].mtime)
		}
		return all[i].key < all[j].key
	})
	var freed int64
	for _, e := range all {
		if total <= maxBytes {
			break
		}
		if keep[e.key] {
			continue
		}
		if err := os.RemoveAll(e.dir); err != nil {
			return freed, err
		}
		_ = os.Remove(filepath.Dir(e.dir)) // only succeeds when the file folder is now empty
		total -= e.size
		freed += e.size
	}
	return freed, nil
}
