package main

import "embed"

// packageIcons holds the pictures the desktop app shows itself (decision 0023, X3): the tray marks.
// The same folder carries the hicolor sizes that build-deb.sh installs; they are embedded too so
// the tests can read them, and they are small.
//
//go:embed package/icons/*.png
var packageIcons embed.FS

const (
	// trayIconLight is the walnut mark, for panels with a light ground; trayIconDark is the cream
	// mark for dark panels. The SetIcon picture is the light-panel one, as Wails names them.
	trayIconLight = "package/icons/tray-walnut-64.png"
	trayIconDark  = "package/icons/tray-cream-64.png"
	// appIconName is the picture of a bare binary (no .desktop file or icon theme): the about box
	// and the window icon where the platform asks for one.
	appIconName = "img/icon-192.png"
)

// iconBytes reads an embedded picture; an empty result means a build without it.
func iconBytes(name string) []byte {
	raw, err := packageIcons.ReadFile(name)
	if err != nil {
		return nil
	}
	return raw
}
