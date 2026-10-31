package db

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// crockford is the Crockford base32 alphabet used by ULIDs.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewID returns prefix + "_" + a ULID-style identifier: 48 bits of Unix
// milliseconds then 80 random bits, in 26 Crockford base32 characters, so
// IDs sort by creation time. Example: "bk_01JBX3Q8ZK5V9T2M4N6P8R0S2T".
func NewID(prefix string) string {
	return newID(prefix, time.Now())
}

func newID(prefix string, now time.Time) string {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	b[0], b[1], b[2], b[3], b[4], b[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		panic("tendshelf: crypto/rand failed: " + err.Error())
	}
	hi := binary.BigEndian.Uint64(b[:8])
	lo := binary.BigEndian.Uint64(b[8:])
	var out [26]byte
	for i := 25; i >= 0; i-- {
		out[i] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return prefix + "_" + string(out[:])
}
