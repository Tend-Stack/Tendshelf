// Package passkey verifies WebAuthn registrations and assertions with the standard library only
// (decision 0041). It holds no state and does no I/O.
package passkey

import (
	"errors"
	"unicode/utf8"
)

// maxCBORDepth is how many arrays or maps may sit inside one another.
const maxCBORDepth = 8

var errCBOR = errors.New("passkey: bad CBOR")

// cborMap is a decoded CBOR map. Keys are int64 or string; a duplicate key is refused while decoding.
type cborMap map[any]any

// decodeCBOR reads one data item from b and returns it with the bytes after it. It understands
// only what a passkey ceremony needs: unsigned and negative integers (as int64), byte strings,
// text strings, arrays, maps with integer or text keys, and false, true and null. Tags, floats,
// indefinite lengths, duplicate map keys, nesting deeper than maxCBORDepth and any length that
// runs past the buffer are refused. depth is the nesting of the item (0 at the top).
func decodeCBOR(b []byte, depth int) (any, []byte, error) {
	if len(b) == 0 {
		return nil, nil, errCBOR
	}
	major := b[0] >> 5
	info := b[0] & 0x1f
	b = b[1:]
	if major == 7 {
		switch info {
		case 20:
			return false, b, nil
		case 21:
			return true, b, nil
		case 22:
			return nil, b, nil
		}
		return nil, nil, errCBOR // floats, undefined, break, other simple values
	}
	if major == 6 {
		return nil, nil, errCBOR // tags
	}
	var n uint64
	switch {
	case info < 24:
		n = uint64(info)
	case info >= 24 && info <= 27:
		size := 1 << (info - 24)
		if len(b) < size {
			return nil, nil, errCBOR
		}
		for _, c := range b[:size] {
			n = n<<8 | uint64(c)
		}
		b = b[size:]
	default:
		return nil, nil, errCBOR // reserved and indefinite lengths
	}
	switch major {
	case 0:
		if n > 1<<63-1 {
			return nil, nil, errCBOR
		}
		return int64(n), b, nil
	case 1:
		if n > 1<<63-1 {
			return nil, nil, errCBOR
		}
		return -1 - int64(n), b, nil
	case 2, 3:
		if n > uint64(len(b)) {
			return nil, nil, errCBOR
		}
		raw := b[:n]
		if major == 3 {
			if !utf8.Valid(raw) {
				return nil, nil, errCBOR
			}
			return string(raw), b[n:], nil
		}
		return append([]byte(nil), raw...), b[n:], nil
	}
	// Arrays and maps: every item takes at least one byte, so a count past the buffer is refused
	// before anything is allocated.
	if depth >= maxCBORDepth || n > uint64(len(b)) {
		return nil, nil, errCBOR
	}
	if major == 4 {
		out := make([]any, 0, n)
		for i := uint64(0); i < n; i++ {
			var v any
			var err error
			v, b, err = decodeCBOR(b, depth+1)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, v)
		}
		return out, b, nil
	}
	out := make(cborMap, n)
	for i := uint64(0); i < n; i++ {
		var k, v any
		var err error
		k, b, err = decodeCBOR(b, depth+1)
		if err != nil {
			return nil, nil, err
		}
		switch k.(type) {
		case int64, string:
		default:
			return nil, nil, errCBOR
		}
		if _, dup := out[k]; dup {
			return nil, nil, errCBOR
		}
		v, b, err = decodeCBOR(b, depth+1)
		if err != nil {
			return nil, nil, err
		}
		out[k] = v
	}
	return out, b, nil
}
