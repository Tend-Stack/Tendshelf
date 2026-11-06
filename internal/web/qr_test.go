package web

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// qrProbe is 75 bytes: version 5, 37 modules at level M (plan docs/plans/invite-and-qr.md, D11).
func qrProbe() string {
	s := "http://tendshelf.example.org:8095/pair/"
	return s + strings.Repeat("Ab3-_", 8)[:75-len(s)]
}

func TestQRDrawsAnAddressThatScans(t *testing.T) {
	for _, text := range []string{
		qrProbe(),
		"https://x.example/invite/" + strings.Repeat("Z", 43),
		"http://192.168.1.20:8095/pair/-_" + strings.Repeat("a1", 21) + "9",
		"https://tendshelf.example.org/invite/ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ?x=1&y=é",
	} {
		v, ok := qrView(text, "label")
		if !ok {
			t.Fatalf("%q: not encoded", text)
		}
		m := qrModules(t, v.ViewBox, v.Path)
		if got := qrDecode(t, m); got != text {
			t.Errorf("decoded %q, want %q", got, text)
		}
	}
}

func TestQRShape(t *testing.T) {
	text := qrProbe()
	if len(text) != 75 {
		t.Fatalf("probe is %d bytes", len(text))
	}
	v, ok := qrView(text, "QR code for adding a phone")
	if !ok {
		t.Fatal("not encoded")
	}
	m := qrModules(t, v.ViewBox, v.Path)
	if len(m) != 37 {
		t.Fatalf("size = %d, want 37", len(m))
	}
	if v.ViewBox != "0 0 45 45" {
		t.Errorf("viewBox = %q: the quiet zone is %d modules on each side", v.ViewBox, qrQuiet)
	}
	// No run starts or ends in the quiet zone: every coordinate lies inside the 37 modules shifted by 4.
	for _, r := range qrPathRun.FindAllStringSubmatch(v.Path, -1) {
		x, _ := strconv.Atoi(r[1])
		y, _ := strconv.Atoi(r[2])
		w, _ := strconv.Atoi(r[3])
		if x < qrQuiet || y < qrQuiet || x+w > 37+qrQuiet || y >= 37+qrQuiet {
			t.Errorf("run %s reaches into the quiet zone", r[0])
		}
	}
	// Three finder patterns: a 7x7 ring of dark, a ring of light, a 3x3 dark centre; none at the fourth corner.
	finder := func(x0, y0 int) bool {
		for dy := 0; dy < 7; dy++ {
			for dx := 0; dx < 7; dx++ {
				ring := dx == 0 || dx == 6 || dy == 0 || dy == 6
				core := dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4
				if m[y0+dy][x0+dx] != (ring || core) {
					return false
				}
			}
		}
		return true
	}
	if !finder(0, 0) || !finder(30, 0) || !finder(0, 30) {
		t.Error("a finder pattern is missing")
	}
	if finder(30, 30) {
		t.Error("a finder pattern at the fourth corner")
	}
	// The path covers exactly the dark modules: re-draw from the matrix and compare.
	var again bytes.Buffer
	for y, row := range m {
		for x := 0; x < len(row); {
			if !row[x] {
				x++
				continue
			}
			s := x
			for x < len(row) && row[x] {
				x++
			}
			again.WriteString("M" + strconv.Itoa(s+qrQuiet) + " " + strconv.Itoa(y+qrQuiet) + "h" + strconv.Itoa(x-s) + "v1h-" + strconv.Itoa(x-s) + "z")
		}
	}
	if again.String() != v.Path {
		t.Error("the path is not the runs of the dark modules")
	}
	// The component: one path, a plate, no inline style, no script, an accessible name.
	var buf bytes.Buffer
	if err := components.QR(v).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`class="ts-qr"`, `viewBox="0 0 45 45"`, `role="img"`, `aria-label="QR code for adding a phone"`, `class="ts-qr__paper"`, `class="ts-qr__ink"`} {
		if !strings.Contains(out, want) {
			t.Errorf("svg lacks %s", want)
		}
	}
	if strings.Contains(out, "style=") || strings.Contains(out, "<script") || strings.Contains(out, "data:") || strings.Count(out, "<path") != 1 {
		t.Error("the svg has an inline style, a script, a data: address or more than one path")
	}
}

func TestQRTooLongIsNotDrawn(t *testing.T) {
	if _, ok := qrView(strings.Repeat("x", 8000), "x"); ok {
		t.Error("an address too long for a code was drawn")
	}
}

func TestQRReachableHost(t *testing.T) {
	for host, want := range map[string]bool{
		"tendshelf.example.org": true, "tendshelf.example.org:8443": true, "192.168.1.20:8095": true, "10.0.0.5": true, "[2001:db8::1]:8095": true, "shelf.home.arpa": true,
		"localhost": false, "localhost:8095": false, "LOCALHOST": false, "app.localhost:8095": false, "localhost.": false,
		"127.0.0.1": false, "127.0.0.1:8095": false, "127.9.9.9": false, "[::1]": false, "[::1]:8095": false, "0.0.0.0": false, "0.0.0.0:80": false, "[::]:80": false, "": false,
	} {
		if got := reachableHost(host); got != want {
			t.Errorf("reachableHost(%q) = %v, want %v", host, got, want)
		}
	}
}
