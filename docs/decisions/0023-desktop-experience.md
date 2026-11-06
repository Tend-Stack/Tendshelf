# 0023 The desktop app gets its own title bar, on Wails 3.1
Status: proposed (2026-11-05). Waits for the operator's word on the board and the list at the end.
Plan: `docs/plans/m7-desktop-experience.md`. Board: `docs/design/boards/DesktopShell.html`.
Amends decision 0020 points 5 and 7 and that plan's "hidden title bars" out-of-scope line. 0020 stands otherwise.

## Operator's words (2026-11-05)
- "On the desktop app, can we have a design to show our own close menu and such? it looks plain and almost like a web
  browser wrap now."
- "Take full advantage of Wails v3 and create something amazing, a great user experience. Also add app icon and such."
- Approved upgrading Wails and using experimental features: "it's expected to have lots of these updates for now."

## What Wails 3.1.0 is
Read, not assumed: the Go proxy, the `v3.1.0` tag's own docs and source, and the module in the cache. wails.io answered
403 to our fetch, so the release post was read from its source at the tag: `docs/mpress/content/blog/wails-v3-1-released.md`.
- `go list -m -versions` shows `v3.1.0` (2026-11-01) as `latest`; `v3.2.0-nightly.*` run to 20261103. Feature releases are
  now weekly, fixes nightly (release post). The release notes pin an exact version, so we do too.
- **CGO-free builds**: opt-in, experimental, `purego` tag or `WAILS_PUREGO=1`. It is still GTK 4 and WebKitGTK 6: the same
  system libraries, opened by `dlopen` at start instead of linked. Not static: our binary still needs `libc`, `libdl` and
  `libpthread` (`ldd`). Floors GTK 4.8, WebKitGTK 2.40 (`internal/puregocheck/floors.json`). CGO stays the default.
- **Wake**: an experimental build and packaging system driven by one `wails.yaml`, on with `WAILS_EXP_USE_WAKE=1`. Migration is
  one-way; the docs say its configuration "may change between preview builds".
- **Fixes we want**: tray tooltips now work (we call `SetTooltip`), a tray made after `Run` survives, dark mode is read from
  the settings portal, a dismissed notification is no longer reported as a click, mixed-DPI placement.

## Evidence
All in a private `gnome-shell --headless`, GNOME Wayland, GTK 4.22.4, WebKitGTK 2.52.6, 2026-11-05. Nothing on the visible
desktop, `~/.local/share/tendshelf` and port 8095 untouched. Results: `.local/tests/desktop/exit-310-*` (ignored by Git).

| Check on 3.1.0 | CGO | CGO-free (`purego`) |
| --- | --- | --- |
| `make desktop-check` (vet, `-race`) | Pass | Vet and tests pass; no `-race`, it needs CGO |
| `make desktop-exit`, 20 checks | 11 PASS, 9 INFO, 0 FAIL | 11 PASS, 9 INFO, 0 FAIL, same rows |
| Cookie kept across restart | `signedIn: true` | `signedIn: true` |
| `attachment` saved (`download-started`) | Pass | Pass, with our Go twin of the C hook |
| `<audio>` on `/stream`, muted play, seek | played 1.0 s, seek 120 s | Same |
| Tray registers, second launch hands over | Pass | Pass |
| Binary, `-tags production` | 38.9 MB | 39.6 MB; builds with no C compiler |

0020's findings hold. Wails 3.1 still exposes neither cookies nor downloads: no match for them in `pkg/` or `internal/`, in
either backend. So the two calls stay ours, and purego can make them too (`desktop/webkit_purego.go`, about 100 lines).

Title bar spike: real pointer and keyboard input through Mutter's RemoteDesktop API, one held session, both backends.

| Action | `Frameless: true` | Hidden GTK titlebar (chosen) |
| --- | --- | --- |
| Shadow, buffer against frame | **None**: 900×600 in 900×600 | **Yes**: 928×629 around 900×600 |
| Rounded corners | **Square** | **Yes** |
| Edge resize | Only with our own script on the page | **Native**: GTK's border, no script |
| Move from the bar (`wails:drag`) | Follows the pointer exactly | Follows the pointer exactly |
| Double-click maximise, restore | Pass | Pass |
| Our close button quits | Pass | Pass |
| Ctrl+A, C, V in a field, with no menu | Pass | Pass |

The technique: start the window hidden, call `gtk_window_set_titlebar` with a hidden widget, then show it. Learned on the way:
`ApplicationStarted` fires before the native window exists, so wait for `NativeWindow()`; a visible empty box still draws a
44 px strip; `wails:drag` must go on the first mouse move with the button held, as the Wails runtime does; `HideMenuBar` is an
empty function on Linux (`webview_window_linux.go:619`), so the menu row goes by not attaching a menu at all. The first key
after a click was sometimes lost in the driver; a real keyboard is on the manual list.

Wake trial, CLI built into a scratch folder, `--skip-build` on our binary: a `.deb` in 0.6 s, with `Depends: libgtk-4-1,
libwebkitgtk-6.0-4`, and its own defaults for the rest: `/usr/local/bin`, a `postinst`, `Homepage: https://wails.io`, an empty
maintainer, `Categories=Utility;`. `dependencies` and `desktop_entry` are settable; the schema has no `Recommends`.

`make desktop` was silently doing nothing: `.PHONY` read `desktopdesktop-check`, and a `desktop/` folder exists, so `make deb`
could package a stale binary. Fixed in its own commit.

## Decision
1. **Upgrade to Wails v3.1.0, pinned**, after the release in flight. Not nightlies.
2. **Our own title bar, on a real GTK window.** Hidden GTK titlebar, never `Frameless`: GTK keeps the shadow, the corners and
   the resize border, and we write no resize code. This is the difference between an app and a flat rectangle. It supersedes
   0020 point 5 "the window keeps its native title bar"; the rest of that point stands. There is still no Wails runtime in pages:
   `desktop.js` already posts `wails:` messages, and `wails:drag` is one more, which 0020 point 6 already allowed.
3. **The bar is server-rendered, only under `data-shell="desktop"`.** It sits outside `#main`, so boosted navigation keeps it.
   Logo and name, Back, Forward, the place ("Library / The Cartographer's Silence"), now playing, More, Minimise, Maximise, Close.
   The logo moves up from the sidebar. Close warms to ember, the one accent. 44 px targets, keyboard reachable, four themes.
4. **The bar has no play button.** "One resume button per book on any screen" holds: it says what is playing and opens the player.
5. **More replaces the menu row on Linux.** Change mode or server, zoom, the close option, Keyboard shortcuts, About, Quit. Shortcuts
   move to `KeyBindings`. macOS keeps a real application menu when it is verified.
6. **The best small set**, because each is worth having and costs little:
   - A window that arrives ready: hidden until painted, on the saved theme's ground, at the saved size.
   - The app icon everywhere, from the handoff, unchanged.
   - A tray that knows the book: title, chapter, Pause, Skip back, Skip forward. No Play from idle (below).
   - A notification at book end and when the sleep timer stops, only when the window is not in front.
   - Drag a book onto the window, for people allowed to add books, through the existing uploader.
   - Window rows in the `?` sheet the app already has.
7. **Not now, with reasons.**
   - Mini player: `AlwaysOnTop` and window position are no-ops on Wayland (`PUREGO_LINUX.md`), so it could not stay on top, and it
     needs a second WebKit process.
   - Global shortcuts: their key list has no media keys, and Wayland needs the person to bind through a portal (M6 plan, D5).
   - Own MPRIS player: WebKit's own already answered `Pause`, `Play` and `PlayPause` over D-Bus in M6. A real media key is still unchecked
     (X6's manual list); if it fails, D5's fallback is written and waiting.
   - Splash window: point 6's ready window replaces it.
   - Open with Tendshelf: the operator decides (item 5 below). Wails reads only a lone argument, and files cross into the library only
     through `internal/upload`.
   - Dock badges: the Linux service is a declared stub. Auto-start, deep links, updater: nothing asks for them; apt updates the app.
8. **CGO-free: yes, as the target, not yet the default.** It passed everything here, and it removes the C compiler and `-dev` packages
   from CI and lets one machine build arm64. But the sound of real audio, real keys and the real tray are unchecked, and it is
   experimental. So: `make desktop-purego` now, checked in `desktop-check`; it becomes the default after the operator's own-desktop
   run (package X6); a build tag is the way back. Our WebKit and GTK calls become one purego file that works in both builds, so `desktop/`
   holds no C either way. `-race` stays on the CGO build. `purego` becomes a direct requirement, already in Wails' graph (`NOTICES.md`).
9. **Wake: no, for now.** Our packaging is 40 readable lines, needs no CLI and has no install scripts, and "no maintainer scripts" is a
   promise in `build-deb.sh`. Wake would add an experimental tool, a one-way migration, and overrides for most fields. Look again
   when macOS or Windows arrive: portable DMG, MSIX and notarisation from Linux are real gains that a `.deb` does not need.
10. **Bridge.** New messages, same channel: `tendshelf:window <minimise|maximise|close|quit>` and `tendshelf:state <json>`, Go's
    `commands` list gains fixed `ts:desktop` values. Both only from the session's origin or the picker. Page words reach the tray and
    notifications as data: at most 1,024 B, cut to 200 runes, control characters dropped, and `<`, `>`, `&` escaped because
    notification bodies may carry markup. Never into a script. A connect-mode server can close its own window; it could already
    navigate it.
11. **Nothing desktop-only changes what a browser loads.** Markup, CSS and JS are all behind `p.Shell == "desktop"`; `upload.js` and
    core files gain 0 bytes. The drop hands files to the existing input, so even the uploader is unchanged.

## App icon
Source: `docs/design/tendshelf-handoff/design-system/assets/logos/tendshelf-app-icon.svg`, byte-identical to the app's copy. No new
drawing. Rasterised once and committed, by the existing `TestRenderInstallIcons` (needs a browser; the build needs none).
- **Linux**: hicolor `apps/org.tendshelf.Tendshelf.png` at 16, 22, 24, 32, 48, 64, 128, 256, 512, plus `scalable/apps/…svg`. Wayland
  matches the window to the `.desktop` by app id, so `StartupWMClass` stays the id. Today only 512 ships.
- **A bare binary** with no `.desktop`: `Options.Icon` with `icon-192.png`, which the app has.
- **Tray**: below 32 px the wave disappears into the ember square (see the board), so the tray uses the handoff's plain mark, cream
  with `SetIcon` and walnut with `SetDarkModeIcon`. A choice of existing art, not a new logo.
- **Later**: macOS `.icns` (16 to 1024 with @2x; or Icon Composer), Windows `.ico` (16, 24, 32, 48, 64, 128, 256). `wails3 generate icons`
  builds both from a large PNG, so the 512 is close; Apple's 1024 needs one more render.

## Budgets to approve
Measured now: app JS **89,581 of 89,600 B**, so there is no room for this inside the ceiling, and browsers never load it.
- `desktop.js` stays at its 1,280 B row (about 1,180 B).
- **`desktop-shell.js`, new, 2,816 B** (estimate 2.4 KB, about 150 lines). Same head condition as `desktop.js`, no effect in a browser.
- **Move both out of `jsTotalGzip` into `jsDesktopTotalGzip` = 4,096 B.** The browser ceiling then means what browsers really load.
  Effect: `jsTotalGzip` is unchanged at 89,600 and browser JS measures about 88,400, so the ceiling gains about 1.2 KB. Sound modes'
  approved 92,160 B then has about 3.76 KB free, not 2.58 KB. Nothing is minified.
- `pages-desktop.css` stays in the 3,072 B row: 730 B today, **2,121 B** with the board's rules (measured).
- Core CSS and `player.js`: 0 bytes.

## Rule changes (AGENTS.md, in the packages named)
- X1, Stack constraints, Wails line: "The window keeps GTK's decorations with a hidden titlebar, never `Frameless` (decision 0023)."
- X6, when the default changes: "Desktop builds use Wails' purego backend; `purego` is the only native-call module."

## Consequences
- Other desktops are unverified: KDE, Sway and X11 may treat the hidden titlebar differently. If a compositor draws its own frame, the
  person sees two bars, so `X1` checks X11 at least. The buttons sit on the right; a left-hand `button-layout` is later.
- A native modal `<dialog>` sits above the bar, so the window buttons wait until it closes. Accepted.
- Window position is not remembered on Wayland, only size and maximised.
- The GoTHW reference is at 3.0.0 and says frameless needs `Frameless`; it needs this note when 3.1 merges.
- The tray still cannot start a book from idle, WebKit's autoplay rule (0020). So its first action is Pause, and with nothing playing it
  offers Show and Quit.

## For the operator (defaults in bold)
1. **The bar as drawn on the board.** Say what to change; the plan waits for this.
2. Closing the window while a book plays: **quits, as today, with "Keep playing when I close the window" as an opt-in line**, offered only when
   a tray exists. The other choice is staying in the tray by default.
3. Which build ships: **CGO for the next release; CGO-free becomes the default after your own-desktop run (X6).**
4. **Move desktop-only JS out of the browser ceiling**, with `desktop-shell.js` at 2,816 B and a 4,096 B desktop total.
5. Open with Tendshelf (double-click an `.epub` or `.m4b`): **later.** It needs a `MimeType=` line, and a one-time local address so the page can
   read the file through the uploader, which works in personal mode only. Drag covers the need meanwhile.
6. Dropping a book when uploads are off (decision 0015 keeps them off): **plain words and a link to the setting.** The other choice is turning
   uploads on for the personal library at first run, which changes 0015.
7. **Call this M6b on the roadmap**, because M7 is the dropped Research milestone. The plan's file name stays as asked.
8. **Merge after the release in flight, with one exception:** the `make desktop` fix (d909084) is worth taking into the release, because `make deb` can
   otherwise package a stale binary.
