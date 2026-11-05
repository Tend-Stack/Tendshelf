# 0020 The desktop shell loads an http origin, not Wails' asset scheme
Status: proposed (2026-11-05). Plan: `docs/plans/m6-desktop.md`.

## Context
`docs/architecture.md` says personal mode runs "the same core in-process"; the GoTHW Wails reference passes the
handler to `AssetOptions.Handler` and, for a remote server, reverse-proxies it on the same `wails://` origin. Decision
0011 reserved `data-shell` from a `ts_shell` cookie plus Wails' `--wails-draggable` and `--default-contextmenu`.
A throwaway probe tested those assumptions. Setup: Wails v3.0.0, GTK 4.22.4, WebKitGTK 2.52.6, `-tags production`,
Tendshelf's CSP, 180 s AAC and MP3 files, a private headless GNOME Shell, two launches of each case (2026-11-05).

| Question | `wails://localhost` (asset handler) | `http://127.0.0.1:port` loaded in the window |
| --- | --- | --- |
| `Set-Cookie` kept and sent back | **No**: dropped, `Cookie` always empty | Yes |
| Cookie survives a restart | Not applicable | **No** by default; **yes** with one cgo call (below) |
| `<audio>` AAC and MP3 | **Fails**: `MEDIA_ERR_SRC_NOT_SUPPORTED` after the first Range request | Metadata, muted play and seek to 120 s all pass; Range and Cookie sent |
| `Origin`, `Sec-Fetch-Site` on POST | Both absent | Both present |
| `isSecureContext` | true | true |
| Cache API `put` | Fails: "Request url is not HTTP/HTTPS" | Works (so the worker guard really matters) |
| `/wails/runtime.js` | Loads | **Fails**: not served on this origin |
| `webkit.messageHandlers.external` | Not tested | Works under the CSP, page to Go; `wails:runtime:ready` releases `ExecJS`, which then reaches the page |
| `attachment` response | Not tested | **Silently lost**; saved once `download-started` is handled (cgo) |
| Unmuted `play()` with no gesture | Not tested | `NotAllowedError` (WebKit's default autoplay policy; muted passes) |
| `play()` from `ExecJS` | Not tested | Not refused, but never started: the private session had no audio sink. **Open** |
| WebKit publishes an MPRIS player | Not tested | **Inconclusive**: no name appeared, but playback never really started |

Limits of this evidence: the http column used a loopback origin. An https server should behave the same, but that is a
guess until the operator's check (plan D7). The `wails://` audio failure is what TendVocal's loopback media gateway
already works around, on GTK 3.

## Decision
1. **Pages always load from an http(s) origin.** The Wails asset handler serves only the first-run picker. A reverse
   proxy on `wails://` would need a Go cookie jar and a media gateway, and that gateway needs a CSP change, so it fails
   "CSP unchanged".
2. **Personal mode is in-process, on loopback.** The desktop module imports `internal/app` and serves it on
   `127.0.0.1:<port>`. The port is random once, then saved, because `localStorage` is keyed by origin. If it is busy,
   a new one is chosen. No child binary: one process, one app, and the server's default `127.0.0.1:8095` is never used.
3. **Connect mode loads the server directly** in the window (`https://server/`). No proxy, no rewriting of cookies,
   `Host` or the CSP. This matches `docs/architecture.md` and overrides the GoTHW reference's advice for Tendshelf.
4. **`data-shell` comes from the `ts_shell` cookie in both modes** (0011's design), because a remote server cannot see a
   wrapper in the desktop process. `/?shell=desktop` also takes `next` (through `auth.SafeNext`). It is ignored when
   `Sec-Fetch-Site` is present and not `none`, so a link on another site cannot switch off someone's offline features.
   `shell=browser` clears it. Spoofing it changes only presentation and offline features.
5. **No Wails runtime in pages; this supersedes part of 0011.** `--default-contextmenu` and `--wails-draggable` need the
   runtime, so neither is used. The window keeps its native title bar. The context menu is handled by `desktop.js`
   (`preventDefault` on chrome, kept on fields and selections), loaded only under `data-shell="desktop"`.
6. **One bridge.** Page to Go is `webkit.messageHandlers.external`, Go to page is `ExecJS` dispatching a `ts:desktop`
   event. No Wails services are registered, so a page has nothing to call. Go accepts only `tendshelf:` messages from
   the configured origin. `wails:` messages reach Wails itself and can only drag or resize the window, or mark the runtime
   ready; `AllowSimpleEventEmit` stays off, so a page cannot raise host events.
7. **Two small cgo calls in `desktop/` only:** persistent cookies (`webkit_cookie_manager_set_persistent_storage`) and
   downloads (`download-started`). Wails exposes neither. Cookies live in a 0700 folder.
8. **Desktop means online.** No service worker, no Downloads. Personal mode has the files already, and connect mode
   needs its server. `offline.js` still loads, for the banner and the retry queue. Downloads links are hidden by CSS.
9. **ffmpeg stays external.** The `.deb` depends on `ffmpeg` and the GStreamer codec packages, so apt installs them. A
   bare binary without ffmpeg warns once in a native dialog and lists books unread. Tendshelf downloads nothing.
10. **Isolated checks use a private `gnome-shell --headless`** under `dbus-run-session`. It needs no install and no sudo.

## Rule changes (AGENTS.md, applied in package D3)
- Under "Security and privacy", after the SMTP sentence: "The desktop shell also checks `/healthz` on the server the
  person typed, at Connect and at launch (`internal/desktopshell`, decision 0020). That is its own client, and it sends
  no credentials."
- Under "Stack constraints", in the Wails line: "Pages load from an http(s) origin, never Wails' asset scheme
  (decision 0020)."

## Budgets to approve
`desktop.js` 1.25 KB gzip (loaded only in the shell, or 1.375 KB if the MPRIS fallback in D5 is needed). `pages-desktop.css`
uses the standard 3 KB `pages-*` row. Core CSS and `player.js` are unchanged. `offline.js` stays in its 3.5 KB row.

## Consequences
- A server must be at this version or newer: `/healthz` gains `app` and `shell`, and an older server is refused in plain
  words. Otherwise it would ignore `?shell=` and register its worker inside the window.
- Personal mode keeps sign-in. There is no automatic login and no new authentication path. The first run opens the
  existing setup page with its code filled in, and a forgotten password uses `tendshelf-desktop reset-password`.
- Personal mode and a server on this same machine share `127.0.0.1` cookies, because cookies ignore ports, so switching
  between them signs the person out.
- The GoTHW reference (sections 3 and 10) needs a note: on WebKitGTK 6 its default wiring has no cookies and no audio.
