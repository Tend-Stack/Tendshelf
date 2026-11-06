package web

import (
	"fmt"
	"net"
	"strings"

	"rsc.io/qr"

	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// qrQuiet is the quiet zone around a code, in modules. The QR standard asks for four.
const qrQuiet = 4

// qrView encodes text at error-correction level M and returns it as one SVG path: a rectangle for
// each horizontal run of dark modules, shifted by the quiet zone. false means the text could not be
// encoded; the caller then shows the link alone. This is the only file that imports rsc.io/qr.
func qrView(text, label string) (components.QRView, bool) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return components.QRView{}, false
	}
	var b strings.Builder
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; {
			if !code.Black(x, y) {
				x++
				continue
			}
			start := x
			for x < code.Size && code.Black(x, y) {
				x++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", start+qrQuiet, y+qrQuiet, x-start, x-start)
		}
	}
	n := code.Size + 2*qrQuiet
	return components.QRView{ViewBox: fmt.Sprintf("0 0 %d %d", n, n), Path: b.String(), Label: label}, true
}

// reachableHost reports whether a phone could reach host (with or without a port). Loopback and
// unspecified addresses, and "localhost" and "*.localhost", mean "this computer": a QR for them
// would send the phone to itself, so the pages show words instead (decision 0028, D9).
func reachableHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.TrimSuffix(strings.Trim(h, "[]"), ".")
	if h == "" {
		return false
	}
	lower := strings.ToLower(h)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return !ip.IsLoopback() && !ip.IsUnspecified()
	}
	return true
}
