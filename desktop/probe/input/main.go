//go:build tendshelf_probe

// Command input is the exit run's hands (decision 0023, X1): it holds one Mutter RemoteDesktop
// session open and turns lines on its standard input into real pointer and key events, so the
// run can click, drag and type in the private GNOME Shell. It talks to the session bus it is
// started under, which scripts/isolated-display.sh makes private; it never reaches the person's own.
//
// Lines (one per command):
//
//	move X Y        pointer to X,Y on screen (it goes to the corner first, then across)
//	rel DX DY       pointer by DX,DY, in small steps
//	btn N 1|0       button N (1 left, 2 middle, 3 right) down or up
//	click           left button down, a short wait, up
//	key NAME 1|0    key down or up; NAME is a letter, Control_L, Alt_L, Escape, Return, F4, ...
//	tap NAME...     each key down in order, then all up in reverse (a chord: tap Control_L q)
//	sleep MS
//	quit
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	service = "org.gnome.Mutter.RemoteDesktop"
	rdPath  = "/org/gnome/Mutter/RemoteDesktop"
	session = "org.gnome.Mutter.RemoteDesktop.Session"
)

var keysyms = map[string]uint32{
	"Control_L": 0xffe3, "Shift_L": 0xffe1, "Alt_L": 0xffe9, "Super_L": 0xffeb,
	"Escape": 0xff1b, "Return": 0xff0d, "Tab": 0xff09, "BackSpace": 0xff08, "space": 0x20,
	"Left": 0xff51, "Right": 0xff53, "Up": 0xff52, "Down": 0xff54,
	"F4": 0xffc1, "F11": 0xffc8,
}

func keysym(name string) (uint32, error) {
	if v, ok := keysyms[name]; ok {
		return v, nil
	}
	if r := []rune(name); len(r) == 1 {
		return uint32(r[0]), nil // Latin-1 keysyms equal their code point
	}
	return 0, fmt.Errorf("unknown key %q", name)
}

func main() {
	bus, err := dbus.ConnectSessionBus()
	if err != nil {
		fail("session bus: %v", err)
	}
	var path dbus.ObjectPath
	if err := bus.Object(service, rdPath).Call(service+".CreateSession", 0).Store(&path); err != nil {
		fail("create the remote desktop session: %v", err)
	}
	obj := bus.Object(service, path)
	if err := obj.Call(session+".Start", 0).Err; err != nil {
		fail("start the session: %v", err)
	}
	defer obj.Call(session+".Stop", 0)

	call := func(method string, args ...any) {
		if err := obj.Call(session+"."+method, 0, args...).Err; err != nil {
			fail("%s: %v", method, err)
		}
	}
	rel := func(dx, dy float64) {
		const step = 24.0
		for dx != 0 || dy != 0 {
			sx, sy := clamp(dx, step), clamp(dy, step)
			call("NotifyPointerMotionRelative", sx, sy)
			dx, dy = dx-sx, dy-sy
			time.Sleep(4 * time.Millisecond)
		}
	}
	button := func(n int, down bool) {
		codes := map[int]int32{1: 272, 2: 274, 3: 273} // BTN_LEFT, BTN_MIDDLE, BTN_RIGHT
		call("NotifyPointerButton", codes[n], down)
	}
	key := func(name string, down bool) {
		sym, err := keysym(name)
		if err != nil {
			fail("%v", err)
		}
		call("NotifyKeyboardKeysym", sym, down)
		time.Sleep(15 * time.Millisecond)
	}

	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		f := strings.Fields(in.Text())
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "move":
			x, y := num(f, 1), num(f, 2)
			call("NotifyPointerMotionRelative", -20000.0, -20000.0) // the pointer stops at the corner
			rel(x, y)
		case "rel":
			rel(num(f, 1), num(f, 2))
		case "btn":
			button(int(num(f, 1)), num(f, 2) != 0)
		case "click":
			button(1, true)
			time.Sleep(60 * time.Millisecond)
			button(1, false)
		case "key":
			if len(f) < 3 {
				fail("key needs a name and 1 or 0")
			}
			key(f[1], num(f, 2) != 0)
		case "tap":
			for _, k := range f[1:] {
				key(k, true)
			}
			for i := len(f) - 1; i >= 1; i-- {
				key(f[i], false)
			}
		case "sleep":
			time.Sleep(time.Duration(num(f, 1)) * time.Millisecond)
		case "quit":
			return
		default:
			fail("unknown command %q", f[0])
		}
		fmt.Println("ok", f[0])
	}
}

func clamp(v, limit float64) float64 {
	if v > limit {
		return limit
	}
	if v < -limit {
		return -limit
	}
	return v
}

func num(f []string, i int) float64 {
	if i >= len(f) {
		fail("%s needs more arguments", f[0])
	}
	v, err := strconv.ParseFloat(f[i], 64)
	if err != nil {
		fail("%q is not a number", f[i])
	}
	return v
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "input: "+format+"\n", args...)
	os.Exit(1)
}
