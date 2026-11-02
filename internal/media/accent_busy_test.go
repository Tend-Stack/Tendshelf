package media

import (
	"image"
	"image/color"
	"testing"
)

// gradient is busy artwork: red runs from 40 to 239 across the width and green from 96 to 159 down
// the height, so the pixels spread over many fine buckets and no one of them holds 5 %.
func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(40 + x*200/w), uint8(96 + y*64/h), 100, 255})
		}
	}
	return img
}

// TestTintBusyCover: the 4-bit count finds no winner on a gradient; the 3-bit count does, and the
// tint is the mean of that coarse bucket's pixels.
func TestTintBusyCover(t *testing.T) {
	img := gradient(128, 128)
	total := 128 * 128
	fine := tintBuckets{}
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			c := img.RGBAAt(x, y)
			fine.add(int(c.R), int(c.G), int(c.B), 4)
		}
	}
	if hex, ok := fine.winner(total); ok {
		t.Fatalf("the 4-bit count found %s: the fixture is not busy enough", hex)
	}
	hex, ok := Tint(img)
	if !ok {
		t.Fatal("the gradient got no tint")
	}
	r, g, b, _ := parseHex(hex)
	// The biggest 3-bit bucket is one 32-wide red band with one of the two green halves; its mean lies in the gradient.
	if r < 40 || r > 239 || g < 96 || g > 159 || b != 100 {
		t.Errorf("tint %s is outside the gradient", hex)
	}
}

// TestTintBusyCoverStillNeedsColour: a coarse winner needs 5 % too, and the keep rule is the same.
func TestTintBusyCoverStillNeedsColour(t *testing.T) {
	// Every pixel differs by a lot: red over 40 to 239 and green over 40 to 239, so no 3-bit bucket
	// (49 of them, about 2 % each) reaches 5 %.
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(40 + x), uint8(40 + y), 100, 255})
		}
	}
	if hex, ok := Tint(img); ok {
		t.Errorf("Tint = %s, want none", hex)
	}
}
