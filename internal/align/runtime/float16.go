package runtime

import "math"

// halfToFloat converts an IEEE 754 half-precision value to float32.
func halfToFloat(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	frac := uint32(h) & 0x3ff
	switch {
	case exp == 0 && frac == 0:
		return math.Float32frombits(sign)
	case exp == 0: // subnormal: frac * 2^-24
		f := float32(frac) / (1 << 24)
		if sign != 0 {
			return -f
		}
		return f
	case exp == 0x1f: // infinity or NaN
		return math.Float32frombits(sign | 0x7f800000 | frac<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | frac<<13)
}
