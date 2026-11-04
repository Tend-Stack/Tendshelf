package ebook

import (
	"bytes"
	"encoding/binary"
	"image"
	_ "image/gif" // register the decoders imageSize reads headers with
	_ "image/jpeg"
	_ "image/png"
)

// ImageKind sniffs the picture type the reader serves: "jpeg", "png", "gif" or "webp". Anything else
// (SVG included) is "".
func ImageKind(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "jpeg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "gif"
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "webp"
	}
	return ""
}

// imageSize reads the pixel size from the header of a JPEG, PNG, GIF or WebP; 0, 0 when unknown.
// Only the header is decoded, never the pixels.
func imageSize(data []byte) (w, h int) {
	switch ImageKind(data) {
	case "jpeg", "png", "gif":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return 0, 0
		}
		return cfg.Width, cfg.Height
	case "webp":
		return webpSize(data)
	}
	return 0, 0
}

// webpSize reads the three WebP flavours: VP8 (lossy), VP8L (lossless) and VP8X (extended).
func webpSize(d []byte) (int, int) {
	if len(d) < 30 {
		return 0, 0
	}
	switch string(d[12:16]) {
	case "VP8 ":
		// frame tag (3 bytes), start code 9d 01 2a, then 14-bit width and height.
		if d[23] != 0x9d || d[24] != 0x01 || d[25] != 0x2a {
			return 0, 0
		}
		return int(binary.LittleEndian.Uint16(d[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(d[28:30]) & 0x3fff)
	case "VP8L":
		if d[20] != 0x2f {
			return 0, 0
		}
		bits := binary.LittleEndian.Uint32(d[21:25])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1
	case "VP8X":
		w := int(d[24]) | int(d[25])<<8 | int(d[26])<<16
		h := int(d[27]) | int(d[28])<<8 | int(d[29])<<16
		return w + 1, h + 1
	}
	return 0, 0
}
