# M6 Desktop (plan, 2026-11-05)

Decision and evidence: [0020](../decisions/0020-desktop-shell.md); read its table first. It overrides the GoTHW wiring.
Parallelism (at most 3): **D1 + D2** together, then **D3**, then **D4**, then **D5 + D6** together, then **D7** alone.
Reused: `web.NewHandler`, `auth.SafeNext`, setup code, `offline.js` guard (:240, inert until now), the player's own
buttons, `ts_` cookie pattern, `/healthz`. Extended: `Page`, `Head`, Makefile. New: `internal/app` (moved code),
`internal/desktopshell`, `desktop/`. Replaced: nothing. Corrected premises: the worker guard exists; an isolated
display exists (private `gnome-shell --headless`, no sudo); Wails `ExecJS` queues until `wails:runtime:ready`.

## Invariants (all packages)
1. `CGO_ENABLED=0 make build` and `make check` at the root stay green; the root module never imports `desktop/` or Wails.
2. Browsers see no change: without `ts_shell`, output is byte-identical apart from `/healthz` (`make visual-compare`).
3. CSP is unchanged in every mode. No inline script or style. `player.js` and core CSS gain 0 bytes.
4. Pages load only from an http(s) origin; only the picker uses `wails://`. No Wails services are registered.
5. No service worker inside the window, ever. A server without `shell` support is refused.
6. No new authentication path. Personal mode needs sign-in like any server.
7. Go honours only `tendshelf:` messages from the configured origin; only `http`, `https`, `mailto` open externally.
8. Tendshelf downloads nothing. The shell's only own request is `GET <server>/healthz`, with no credentials.
9. Personal data never shares the server's default data folder. One app instance only.

## Names (fixed)
App ID `org.tendshelf.Tendshelf`; program `tendshelf-desktop`; build tags `production`; module
`github.com/Tend-Stack/tendshelf/desktop`; events `ts:desktop` (`detail`: `toggle`, `back`, `forward`); messages
`tendshelf:ready`, `tendshelf:open <url>`. Paths (`TENDSHELF_DESKTOP_HOME` replaces all with `<h>/config`, `<h>/library`,
`<h>/shell`): config `os.UserConfigDir()/tendshelf-desktop/desktop.json` (0600, atomic); library
`<XDG_DATA_HOME or ~/.local/share>/tendshelf-desktop/library`; cookies `.../tendshelf-desktop/shell/cookies.sqlite` (dir 0700).
Config: `{"schema":1,"mode":""|"personal"|"connect","server":"https://…","port":0}`; unreadable file is renamed `.bad`
and treated as first run.

## D1 `internal/app` (Go; about 80 calls; parallel with D2). Prefix `TestAppRun`
Pure move, no behaviour change. Keep test bodies.
1. `internal/app/app.go`: move `run` from `cmd/tendshelf/main.go`, from the data folder onward (flags stay), with `deps`, `locateTools`,
   `describeOpenError`, `setupURL`; `internal/app/hooks.go`: from `cmd/tendshelf/hooks.go` except flag and env helpers.
   `internal/app/resetpw.go`: `ResetPassword(ctx, args, out) error` from `resetpw.go`.
   ```go
   type Config struct {
       Addr string; Listener net.Listener // Listener wins when set; Run closes it
       DataDir, FFprobe, FFmpeg string; TrustedProxies auth.Proxies
       CompatCacheGiB int; Waveforms bool; Version string; Logger *slog.Logger
       Ready func(Info) // once, after the listener is bound and the runner has started
   }
   type Info struct { Addr net.Addr; SetupURL string; ToolsMissing bool } // SetupURL empty once an owner exists
   func Run(ctx context.Context, cfg Config) error
   ```
2. `cmd/tendshelf/main.go`: flags, env, `defaultDataDir`, then `app.Run`; `serving` stays, called from `Ready`.
   `hooks_media_test.go`, `tools_test.go` and `TestSetupURL` move with their code.
3. Tests: `TestAppRunGivenListener`, `TestAppRunReadyInfo` (SetupURL before and after an owner),
   `TestAppRunToolsMissing`, `TestAppRunTwice` (same process, same folder: the mode switch needs it).
Gates: `make check`, `make build`, `go run ./cmd/tendshelf -version`.

## D2 Shell marking in the web layer (Go, templ, CSS, JS; about 150 calls; parallel with D1). Prefix `TestShell`
1. `internal/web/shell.go` (new): `shellCookie = "ts_shell"`; `shellFrom(r)` returns `desktop|ios|android` or `""`;
   `SecurityHeaders` exports `securityHeaders` (D3 needs it);
   `(s *server) shellLink(next)`, for GET `/` with a `shell` query only:
   | Input | Result |
   | --- | --- |
   | `Sec-Fetch-Site` present and not `none` | ignored, falls through |
   | value outside `desktop, ios, android, browser` | ignored, falls through |
   | valid | cookie (Path=/, HttpOnly, Lax, `Secure` from `s.proxies.Secure(r)`, one year), 303 to `auth.SafeNext(next)` |
   | `browser` | deletes the cookie, 303 as above |
2. `internal/web/server.go`: `outer.Handle("/", s.shellLink(guarded))`; `/healthz` adds `"app":"tendshelf"`, `"shell":"1"`;
   `pageCached` and `frame` pass `Shell: shellFrom(r)` and set `NoInstall` when it is non-empty. Same at `routes_link.go:55`.
3. `internal/web/components/layout.templ`: `Page` gains `Shell string`, `NoOffline bool`; `Layout`, `frame.templ`,
   `link_shell.templ` write `data-shell` when set. `Head`: when `Shell == "desktop"`, link `css/pages-desktop.css` and
   `<script type="module" src=js/desktop.js>`. `offline.templ`: `OfflineScript` skips when `NoOffline`.
4. `internal/web/static/js/desktop.js` (new, readable, commented; exits at once when `webkit.messageHandlers.external`
   is missing, so a spoofed browser only loses offline):
   post `wails:runtime:ready` then `tendshelf:ready`; capture-phase click on `a[href]` whose origin differs from
   `location.origin`: `http`, `https`, `mailto` post `tendshelf:open <href>` and `preventDefault`; `contextmenu`:
   `preventDefault` unless inside `input, textarea, [contenteditable]` or the selection is not collapsed;
   `ts:desktop` clicks `#player [data-ts-toggle]`, `[data-ts-back]` or `[data-ts-forward]` (no-op when `#player` is hidden).
5. `internal/web/static/css/pages-desktop.css` (new): under `html[data-shell="desktop"]`, `cursor: default` on controls,
   rows and cards (text links keep the hand); `a[href="/me/downloads"] { display: none }`.
6. `offline.js`: first line of `needsDownloads` returns false under `data-shell`. `motion_test.go`: row
   `"desktop.js": {1*1024 + 256, 16 * 1024}`.
7. `pages/downloads.templ`, `downloads_copy.go`: `DownloadsView.Shell` renders only the title and "Downloads are for
   your phone or browser. This app plays straight from your library or server."
8. Tests: `TestShellLinkTable` (each row, `next=//evil`, and `next=/setup?code=x` surviving intact), `TestShellCrossSiteIgnored`, `TestShellAttrOnAllFrames`,
   `TestShellNoCookieNoMarkup`, `TestShellHealthz`, `TestShellDownloadsPage`, `TestShellOfflineGuards` (text checks of both
   guards), `TestShellCSPUnchanged`, `TestShellBrowser` (Chromium, 390 and 1440: `data-shell`, no worker registered,
   Downloads hidden; `desktop.js` exits without the handler).
Gates: `make generate`, `make check`, `make build`, `make visual-compare`, `make a11y` on touched pages.

## D3 Start page and server check (Go, templ; about 120 calls; after D1, D2). Prefix `TestDesktopStart`, `TestDesktopCheck`, `TestDesktopConfig`
Root module, pure Go, so the root gate covers it.
1. `internal/desktopshell/config.go`: `Paths()`, `Load`, `Save` as in Names.
2. `internal/desktopshell/check.go`: `NormalizeServer(string) (origin string, err error)`: no scheme means `https`; only
   `http`, `https`; rejects user info, query, fragment, any path except `/`; lower-cases the host; strips a trailing slash.
   `CheckServer(ctx, origin) error`: own `http.Client`, 5 s, no jar, redirects not followed, 4 KiB limit.
   | Case | Error | Words |
   | --- | --- | --- |
   | bad address | `ErrAddress` | "Enter the address you use in your browser, like https://books.example.com." |
   | no connection, timeout | `ErrUnreachable` | "Couldn't reach that server. Check the address and that it's running." |
   | certificate error | `ErrCertificate` | "That server's certificate isn't trusted, so Tendshelf won't connect." |
   | not our JSON, or a redirect | `ErrNotTendshelf` | "That address doesn't look like a Tendshelf server." |
   | `status` and `version` but no `shell` | `ErrOldServer` | "That server needs updating before this app can use it." |
3. `internal/web/pages/desktop_start.templ`: in `components.Frame`, with `Shell:"desktop"`, `NoOffline:true`, `NoInstall:true`.
   Copy: "How do you want to use Tendshelf?"; "Just on this computer" / "Your books stay here."; "Connect to a server" with
   field "Server address". Plain `http://` on a non-private host shows "Your password would travel unencrypted."
4. `internal/desktopshell/start.go`: `New(Options{Starter, Assets}) http.Handler`, wrapped in `web.SecurityHeaders`:
   `GET /` (picker; `?change=1` keeps it up over a saved mode), `POST /start/personal`, `POST /start/connect`,
   `GET /static/`. `Starter` is `StartPersonal() error`, `Connect(origin string) error`, `State() State`
   (`Idle`, `Busy`, `Failed{Words}`). Forms carry a random per-process token (constant-time compare); a miss is 403. Busy
   shows "Opening your library…". Forms are plain POST, no JS.
5. `AGENTS.md`: the two rule edits in decision 0020. `docs/architecture.md:549-557`: replace "See TendVox…" with the 0020 summary.
6. Tests: `TestDesktopConfigRoundTrip`, `TestDesktopConfigCorrupt`, `TestDesktopCheckTable` (`httptest`, TLS server for the
   certificate row), `TestDesktopCheckNormalize`, `TestDesktopStartForms`, `TestDesktopStartBadToken`,
   `TestDesktopStartFailedWords`, `TestDesktopStartCSP`, `TestDesktopStartVisual` (four themes at 390, 1024, 1440).
Gates: `make generate`, `make check`, `make build`.

## D4 `desktop/` module, both modes (Go, cgo; about 200 calls; after D3). Prefix `TestWindow`, `TestBridge`
1. `desktop/go.mod`: `go 1.27`, `require github.com/wailsapp/wails/v3 v3.0.0`, root `v0.0.0`, `replace … => ../`. Offline,
   seed the require block from `~/Projects/TendVocal/cmd/tendvocal-desktop/go.mod`. `NOTICES.md` row (MIT, desktop only).
2. `desktop/main.go`: `var version = "dev"`; `reset-password` in `os.Args[1]` calls `app.ResetPassword` with the personal
   library; then `initWebKit`, **before** `application.New` (probe order). `application.Options`: `Name` "Tendshelf",
   `Assets.Handler` = `desktopshell.New`, `SingleInstance` (restore and focus), `Linux{ProgramName, ApplicationID}`,
   `RawMessageHandler`, `OnShutdown` calls `session.Stop`. Window 1280x820, min 390x600, no `URL`, `BackgroundColour` = dark
   `--ground`, `ZoomControlEnabled`, `UseApplicationMenu`, `KeyBindings` zoom. No `JS` or `CSS` options, no `Frameless`.
3. `desktop/webkit_linux.go` (`//go:build linux && !gtk3`, `#cgo pkg-config: webkitgtk-6.0`), `webkit_other.go` (no-op):
   on `webkit_network_session_get_default()`, `webkit_cookie_manager_set_persistent_storage(…, SQLITE)`, and
   `download-started` connecting `decide-destination`, which sets `<XDG download dir or home>/<name>`, adding ` (1)`
   before the extension when taken. Probe-proven; about 40 lines. Folder 0700 is made first.
4. `desktop/session.go` implements `Starter`; one session at a time; `Stop` cancels and waits 15 s.
   Personal: `net.Listen("tcp4", "127.0.0.1:<saved>")`; busy or 0 means `:0`, save the port; `app.Run` with the
   library path, `Listener`, `Version`, media defaults (5 GiB, waveforms on). On `Ready`: `SetURL(origin +
   "/?shell=desktop")`, adding `&next=` (escaped path and query of `SetupURL`) when present; `ToolsMissing` shows one native
   warning: "Tendshelf needs ffmpeg to read audio files. On Ubuntu: sudo apt install ffmpeg. Until then, books are listed but
   won't play." A failed `Run` (including `ErrSchemaTooNew`) is `Failed{Words}`.
   Connect: `CheckServer`, then `SetURL(origin + "/?shell=desktop")`; failure at launch shows the picker with the words.
   Launch with a saved mode starts it straight away; first run shows the picker.
5. `desktop/bridge.go`: `allowOpen(allowedOrigin, currentURI, target string) bool`: `currentURI` (`OriginInfo.Origin`; ignore
   `IsMainFrame`, it read false for the main frame in the probe) has prefix `allowedOrigin + "/"`; target parses, at most 2,048 bytes, scheme allowed. `tendshelf:ready` sets `ready`; `send(cmd)`
   drops commands until ready, then `ExecJS` of a fixed `ts:desktop` dispatch (values from a constant list, never page data).
6. `desktop/menu.go`: menu "Tendshelf": "Change mode or server…" (`SetURL("/?change=1")`), "Quit"; `EditMenu` role; "View":
   Zoom in, Zoom out, Actual size, Ctrl +, -, 0. All actions are plain funcs so tests reach them.
7. `Makefile`: `desktop` (`go -C desktop build -trimpath -tags production -ldflags "-X main.version=$(VERSION)" -o
   ../bin/tendshelf-desktop .`), `desktop-check` (gofmt on `desktop/`, vet, `test -race -tags production ./...`), `isolated`.
8. `scripts/isolated-display.sh CMD…`: `mktemp -d /tmp/ts-XXXXXX` for `XDG_RUNTIME_DIR` (the socket path must stay under 108
   bytes), private `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME`, `GSETTINGS_BACKEND=memory`; unset `DISPLAY`,
   `WAYLAND_DISPLAY`; `dbus-run-session -- gnome-shell --headless --wayland --no-x11 --wayland-display=ts-desktop
   --virtual-monitor 1280x800 --unsafe-mode`; wait for the socket; run CMD with `WAYLAND_DISPLAY=ts-desktop
   GDK_BACKEND=wayland`; stop by PID (never `pkill -f`); remove the folder; exit with CMD's status. Shell log to a file.
9. Tests: `TestBridgeAllowOpen` (table: wrong origin, `file:`, `javascript:`, oversize, picker page), `TestBridgeUnknownMessage`,
   `TestWindowSendBeforeReady`, `TestWindowSessionSwitch` (fake `Starter` calls), `TestWindowPortBusy`.
10. `desktop/probe/` (tag `tendshelf_probe`): Go runs steps by `ExecJS` (which works once ready) and gets replies as
    `tendshelf:probe <id> <json>`, writing `.local/tests/desktop/<run>/result.json`. Personal script: first run, setup, sign
    in, add a fixture library made by ffmpeg, muted play, seek. D7 runs the full set.
Gates: `make check`, `make build`, `make desktop`, `make desktop-check`, `scripts/isolated-display.sh` running the personal probe.

## D5 Media keys and tray (about 120 calls; after D4; parallel with D6). Prefix `TestKeys`, `TestTray`
1. **Probe first, build second.** In a `tendshelf_probe` build, play unmuted, then run `gdbus` ListNames for
   `org.mpris.MediaPlayer2.*`, read `Metadata`, call `Pause`. Pass: a name exists and the page's `pause` action runs. The
   isolated session needs a sink: try private `pipewire` and `wireplumber` with a null sink (`pw-cli`) in the script; the 2026-11-05 attempt to borrow
   the desktop's PipeWire left `play()` pending. If no sink works, ask the operator for one check: `make desktop`, play a
   book, press the key. Also record whether tray Play works before any click (`play()` from `ExecJS`).
2. **If it passes:** no code; note it in `docs/manual-checks.md`.
3. **If it fails (fallback, about 180 lines of Go, no `player.js` change):** `desktop/mpris_linux.go` owns
   `org.mpris.MediaPlayer2.tendshelf` with godbus (already in Wails' graph; direct require, `NOTICES.md` row): `PlayPause`,
   `Play`, `Pause`, `Next` (forward), `Previous` (back) through `send`; `PlaybackStatus` from a new `tendshelf:state` message
   posted by `desktop.js` on `playing` and `pause` (raise its row to 1.375 KB). Not Wails `GlobalShortcut`: its key list has no
   media keys, and Wayland needs the person to bind through a portal.
4. `desktop/tray.go`: `app.SystemTray.New()`, icon from `static` `img/icon-192.png`, menu "Show Tendshelf", "Hide
   Tendshelf", "Play or pause", "Quit Tendshelf"; click toggles the window. Closing the window quits, so a desktop with no
   tray never strands a hidden app. GNOME needs an AppIndicator extension (Ubuntu ships one).
5. Tests: `TestTrayActions`, `TestKeysCommandList`, and with the fallback `TestKeysMprisMethods`.
Gates: `make desktop-check`, `make desktop`; start with no StatusNotifier watcher and confirm no crash.

## D6 Packaging and CI (about 80 calls; after D4; parallel with D5). Prefix `TestPackage`
1. `desktop/package/build-deb.sh`, `tendshelf-desktop.desktop`, `control.in`: `bin/tendshelf-desktop`, `.desktop`, icon from
   `internal/web/static/img/icon-512.png`, via `dpkg-deb`. `Version:` is the plain `YEAR.RELEASE.FIX` of the last tag; the binary
   embeds `git describe --tags --long --dirty`; the file is `tendshelf-desktop_<describe>_amd64.deb`. `Depends: libgtk-4-1,
   libwebkitgtk-6.0-4, gstreamer1.0-plugins-good, gstreamer1.0-libav, ffmpeg`. Recommends `gnome-shell-extension-appindicator`.
   Reason for a `.deb`, no AppImage: GTK and WebKit are system libraries, and apt brings ffmpeg and the AAC decoder.
2. `Makefile`: `deb`. Plain `go build`, no `wails3` CLI, no Taskfile.
3. `.gitea/workflows/desktop.yml` (new, separate so `ci.yml` stays CGO-free; `paths:` `desktop/**`, `internal/app/**`,
   `internal/desktopshell/**`, `go.mod`, itself): same runner, image and action pins as `ci.yml`; apt `make gcc pkg-config
   libgtk-4-dev libwebkitgtk-6.0-dev`; `make desktop-check`, `make desktop`. Guess: 24.04's GTK and WebKit compile Wails v3.
   If not, use a 26.04 image; report it. No nightly row, no windows, no packaging in CI.
4. `docs/desktop.md` (new, short): what to install, modes, the `WEBKIT_DISABLE_DMABUF_RENDERER=1` workaround, limits; macOS and
   Windows "not verified". `README.md` link.
5. Test: `TestPackageControl` (Go, in `desktop/`: `control.in` fields, version parse).
Gates: `make deb`, `dpkg-deb -I` and `-c`, the workflow green after the orchestrator pushes.

## D7 Exit run (about 80 calls; last, alone)
1. Add to `desktop/probe/` and run through `scripts/isolated-display.sh`, both modes (connect against `bin/tendshelf
   -addr 127.0.0.1:18096`), on a `production` build:
   `data-shell="desktop"` (this proves `Sec-Fetch-Site` on `SetURL`); no worker registered; Downloads hidden; muted play and
   seek on `/stream`; CSP header identical to the server's; an external link reaches `tendshelf:open`; an attachment lands in
   the download folder; quit and relaunch keeps sign-in; a second launch focuses the first; SIGTERM stops cleanly; the mode
   switch stops personal jobs. Screenshots through `org.gnome.Shell.Screenshot`.
2. Try enabling `ubuntu-appindicators@ubuntu.com` in the private session to check the tray over D-Bus; otherwise manual.
3. Reader check (`docs/manual-checks.md:119-135`): in the Wails build with a synthetic EPUB, drive rows that a script can
   (open, page turns, Aa, marks, footnote, search) and fill the WebKit column. Leave touch and eye rows for the operator.
   Run `scripts/isolated-display.sh make visual-webkit`; fix the Makefile comment at :101.
4. `docs/manual-checks.md`: new section for the operator's own desktop: real key, tray, audible playback, https server,
   upload file chooser, drag-free title bar. `docs/roadmap.md` M6: status, checks, limits; other platforms "not verified".
Gates: `make check`, `make build`, `make desktop-check`, all probe steps pass or are recorded with a reason.

## Shared files
| File | Package order |
| --- | --- |
| `Makefile` | D4, D6, D7 (D5 none) |
| `desktop/main.go` | D4, then D5 (one `tray.Setup` line); D6 none |
| `internal/web/static/motion_test.go`, `desktop.js` | D2, D5 fallback only |
| `NOTICES.md` | D4, D5 fallback only |
| `docs/roadmap.md` | each at merge; orchestrator resolves |
D1 and D2 share no file: D1 owns `cmd/tendshelf`, `internal/app`; D2 owns `internal/web`. Prefixes above partition test names.

## Out of scope (with reason)
- macOS and Windows builds: code stays portable, unverified by the roadmap's exit.
- Offline in connect mode, and any service worker: rule, and decision 0020.
- A reverse proxy, a media gateway, Wails runtime in pages, hidden title bars: decision 0020.
- Close to tray, folder picker for libraries, auto-update, autostart, file association: later, separate choices.
- Saved-file confirmation in the app; back and forward keys: small, later.
- Self-signed certificates: WebKit refuses; the check says so in plain words.
- Flatpak, Snap, AppImage; bundled ffmpeg (not "nothing to keep up to date", and a licence question).
- Using the personal library from a phone: it binds to loopback; run the server for that.
