# Tendshelf desktop app

A window around Tendshelf, built with Wails v3 on GTK 4 and WebKitGTK 6. Design and evidence:
[decision 0020](decisions/0020-desktop-shell.md). Linux is the verified platform.

## Install (Debian, Ubuntu)

`make deb` builds `bin/tendshelf-desktop_<describe>_<arch>.deb` (needs `libgtk-4-dev`,
`libwebkitgtk-6.0-dev`, `dpkg-deb`). Install it with `sudo apt install ./bin/tendshelf-desktop_*.deb`.
apt brings `libgtk-4-1`, `libwebkitgtk-6.0-4`, `gstreamer1.0-plugins-good`, `gstreamer1.0-libav` and
`ffmpeg`, so audio plays and books are read. The package has no install scripts and the app downloads
nothing. On GNOME, `gnome-shell-extension-appindicator` (recommended) shows a tray icon.
The `.deb` installs the app icon at 16, 22, 24, 32, 48, 64, 128, 192, 256 and 512 px plus a scalable SVG in the hicolor theme; the tray uses a walnut mark on light panels and a cream mark on dark ones (`desktop/package/icons/`, redrawn with `TENDSHELF_RENDER_ICONS=1`).

Without the package, `make desktop` builds `bin/tendshelf-desktop`. `make desktop-purego` builds the same window with no C
compiler and no `-dev` packages (experimental, [decision 0023](decisions/0023-desktop-experience.md)); GTK and WebKit must still be installed. Without ffmpeg on the `PATH` the app
warns once and lists books unread.

## Modes

- **Personal**: Tendshelf runs inside the app on this computer, with its own library under
  `~/.local/share/tendshelf-desktop/library`. It listens on loopback only and needs a sign-in like any server.
- **Connect**: the window opens a Tendshelf server you already run. The server must be recent enough to
  answer `/healthz` with `shell` support; an older one is refused in plain words.

The choice is saved in `~/.config/tendshelf-desktop/desktop.json`, with the window's size and whether it was
maximised (the position is not kept: Wayland does not let an app place its window). Quit with Ctrl+Q.

## The window

The window opens when the first page is ready, at the size you left it, on the background of your theme. It has
the system's shadow, rounded corners and resize border; the title bar is drawn by the page itself (decision 0023).
The bar has Back and Forward (also Alt+Left and Alt+Right), the page's place, the book that is playing (it opens the
player), More and Minimise, Maximise and Close. More has Change mode or server, Zoom in, Zoom out, Actual size, Keep playing when I
close the window (shown only when your desktop has a tray), Keyboard shortcuts, About Tendshelf and Quit. Drag the bar to move the window; double-click it
to maximise. If your desktop shows two bars or none, start it with `TENDSHELF_NATIVE_FRAME=1 tendshelf-desktop` for the system's own bar.

## Now playing

While a book is loaded, the tray icon's menu names it (title, then chapter and time left), and offers Pause (only while it
plays), Skip back and Skip forward, then Show Tendshelf and Quit; the icon's tooltip names the book too. With nothing loaded
the menu is Show Tendshelf and Quit. The tray never starts a book: WebKit allows sound only after a click in the page.
Clicking the icon shows or hides the window. The tray needs a StatusNotifier host (on GNOME, the AppIndicator extension).

When a book ends, or the sleep timer stops it, and Tendshelf is not the window in front, a desktop notification says so;
clicking it brings the window back. **Keep playing when I close the window** (More; off by default, and offered only when a
tray exists) makes Close hide the window while a book plays, so the sound goes on and the tray icon brings it back. Quit
(Ctrl+Q, the tray, More) always ends the app. The page's words reach the tray and the notification as data: at most 1,024
bytes, cut to 200 characters, control characters dropped, `<`, `>` and `&` escaped (decision 0023, point 10). The theme you
use is saved as the colour the window opens on.

## Known limits

- If the window is blank or flickers on some graphics drivers, start it with
  `WEBKIT_DISABLE_DMABUF_RENDERER=1 tendshelf-desktop`.
- A server with a self-signed certificate is refused by WebKit.
- No offline downloads in the window (no service worker), no auto-update, no autostart.
- Keep playing and the tray depend on the desktop showing tray icons; without one the option is not offered and Close quits.
- Whether sound continues in a hidden window, a real tray click and a real notification click are for the operator's own desktop.
- macOS and Windows: not verified.
