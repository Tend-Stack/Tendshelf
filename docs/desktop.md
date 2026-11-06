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

### Never wider than 1,920 px

The pages stop growing at 1,920 px (`--size-app-max`), so the window never goes wider either: a wider window on an
ultrawide monitor would only show a blank strip. Dragging an edge or corner stops at 1,920 px, and the Maximise button
becomes "fit to screen". On a screen no wider than 1,920 px it maximises as before. On a wider one it sets the window to
1,920 px by the screen's usable height and centres it where the system lets an app place its window; pressing it
again goes back to the earlier size. A maximise that did not come from the button (double-click on a system bar,
Super+Up, a tiling key) becomes the same fit, and a window saved as maximised starts fitted. A fitted window shows
Restore and is saved as maximised, with the size to return to kept. There is no full-screen mode, in the bar, the
menu or a key. The rules are in `desktop/windowstate.go` (`maxWidth`, `fitWindow`, `fitTarget`), and `desktop/windowfit.go`.

How it is done, because GTK 4 gives no help here: Wails' `MaxWidth` only bounds the sizes Wails sets itself (its
clamp listens to `notify::default-width`, which a drag never changes), and `gtk_window_set_default_size` cannot make
a window that is on screen smaller. So a drag past 1,920 px locks the window at 1,920 px (not resizable, through
`gtk_window_set_resizable`) until the mouse button is let go, then unlocks it; the window stops at the limit instead
of following the pointer. The fit uses the same lock to set its size.

Sizes are logical pixels, the same as CSS pixels at 100 % page zoom. The screen's scale (HiDPI) is already out of them.
At a page zoom above 100 % the page has fewer CSS pixels than the window, so no strip appears. Below 100 % a
1,920 px window holds more than 1,920 CSS pixels and a strip returns beside the page; the window is not made
narrower for that.

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

## Dropping a book

Drag audiobook or ebook files from your file manager onto the window. If you are the owner or an admin and a library takes
uploads, a zone says "Drop to add to your library", and the files go to that library's Add books page, where the same
uploader as in the browser takes them (one upload path, decision 0015): copy-protected files are refused with the same
words, and nothing is kept. Escape or leaving the window hides the zone. A folder is not taken here; a toast points to
Add books, which does take folders. When you cannot add books, or uploads are off, a drop shows plain words and, for an
owner or admin, a link to Libraries. Dropping on the Add books page itself is that page's own. The first library that takes
uploads and that you may add to is used.

## Known limits

- If the window is blank or flickers on some graphics drivers, start it with
  `WEBKIT_DISABLE_DMABUF_RENDERER=1 tendshelf-desktop`.
- A server with a self-signed certificate is refused by WebKit.
- No offline downloads in the window (no service worker), no auto-update, no autostart.
- Keep playing and the tray depend on the desktop showing tray icons; without one the option is not offered and Close quits.
- Whether sound continues in a hidden window, a real tray click and a real notification click are for the operator's own desktop.
- A real drag from the file manager into the window is for the operator's own desktop; the tests drop generated files on the page in Chromium with the window channel stubbed.
- Under Wayland an app cannot place its window, so the fit is 1,920 px wide and as high as the screen but sits where the
  compositor puts it, not centred; a window already 1,920 px wide and low on the screen may not grow taller. X11 centres it.
  An edge drag past the limit snaps back and ends that drag; press again to keep resizing.
- macOS and Windows: not verified.
