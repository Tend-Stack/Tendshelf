# Desktop experience (plan, 2026-11-05)

Decision and evidence: [0023](../decisions/0023-desktop-experience.md); read its tables first. Board:
`docs/design/boards/DesktopShell.html`, the visual contract. This is not the roadmap's M7 (Research, dropped).
Order: **X1**, then **X2 + X3** together, then **X4**, then **X5**, then **X6** alone. At most 200 tool calls each.
No package starts before the operator approves 0023 and the release in flight is out.
Reused: `Bridge`, `commands`, `isolated-display.sh`, `desktop/probe`, `TestRenderInstallIcons`, `#keys-help`, `upload.js`, `build-deb.sh`.
Extended: `desktop.js`, `pages-desktop.css`, `Layout`, `Frame`, `LinkShell`, the picker, `desktop.json`. Replaced: the menu row, `webkit_linux.go`
(C) by one purego file. New: `desktop-shell.js`, `desktop/titlebar.go`, `desktop/state.go`, `desktop/probe/input`.

## Invariants (all packages)
1. `CGO_ENABLED=0 make build` and `make check` stay green; the root module never imports `desktop/`.
2. Browsers see no change: without `ts_shell`, output is byte-identical (`make visual-compare`: all identical). `upload.js`, `player.js`,
   core CSS gain 0 bytes.
3. CSP unchanged in every mode. No inline script or style. Pages never load the Wails runtime.
4. No Node. Plain words; new strings listed in the report. 13 px smallest, 44 px targets, all four themes, `prefers-reduced-motion`.
5. Only exact PIDs are stopped. Windows open only in the private session.
6. Budgets are reported, never met by squeezing.

## Names (fixed)
- Messages: `tendshelf:window minimise|maximise|close|quit`, `tendshelf:state <json>`.
- `commands` gains `active`, `inactive`, `maximised`, `restored` and `tray` (a tray is present). The tray's skips reuse `back` and `forward`.
- State JSON: `{"title","chapter","left","playing","theme","canAdd"}`. Words are data (decision 0023, point 10).
- `desktop.json` gains `window: {width, height, maximised}`, `keepPlaying: false`, `ground: "dark"`.
- CSS: `ts-wbar*`, `ts-wmenu*`, `ts-wdrop*`, from the board; `--desktop-bar: var(--size-tap)`.

## X1 The window (Go; about 200 calls). Prefix `TestDeskWindow`, `TestDeskBridge`
1. Merge the 3.1.0 branch. `desktop/nativelib.go`: `dlopen` GTK, WebKit, GObject, GLib once, with a plain error naming the package.
2. `desktop/webkit.go` replaces `webkit_linux.go` and `webkit_purego.go`: one purego file, no tags beyond `linux && !gtk3`, works in both
   builds. `uniquePath` and its test move with it. Drop `#cgo pkg-config`.
3. `desktop/titlebar.go`: `hideTitlebar(win)`, the spike's three calls. Window options `Hidden: true`, no `UseApplicationMenu` and no menu on
   Linux, `KeyBindings` gain `ctrl+q`. In `ApplicationStarted` wait for `NativeWindow()`, apply, `InvokeSync`. Never `Frameless`.
4. Show on the first `tendshelf:ready`, with a 3 s fallback timer so a failed page cannot leave an invisible app. The picker page sends ready too.
5. Save size and maximised on `WindowClosing` (Wayland ignores position; do not store it). Restore them; clamp to the screen.
   `BackgroundColour` comes from `ground`, one of the four themes' `--ground`, a test keeping them equal to `tokens.css`.
6. Bridge: `tendshelf:window`, table-driven; same origin check as `allowOpen`, plus the picker. `win.Minimise`, `ToggleMaximise`, `Close`,
   `app.Quit`. `OnWindowEvent` for focus and maximise sends the fixed commands.
7. `desktop/probe/input`, from the spike's held RemoteDesktop client, tag `tendshelf_probe`: `move`, `btn`, `key`, using a focusing click first.
   `exit-run.sh` adds: shadow (buffer larger than frame), drag follows the pointer, edge resize, double-click, close, Ctrl+Q, copy and paste,
   hidden until ready, size restored. On X11 too: drop `--no-x11` from the private shell and run the window with `GDK_BACKEND=x11` through Xwayland; if that cannot start, record it as not run.
8. AGENTS.md rule, `docs/desktop.md`, `docs/architecture.md`.
Tests: `TestDeskBridgeWindow` (wrong origin, unknown verb), `TestDeskWindowStateRoundTrip`, `TestDeskWindowShowFallback`, `TestDeskWindowKeys`.
Gates: `make check`, `make build`, `make desktop-check`, `make desktop-exit`, the same exit run on `CGO_ENABLED=0`.

## X2 The bar (templ, CSS, JS; about 200 calls; parallel with X3). Prefix `TestDeskBar`, `TestDeskBudget`
1. `internal/web/components/desktop_bar.templ`, `DesktopBar`, rendered by `Layout`, `Frame`, `LinkShell` and the picker only when
   `p.Shell == "desktop"`, as the first child of `<body>`, outside `#main`, `hx-preserve`. Picker: no Back, Forward or More.
2. Port the board's rules to `pages-desktop.css`. Add window icons to `iconset.go` (`window-minimise`, `window-maximise`, `window-restore`,
   `more`, `nav-back`, `nav-forward`), unused icons cost only desktop markup.
3. Offsets, all under `html[data-shell="desktop"]`: `body` padding; `top: var(--desktop-bar)` and height for `.ts-nav-side`, `.ts-nav-rail`,
   `.ts-player`, `.ts-readerpage`, `.ts-eyesfree`, `.ts-sheet-layer`, `.ts-offline`, `.ts-prefs__nav`; hide `.ts-side__brand`. Test: every
   `position: fixed; inset: 0` in `static/css` is on this list, so a new one fails.
4. `internal/web/static/js/desktop-shell.js`, ES module, head condition beside `desktop.js`: bar drag (arm on `mousedown` with `detail 1` on empty
   bar, post `wails:drag` on first move with `buttons & 1`), double-click, window buttons, More as a menu (arrow keys, Escape, focus back),
   Back and Forward by history with a depth counter, Alt+Left and Alt+Right, `document.title` into the place after `htmx:after:swap`, crumb from
   `#main[data-ts-crumb-href]`. `ts:desktop` sets `html[data-window]` and the Maximise icon.
5. `Page.Crumb` for book, series, author and reader pages. Rendered on `#main` only under the desktop shell.
6. Guard: `dragover` and `drop` on the window `preventDefault`, so a dropped file never navigates the window to `file:`.
7. `TestJSBudget`: `jsDesktopFiles` skip `jsTotalGzip`, count in `jsDesktopTotalGzip = 4096`. `desktop-shell.js` row `{2816, 16 * 1024}`.
8. Add window rows to `#keys-help` under the desktop shell only.
9. Update `motion_test.go`'s history comment, `docs/architecture.md` budgets.
Tests: `TestDeskBarOnlyInShell`, `TestDeskBarOffsets`, `TestDeskBudgetSplit`, `TestDeskBarLabels` (every button named), `TestDeskBarCSP`.
Gates: `make check`, `make build`, `make a11y`, `make visual-compare`, `make page-weight`, board compared at 390, 768, 1024, 1440 and in four themes
through `scripts/isolated-display.sh make visual-webkit`; `make desktop-exit`. Contrast of `.ts-wbar__btn` rest and Close hover in all themes.

## X3 Icons and packaging (about 100 calls; parallel with X2). Prefix `TestDeskIcons`, `TestDeskPackage`
1. Add 16 to 256 to `installIcons` under `internal/web/static/img/hicolor/`; render with `TestRenderInstallIcons`. No new art. Keep them out of the
   service-worker precache and of `go:embed` budgets if they do not serve pages: they may live in `desktop/package/icons/` instead (preferred).
2. Tray: `img/tray-cream-64.png`, `img/tray-walnut-64.png` from the mark SVGs; `SetIcon`, `SetDarkModeIcon`.
3. `Options.Icon` = `icon-192.png` for a bare binary.
4. `build-deb.sh`: every size, `scalable/apps/org.tendshelf.Tendshelf.svg`. `.desktop` stays minimal. `dpkg-deb -c` test lists all of them.
Tests: `TestDeskIconsSizes` (PNG dimensions), `TestDeskPackageIcons`. Gates: `make deb`, `make desktop-check`.

## X4 Now playing (about 180 calls). Prefix `TestDeskState`, `TestDeskTray`, `TestDeskNotify`
1. `desktop-shell.js`: a `MutationObserver` on the player's data attributes, plus `playing` and `pause`, posts `tendshelf:state` when it changes, at
   most every 5 s for `left`. No `player.js` change. The bar's `ts-wbar__now` and hairline use the same values.
2. `desktop/state.go`: parse, limit, clean (`TestDeskStateLimits`: oversize, control characters, `<b>`).
3. `tray.go`: book lines disabled, Pause, Skip back, Skip forward; `menu.Update()` on change. Nothing playing: Show and Quit. `SetTooltip`.
4. Keep playing: direct `godbus` require (`NOTICES.md`), `NameHasOwner("org.kde.StatusNotifierWatcher")`; only then the More line shows. On: `RegisterHook`
   `WindowClosing` cancels and hides while playing. Tray must exist, so no hidden app is stranded. Off by default.
5. Notifications with Wails' `notifications` service: book end, sleep stop, only when not `active`; clicking shows the window. Check registering it adds
   nothing reachable from an http page (pages cannot reach `/wails/`); `TestDeskNotifyNotReachable`.
Gates: `make desktop-check`, `make desktop-exit` adding a tray menu read over D-Bus and a notification seen in the private shell. JS budget reported.

## X5 Drop a book (about 120 calls). Prefix `TestDeskDrop`
1. `canAdd` comes from a `data-ts-can-add` on the bar: owner or admin with a library whose uploads are on (`share.Can`, server-side).
2. `dragenter` with files and `canAdd`: `ts-wdrop`, Escape or leaving hides it. `drop`: keep the `File`s (not folders) in the module, boost to Add books,
   then `new DataTransfer()`, `items.add`, set the page's `input.files`, dispatch `change`. `upload.js` unchanged; its refusals give the words.
3. A folder or `canAdd` false: plain words in a toast, with the Add books link.
Tests: `TestDeskDropOnlyWhenAllowed` (forbidden cases per role). Gates: `make check`, `make desktop-check`. A real drag from Files is manual.

## X6 Own desktop, then CGO-free by default (about 80 calls; last, alone)
1. `docs/manual-checks.md`, new section: audible play, real keys, tray clicks, Nautilus drop, Ctrl+Q, 16 and 22 px icons on the panel, an X11 session, one
   hour of playback on the CGO-free build. The operator runs it.
2. On pass: `make desktop` uses `purego`; `make desktop-cgo` remains for one release; `desktop.yml` drops `gcc`, `pkg-config`, `-dev` packages but keeps one
   CGO job for `-race`; arm64 `.deb` as a local target; AGENTS.md rule; `docs/desktop.md`; `docs/roadmap.md`.
3. Note for the GoTHW reference: the hidden titlebar, `hideMenuBar` no-op, purego.
Gates: all of the above on the final tree, `make deb`, `dpkg-deb -I`, install in a clean container if one is at hand.

## Shared files
| File | Order |
| --- | --- |
| `desktop/main.go`, `bridge.go` | X1, X4 |
| `desktop/tray.go` | X3, X4 |
| `desktop-shell.js`, `pages-desktop.css` | X2, X4, X5 |
| `Makefile`, `desktop/probe/exit-run.sh` | X1, X4, X6 |
| `motion_test.go`, `NOTICES.md` | X2, X4 |
| `docs/roadmap.md` | each at merge |

## Risks
- **Hidden titlebar breaks on another compositor**: two bars, or none to drag. X1 checks X11; fallback flag `TENDSHELF_NATIVE_FRAME=1` shows GTK's own bar.
- **WebKitGTK crash through purego's signal handling** (SIGUSR1 is JSC's GC): the hour's playback in X6 is the test, CGO stays until it passes.
- **Wails moves weekly**: pinned; an upgrade reruns `make desktop-exit`. `hideTitlebar` depends only on `NativeWindow()`.
- **Experimental purego changes shape**: a build tag returns to CGO, and our purego file works there too.
- **`desktop-shell.js` passes 2.4 KB**: report, do not squeeze. X5's drop could load separately.
- **Sound modes also touches budgets**: the split lands first or the two reconcile `motion_test.go` at merge.
- **Back and Forward with htmx 4 history**: Tested on real boosted navigation in X2; a page that replaced state must not count.

## Out of scope (with reason)
Mini player, global shortcuts, own MPRIS, splash window, Open with Tendshelf, dock badges, auto-start, deep links, updater, Wake: reasons in 0023 point 7 and 9.
macOS and Windows chrome: `MacTitleBarHiddenInset` and `--wails-non-client-region` need their own check; the latter needs the runtime in pages, a separate choice.
Flatpak, Snap, AppImage; offline in the window: 0020.
