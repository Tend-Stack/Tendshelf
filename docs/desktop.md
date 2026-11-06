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
player), More (Keyboard shortcuts, Quit) and Minimise, Maximise and Close. Drag the bar to move the window; double-click it
to maximise. If your desktop shows two bars or none, start it with `TENDSHELF_NATIVE_FRAME=1 tendshelf-desktop` for the system's own bar.

## Known limits

- If the window is blank or flickers on some graphics drivers, start it with
  `WEBKIT_DISABLE_DMABUF_RENDERER=1 tendshelf-desktop`.
- A server with a self-signed certificate is refused by WebKit.
- No offline downloads in the window (no service worker), no auto-update, no autostart.
- macOS and Windows: not verified.
