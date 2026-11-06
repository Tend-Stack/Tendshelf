package web

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A small QR decoder for tests, written from ISO/IEC 18004 and independent of rsc.io/qr: it reads
// the SVG path back into modules, undoes the mask, de-interleaves the blocks, checks every block's
// Reed-Solomon syndromes and returns the text. It covers byte mode at level M, versions 1 to 10,
// which is every code this server draws. It exists to prove that what the page draws scans.

var qrPathRun = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`)

// qrModules rebuilds the module matrix (without the quiet zone) from a QRView's path and viewBox.
func qrModules(t *testing.T, viewBox, path string) [][]bool {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(viewBox, "0 0 %d %d", &n, &n); err != nil {
		t.Fatalf("viewBox %q: %v", viewBox, err)
	}
	size := n - 2*qrQuiet
	m := make([][]bool, size)
	for i := range m {
		m[i] = make([]bool, size)
	}
	rest := path
	for _, r := range qrPathRun.FindAllStringSubmatch(path, -1) {
		x, _ := strconv.Atoi(r[1])
		y, _ := strconv.Atoi(r[2])
		w, _ := strconv.Atoi(r[3])
		if r[3] != r[4] {
			t.Fatalf("run %q is not symmetric", r[0])
		}
		x, y = x-qrQuiet, y-qrQuiet
		if x < 0 || y < 0 || y >= size || x+w > size {
			t.Fatalf("run %q is outside the code", r[0])
		}
		for i := 0; i < w; i++ {
			if m[y][x+i] {
				t.Fatalf("run %q overlaps another", r[0])
			}
			m[y][x+i] = true
		}
		rest = strings.Replace(rest, r[0], "", 1)
	}
	if rest != "" {
		t.Fatalf("path holds more than runs: %q", rest)
	}
	return m
}

var qrAlign = map[int][]int{2: {6, 18}, 3: {6, 22}, 4: {6, 26}, 5: {6, 30}, 6: {6, 34}, 7: {6, 22, 38}, 8: {6, 24, 42}, 9: {6, 26, 46}, 10: {6, 28, 50}}

// qrBlocks gives, for level M, the data codewords of each block and the check codewords per block.
var qrBlocks = map[int]struct {
	data []int
	ec   int
}{
	1: {[]int{16}, 10}, 2: {[]int{28}, 16}, 3: {[]int{44}, 26}, 4: {[]int{32, 32}, 18}, 5: {[]int{43, 43}, 24},
	6: {[]int{27, 27, 27, 27}, 16}, 7: {[]int{31, 31, 31, 31}, 18}, 8: {[]int{38, 38, 39, 39}, 22},
	9: {[]int{36, 36, 36, 37, 37}, 22}, 10: {[]int{43, 43, 43, 43, 44}, 26},
}

func qrFunction(version int) [][]bool {
	size := 17 + 4*version
	f := make([][]bool, size)
	for i := range f {
		f[i] = make([]bool, size)
	}
	mark := func(x0, y0, w, h int) {
		for y := y0; y < y0+h; y++ {
			for x := x0; x < x0+w; x++ {
				if x >= 0 && y >= 0 && x < size && y < size {
					f[y][x] = true
				}
			}
		}
	}
	mark(0, 0, 9, 9)      // top-left finder, separator and format bits
	mark(size-8, 0, 8, 9) // top-right
	mark(0, size-8, 9, 8) // bottom-left, with the dark module
	mark(6, 0, 1, size)   // timing column
	mark(0, 6, size, 1)   // timing row
	centers := qrAlign[version]
	for _, cy := range centers {
		for _, cx := range centers {
			if (cx == 6 && cy == 6) || (cx == 6 && cy == centers[len(centers)-1]) || (cy == 6 && cx == centers[len(centers)-1]) {
				continue
			}
			mark(cx-2, cy-2, 5, 5)
		}
	}
	if version >= 7 {
		mark(size-11, 0, 3, 6)
		mark(0, size-11, 6, 3)
	}
	return f
}

func qrMask(m, y, x int) bool {
	switch m {
	case 0:
		return (y+x)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (y+x)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return (y*x)%2+(y*x)%3 == 0
	case 6:
		return ((y*x)%2+(y*x)%3)%2 == 0
	}
	return ((y+x)%2+(y*x)%3)%2 == 0
}

// gfMul multiplies in GF(256) with the QR polynomial 0x11d.
func gfMul(a, b int) int {
	r := 0
	for b > 0 {
		if b&1 == 1 {
			r ^= a
		}
		a <<= 1
		if a&0x100 != 0 {
			a ^= 0x11d
		}
		b >>= 1
	}
	return r
}

func qrDecode(t *testing.T, m [][]bool) string {
	t.Helper()
	size := len(m)
	version := (size - 17) / 4
	blocks, ok := qrBlocks[version]
	if !ok || 17+4*version != size {
		t.Fatalf("unsupported size %d", size)
	}
	// Format information, first copy.
	bits := 0
	push := func(x, y int) {
		bits <<= 1
		if m[y][x] {
			bits |= 1
		}
	}
	for x := 0; x <= 5; x++ {
		push(x, 8)
	}
	push(7, 8)
	push(8, 8)
	push(8, 7)
	for y := 5; y >= 0; y-- {
		push(8, y)
	}
	data := (bits ^ 0x5412) >> 10
	if level, mask := data>>3, data&7; level != 0 {
		t.Fatalf("error correction level bits = %d, want 0 (M)", level)
	} else {
		return qrRead(t, m, version, mask, blocks)
	}
	return ""
}

func qrRead(t *testing.T, m [][]bool, version, mask int, blocks struct {
	data []int
	ec   int
}) string {
	size := len(m)
	fn := qrFunction(version)
	var stream []int
	cur, n := 0, 0
	up := true
	for x := size - 1; x > 0; x -= 2 {
		if x == 6 {
			x--
		}
		for i := 0; i < size; i++ {
			y := i
			if up {
				y = size - 1 - i
			}
			for _, xx := range []int{x, x - 1} {
				if fn[y][xx] {
					continue
				}
				bit := m[y][xx] != qrMask(mask, y, xx)
				cur <<= 1
				if bit {
					cur |= 1
				}
				if n++; n == 8 {
					stream = append(stream, cur)
					cur, n = 0, 0
				}
			}
		}
		up = !up
	}
	total := blocks.ec * len(blocks.data)
	maxData := 0
	for _, d := range blocks.data {
		total += d
		if d > maxData {
			maxData = d
		}
	}
	if len(stream) < total {
		t.Fatalf("read %d codewords, want %d", len(stream), total)
	}
	stream = stream[:total]
	per := make([][]int, len(blocks.data))
	pos := 0
	for i := 0; i < maxData; i++ {
		for b, d := range blocks.data {
			if i < d {
				per[b] = append(per[b], stream[pos])
				pos++
			}
		}
	}
	for i := 0; i < blocks.ec; i++ {
		for b := range blocks.data {
			per[b] = append(per[b], stream[pos])
			pos++
		}
	}
	// Every block must have zero syndromes at alpha^0 .. alpha^(ec-1).
	for b, cw := range per {
		alpha := 1
		for s := 0; s < blocks.ec; s++ {
			acc := 0
			for _, c := range cw {
				acc = gfMul(acc, alpha) ^ c
			}
			if acc != 0 {
				t.Fatalf("block %d: Reed-Solomon syndrome %d is %d", b, s, acc)
			}
			alpha = gfMul(alpha, 2)
		}
	}
	var payload []int
	for b, d := range blocks.data {
		payload = append(payload, per[b][:d]...)
	}
	var bitstr strings.Builder
	for _, c := range payload {
		fmt.Fprintf(&bitstr, "%08b", c)
	}
	bs := bitstr.String()
	if bs[:4] != "0100" {
		t.Fatalf("mode bits %s, want byte mode 0100", bs[:4])
	}
	countBits := 8
	if version >= 10 {
		countBits = 16
	}
	count, _ := strconv.ParseInt(bs[4:4+countBits], 2, 32)
	out := make([]byte, count)
	for i := range out {
		v, _ := strconv.ParseInt(bs[4+countBits+8*i:4+countBits+8*i+8], 2, 32)
		out[i] = byte(v)
	}
	return string(out)
}
