//go:build ignore

// iconflat checks a rendered icon and writes it as an opaque PNG without an alpha channel (App Store icons
// must have none). Usage: go run ios/tools/iconflat.go <in.png> <out.png>
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: iconflat <in.png> <out.png>")
		os.Exit(2)
	}
	in, err := os.Open(os.Args[1])
	if err != nil {
		fail(err)
	}
	defer in.Close()
	src, err := png.Decode(in)
	if err != nil {
		fail(err)
	}
	b := src.Bounds()
	if b.Dx() != 1024 || b.Dy() != 1024 {
		fail(fmt.Errorf("the icon is %dx%d, not 1024x1024", b.Dx(), b.Dy()))
	}
	// Re-encode through an image type without alpha (color.RGBModel), so the PNG is written as plain RGB.
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := src.At(x, y)
			if _, _, _, a := c.RGBA(); a != 0xffff {
				fail(fmt.Errorf("pixel %d,%d is not fully opaque", x, y))
			}
			dst.Set(x, y, color.RGBAModel.Convert(c))
		}
	}
	out, err := os.Create(os.Args[2])
	if err != nil {
		fail(err)
	}
	if err := png.Encode(out, dst); err != nil {
		out.Close()
		fail(err)
	}
	if err := out.Close(); err != nil {
		fail(err)
	}
}
