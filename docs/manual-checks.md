# Manual checks

Checks that need a real device. Automated tests cover the rest; this file is for what a headless
browser cannot say (touch feel, one-handed reach, the lock screen, shake, the phone's own audio rules).
Run a check, write what you saw in the Result column, and date it under the table.

## P1c player: one-handed phone check

**Setup.** Start the server so the phone can reach it: `tendshelf -addr 0.0.0.0:8095` on the same Wi-Fi
(the default `127.0.0.1:8095` is not reachable from a phone). On the phone open
`http://<computer's address>:8095`, sign in, and use a library that has a book with chapters and, if you
can, one that spans more than one audio file. Hold the phone in one hand at its normal 390 px-wide
portrait layout, and reach with the thumb only.

**Plain http on the LAN.** Phone browsers give motion sensors only to secure pages (HTTPS or localhost), so over `http://<address>:8095` shake (step 9) cannot work: write "skipped (http)" there, or reach the server through HTTPS (for example a reverse proxy you already run) to check it. The other steps work over http.

**Browsers.** Run the whole table on Android Chrome and again on iOS Safari. Note the browser and
version next to the result. Some steps are known to differ: iOS ignores volume changes from the page, and
shake needs a motion permission prompt on iOS.

| # | Step | Expected | Result |
| --- | --- | --- | --- |
| 1 | Open a book from Home or Library. | The book page shows one play button (Resume, Start listening or Listen again), chapters and a Bookmarks tab. | Pass |
| 2 | Tap the play button on the book page. | Sound starts within about a second. The mini-player appears above the tab bar with the cover, title, chapter and a progress line. Only one resume button is on screen. | Pass |
| 3 | Pause and play from the mini-player with your thumb. | The button toggles, the icon changes, and it is at least thumb-sized. Back and forward skip by the seconds on the icons. | Pass |
| 4 | Tap the mini-player's title area. | The full player opens full screen. The play button, skip buttons, scrubber and the Speed, Sleep and Chapters bar are all in the lower part of the screen, reachable without shifting your grip. | Pass (rechecked after the player-fit fix) |
| 5 | Skip back and forward, then go to the next and previous chapter. | Each tap moves by the shown seconds or to the chapter start; the clock and "left in chapter" change; "Chapter N of M" follows. | Pass |
| 6 | Drag the scrubber across a chapter boundary (and across a file boundary if the book has two files), then let go. | A bubble shows the chapter and time while you drag; dragging upward slows the drag (fine control). Playback resumes at the dropped place, with no gap or wrong chapter. | Pass |
| 7 | Open Speed, pick 1.5x, then set an in-between speed with the slider. | The speed shows on the bar; the voice is faster and not pitched up; "left in book" shrinks. | Pass |
| 8 | Open Sleep, choose 15 minutes. Then set a short custom timer (1 minute) and wait for it. | The Sleep item shows the time left. In the last ten seconds the volume fades and a card says "Sleeping in N seconds" with "+10 min". Tapping "+10 min" removes the card and brings the volume back. Left alone, playback stops at the end. | Pass |
| 9 | Start another short timer, and during the fade shake the phone (on iOS, allow motion access when asked). | The shake adds ten minutes like the button, and the card goes. (On Android Chrome, shake works without a prompt.) | Skipped (http: motion sensors need HTTPS) |
| 10 | Tap the bookmark button in the player (on a computer keyboard it is the B key). Then add a note in the toast that appears. | The toast confirms the bookmark at the current time and offers a note. The note saves. The bookmark and note are on the book page's Bookmarks tab. | Pass (rechecked after the toast fix) |
| 11 | Lock the phone while playing. Use the lock screen or notification controls: play, pause, skip back, skip forward, next chapter. | The lock screen shows title, author, chapter and cover (Media Session), and each control works. Sound keeps playing with the screen off. | Pass |
| 12 | Unlock, close the full player with the down arrow, and navigate to another page. | The player closes, the mini-player stays, and the sound does not stop or restart when you move between pages. | Pass |
| 13 | Pause on the phone and note the time. On a second device (a computer browser signed in as the same person) open the same book and press Resume. | The second device starts within a few seconds of where the phone stopped (a short rewind after a pause is normal). | Pass |
| 14 | On the second device listen on for at least two minutes and pause. Return to the phone, where the book is still loaded, and press play (reopen the tab first if the phone had put it to sleep). | A message says "You were at <time> on <device>. Continue there?" with Continue there and Stay here. Nothing moves until you choose. Continue there jumps to the second device's place; Stay here keeps the phone's own place. | Pass |

Dated runs (browser and version, date, who ran it, anything that surprised you):

- 2026-11-02 and 2026-11-03: see the run below.

## Run 2026-11-02 (operator, Android Chrome over http on the LAN, build bea2035)

Steps 1-8 and 10-14 pass; step 9 (shake) skipped because motion sensors need HTTPS. Found during the
run and fixed in the next build: the bottom tab bar drifted while scrolling; the full player did not fit
the screen (Speed/Sleep/Chapters needed a scroll); pulling the chapters sheet down refreshed the page
instead of closing it; the "Note saved" toast stayed on screen. All four were fixed (5f8ab4d) and rechecked on
the phone by the operator on 2026-11-03. iOS Safari not yet checked.

## M3a offline pocket: airplane-mode check (Android Chrome, iOS Safari)

Closes the M3 exit: a downloaded book plays in airplane mode, and offline progress syncs on reconnect.
Plan: [m3-offline.md](plans/m3-offline.md); decision [0012](decisions/0012-offline-pocket.md).

**Setup (HTTPS is required).** A service worker does not run on `http://<LAN address>`, so Downloads would
say it needs a secure address. Give the phone an https address with Tailscale. Start Tendshelf on its
default `127.0.0.1:8095` (the server must be reachable there; Tailscale connects to it on this machine), then
run, as a suggestion to verify against your Tailscale version (`tailscale serve --help`):

    tailscale serve --bg --https=443 http://127.0.0.1:8095

Tailscale prints the `https://<machine>.<tailnet>.ts.net` address; open it on a phone that has Tailscale
running and is signed in to the same tailnet. Stop sharing afterwards with `tailscale serve --https=443 off`
(check the form with `tailscale serve --help`). Use a book with two or more audio files, and a second device
signed in as the same person for step 7. Run everything twice: Android Chrome (a tab, then installed) and
iOS Safari (a Safari tab, then the Home Screen app).

| # | Step | Expected | Android Chrome | iOS Safari tab | iOS Home Screen app |
| --- | --- | --- | --- | --- | --- |
| 1 | Install: Android, the Install Tendshelf button on Downloads or the browser menu; iOS, Share, Add to Home Screen. | The app opens in its own window with the Tendshelf icon (not a letter on a plain tile) and the name Tendshelf. On iOS Safari tab, Downloads shows the line about Safari clearing downloads after a week. | | | |
| 2 | Open a multi-file book, press Download. Watch Downloads: progress row, Pause download, Resume download. Keep the app open until it finishes. | The row shows "{done} of {total}", the bar moves, Pause then Resume continue where it stopped, the end shows "{title} is ready offline." and a Ready offline row. | | | |
| 3 | Open Downloads. | Storage numbers show; the "may clear downloads" line shows or not as `persist()` says. Write both below. | | | |
| 4 | Turn on airplane mode. Close the app and open it cold. | The offline page shows: banner "You're offline...", "On this phone" with the book as Ready offline, others as Needs a connection. | | | |
| 5 | Press play, seek into the second file, lock the phone for 2 minutes, unlock. | Sound plays from the start, the seek lands without an error or a gap, playback is still going after the unlock (or resumes on one tap). | | | |
| 6 | Add a bookmark, then pause. | A toast says "Bookmarked at {time}. It saves when you're back online." | | | |
| 7 | Turn airplane mode off. | A "Back online. Your place and bookmarks are saved." toast; on the second device Resume starts within a few seconds of where the phone stopped, and the bookmark is in the book's Bookmarks tab. | | | |
| 8 | Sign out on the phone, then sign in again (as the same person, then as another person if you have one). | Downloads is empty after the next sign-in; nothing from the old session is left. | | | |

Record the figures: the `estimate()` quota and usage Downloads shows, and whether `persist()` was granted
(the line is absent when it was). Android: quota ____ , persist ____ . iOS Safari tab: quota ____ , persist
____ . iOS Home Screen app: quota ____ , persist ____ . (iOS figures in the plan are guesses until measured.)

Dated runs (browser and version, date, who ran it, anything that surprised you):

- 2026-11-04, operator (this is the M3a table, steps 1-8), Android Chrome tab over `http://<LAN address>:8095` with the
  "Insecure origins treated as secure" flag (not real HTTPS), build v2026.1.0-201.
  1 partial: "Install app" made a shortcut that opens in full Chrome, not an app window; expected without a
  real HTTPS address (Chrome only builds an installed app for a secure origin), recheck over Tailscale.
  2 pass. 3: storage shown 10.7 GB, the downloaded book 878 MB; the "may clear downloads" line not recorded.
  4 partial: the book played offline, but the page showed broken parts (details to collect; likely the
  shortcut opening as a Chrome tab), recheck over HTTPS. 5 pass. 6 fail, fixed: the queued-bookmark toast
  stayed until a reload (toasts inside the full player were never timed out; fixed in f8ba733 with
  `TestPlayerToastLeavesInFullPlayer`). 7 pass. 8 pass. iOS not run.
- 2026-11-05, operator, Firefox over Tailscale: the page hang is fixed by the service worker change
  (confirmed by the operator). Not a step of this table.

Automated proof of the same surfaces lives in `internal/web/offline_browser_test.go`,
`offline_downloads_browser_test.go` and `offline_proof_browser_test.go` (Chromium over CDP). What those
cannot say: a real phone's storage limits, iOS eviction, the lock screen and a real airplane mode.

## M3c keyboard and screen reader

Covers what `make a11y` cannot: real assistive technology. Status: **not run**. Plan:
[plans/m3c-audit.md](plans/m3c-audit.md); summary: [accessibility.md](accessibility.md). Run each row with Orca
and Firefox on Ubuntu, TalkBack on Android Chrome, and VoiceOver on iOS Safari. Write the result in each column.

| # | Step | Expected | Orca + Firefox | TalkBack | VoiceOver |
| --- | --- | --- | --- | --- | --- |
| 1 | Sign in. | Fields are announced with their labels; a wrong password error is announced. | | | |
| 2 | From Home, play a book with the keyboard only (or the screen reader's gestures). | One resume control, reachable, labelled; sound starts. | | | |
| 3 | Full player: every key (space, arrows, speed, sleep, chapters). | Each control works and announces its new state. | | | |
| 4 | Open a sheet, then close it. | Focus moves inside on open and returns to the opener on close. | | | |
| 5 | Trigger a toast (add a bookmark). | The toast text is announced once. | | | |
| 6 | Go offline. | The offline banner is announced. | | | |
| 7 | Submit a form with an error (Add books, Settings). | The error is announced and linked to its field. | | | |
| 8 | Browser zoom 200 % and 400 % at 1280 px wide. | No lost content, no sideways scroll, controls usable. | | | |
| 9 | Windows high-contrast / forced colours (Edge or Firefox). | Text, focus and control edges stay visible. | | | |
| 10 | Move the scrubber and the volume by keys, without dragging. | Arrow keys change the value and announce it. | | | |

## M5 book reader: WebKit and phone (RD4)

Why manual: `make visual-webkit` drives the kitchen sink and eight pages through WebKitGTK's MiniBrowser, which has no
headless mode; on 2026-11-05 the workstation had no `xvfb-run`, and running it would have put windows on the desktop, so the
reader was not driven in WebKit. Later that day (M6 D7) `scripts/isolated-display.sh` ran `make visual-webkit` in a private
headless GNOME Shell, and the desktop exit run drove the reader inside the Wails window; the WebKit column below holds what a
script could do. Chromium covers the reader (`make a11y`, `TestBRVisual`, `TestBRLayoutTime`). The reader
relies on CSS columns for pages, `CSS.highlights` for a search hit, `inert` behind a sheet, `:has()` for the hidden shell
navigation and `Element.animate` for the page slide; WebKit and the phone browsers are where those differ. Use a book from
your own folder (for example `~/Books` "Rules for the Summer", EPUB, with its audiobook in the library); never commit it.

| # | Step | Expected | WebKit (Epiphany or a Wails build) | iPhone Safari | Android Chrome |
| --- | --- | --- | --- | --- | --- |
| 1 | Open the book from its page with Read, in Pages. | Text fills columns, no clipped line at the top or bottom, no sideways scroll. | Pass, after a fix. In the Wails window (WebKitGTK 2.52, 1280 by 705) Auto opens in Scroll; with Pages, text fills the column, the first sentence is inside the box, no sideways page scroll. Before the fix WebKit made no columns at all (see below). | | |
| 2 | Turn pages with the arrow keys, the tap zones and a swipe. | One page per turn, the slide plays once, the page line changes. | Arrow keys and tap zones: pass (synthetic key and click events). Swipe: not run (touch). Whether the slide plays once: not checked. | | |
| 3 | Turn the phone sideways, then back. | The place stays on the same sentence; a second column appears only from 1024 px. | Not run: needs the window or phone turned sideways by hand. | | |
| 4 | Aa: change size, font, theme, then Scroll. | The page behind changes at once; the choices stay after a reload. | Pass for theme (Sepia) and flow (Pages): both applied at once, and Sepia was still on after leaving the reader and coming back. Size and font not driven. | | |
| 5 | Select a few words (long press on the phone). | The toolbar with four colours, Note and Copy appears above the selection; the system menu does not hide it. | Pass for a programmatic selection: the bar with four colours, Note, Look up and Copy appeared above it. Long press and the system menu: not run. | | |
| 6 | Highlight in each colour, add a note. | Washes are readable in dark, light, sepia and contrast; the note dot opens the marks sheet. | Pass for one colour (green) and one note: the wash and the note dot appeared. Other colours, the four themes' washes and the marks sheet: not run. | | |
| 7 | Open a footnote link, then close it with Escape or the backdrop. | Popup opens; focus returns to the link; the page does not move. | Pass with the backdrop: popup opened, focus went inside and came back to the link, the page did not move. Escape: not run (a script's key event does not close a dialog). | | |
| 8 | Contents, Search for a word, follow a hit. | The sheet opens over an inert page; the hit is marked on arrival. | Pass: Contents opened with the page behind inert (4 entries); Search for 'zebra' listed one hit, and following it marked the word through the highlight registry. | | |
| 9 | Close the reader and open it again from Home. | It resumes on the same sentence. | Pass: left for Home, reopened with no place in the address, same sentence on screen, Sepia kept. | | |
| 10 | Read past the end of a long chapter. | The next part loads without a jump. | Partly: 80 page turns crossed from chapter 2 into chapter 3 with every sentence in order. A visual jump cannot be seen by a script; watch it by hand. | | |

WebKit run, 2026-11-05 (M6 D7, scripted, nobody watching): Wails v3.0.0 window, WebKitGTK 2.52.6, GTK 4.22.4, in a private
headless GNOME Shell, a synthetic EPUB ("The Pager", three chapters, one footnote) beside a synthetic 180 s AAC audiobook,
1280 by 705 px, personal and connect modes alike (same answers). Finding and fix: in Pages flow with one column (every phone,
and a desktop window under 1024 px) WebKit treated `column-count: 1` as no columns, so the part's `scrollWidth` equalled one
page and the reader believed every chapter was one page: arrow keys and taps jumped chapters, and the highlight and note steps
failed. `pages-reader.css` now also sets `column-width: var(--ts-col)`; with the box exactly that wide, other engines lay out
the same columns. Chromium was not available on this machine, so the Chromium reader tests were not re-run after the change;
run `make a11y` and the reader browser tests where a Chromium exists. Not driven: touch, swipe, long press, rotation, the
Escape key, size and font in Aa, the other three highlight colours, the four themes' washes. iPhone Safari and Android Chrome:
not verified.

`make visual-webkit` (kitchen sink and eight pages through MiniBrowser 4.1) passes in the private session (10.8 s). It needed
one change to its helper: the private shell opens every window maximized and then refuses WebDriver's resize, so the helper
un-maximizes the window through the private shell's Eval call, and only when `WAYLAND_DISPLAY` is `ts-desktop`.

## Desktop media keys and tray (M6 D5, 2026-11-05)

Probe result: WebKitGTK 6 publishes `org.mpris.MediaPlayer2.org.webkit.app-<hash>.Sandboxed.instance-N` on the session bus
once a media element is really playing; `Pause`, `Play` and `PlayPause` over D-Bus changed `PlaybackStatus` as expected, so
no Go fallback was built and `player.js` is untouched. The page's own `mediaSession` handlers (play, pause, previoustrack,
nexttrack) are what WebKit calls. The isolated session had a private PipeWire null sink; the real key came from the shell's
remote-desktop API. Limit: `play()` without a click is refused by WebKit's autoplay rule (`NotAllowedError`), so the tray's
"Play or pause" can pause at any time but can start a book only after the person has pressed play once in the window.

Operator check, on the real desktop (the isolated session has no media-key handler):

1. `make desktop`, run `bin/tendshelf-desktop`, open a book and press its play button.
2. Press the keyboard's Play/Pause key. Expected: audio pauses; press again, it resumes.
3. Press Next and Previous. Expected: the chapter changes.
4. With an AppIndicator extension on (Ubuntu ships one), click the tray icon: the window hides, then shows. The menu's
   "Play or pause" pauses and resumes. Quit Tendshelf ends the app. Without the extension there is no icon and nothing else changes.

## Desktop app on your own computer (M6 exit, 2026-11-05)

Why manual: the exit run (`make desktop-exit`) drives the real window in a private headless GNOME Shell with no sound
card, no real keyboard, no real tray, no real file chooser and a loopback `http` server. It proves the plumbing; these
rows are what it cannot see. Run them on Ubuntu with `make desktop` then `bin/tendshelf-desktop`, or the `.deb` from
`make deb`. Status: **not run**. Write what you saw in the Result column and date it under the table.

Two limits the exit run found and kept, so do not be surprised by them: the tray's "Play or pause" cannot start a book
nobody has played yet in that window (WebKit refuses `play()` without a click), and the page's own media-key handlers
(`mediaSession`) have not been seen firing from a real key.

| # | Step | Expected | Result |
| --- | --- | --- | --- |
| 1 | First launch: choose "Just on this computer", finish setup, add a folder of your own audiobooks. | The setup page opens with its code filled in; books appear; no window opens anywhere else. | |
| 2 | Play a book (press its play button once), with real speakers or headphones. | Sound plays; the progress moves. | |
| 3 | Press the keyboard's Play/Pause, Next and Previous keys, with another app focused. | Pause and resume; Next and Previous change the chapter. If nothing happens, note the desktop and session type (Wayland or X11). | |
| 4 | Tray (Ubuntu's AppIndicator extension on): click the icon, then use its menu. | Click hides, then shows the window. "Play or pause" pauses and resumes a book you already started. "Quit Tendshelf" ends the app. | |
| 5 | Same, with the extension off. | No icon; nothing else changes; closing the window quits. | |
| 6 | Window: drag the native title bar, resize from every edge and corner, maximize, restore, double-click the bar. | All work like any app; nothing is "drag-free" or stuck; the window will not shrink below about 390 by 600. | |
| 7 | Menu: Tendshelf, Change mode or server…; View, Zoom in, Zoom out, Actual size (and Ctrl +, -, 0). | The picker opens and your library keeps running until you choose; zoom changes at once and resets. | |
| 8 | Connect mode against a real `https` server (a certificate from a real authority). | Sign-in works, play and seek work, reload keeps you signed in, quit and relaunch keeps you signed in. A self-signed certificate is refused with plain words. | |
| 9 | On the Add books page, upload a book with the file chooser. | The native chooser opens, the upload shows progress and the book appears. (The exit run cannot drive a chooser.) | |
| 10 | Open a link to another site from a book's details. | It opens in your own browser, not in the window. | |
| 11 | Start a second copy from the launcher while the first runs. | The first window comes forward; no second window. | |
| 12 | Quit, then start with ffmpeg missing (rename it for a minute). | One plain warning about ffmpeg; books are listed but do not play. | |

Dated runs (desktop, session type, WebKitGTK version, date, who ran it, anything that surprised you):

-

## Operator reports, 2026-11-05

- Firefox hang on Tailscale: fixed by the worker change, confirmed by the operator.
- Voice sound: it is the same in `ffplay`, so it is not the app.

## KOReader sync (S6)

Needs a real KOReader (e-reader or desktop build), an EPUB and a PDF that are in a Tendshelf library, and a `KOReader sync` key from Your devices.

| # | Step | Expected | Result |
| --- | --- | --- | --- |
| 1 | In KOReader open Progress sync, choose Custom sync server, enter the address from the key page, then choose Login (not Register) with your user name and the key. | "Logged in"; Register answers with the Tendshelf message and creates nothing. | |
| 2 | Open the EPUB in KOReader, read some pages, then use Push progress now. | In Tendshelf the book shows a reading place from KOReader, in about the same chapter. | |
| 3 | In Tendshelf read further in the same book on the web, then in KOReader use Pull progress from other devices. | KOReader offers the Tendshelf place and jumps to the start of that chapter or earlier (the known limit of the short pointer; a bad pointer leaves the page alone). | |
| 4 | Do steps 2 and 3 with the PDF. | Pushing sets the reading place; pulling jumps to the page matching the Tendshelf place. | |
| 5 | Turn the key off in Your devices, then push again. | KOReader reports that the login failed. | |

## Desktop app on your own computer (X4 to X6)

Needs the desktop build on a real Linux session with a sound card, a keyboard with media keys and a tray or panel that shows StatusNotifierItem icons, and a server with one audiobook and one ebook. Run the CGO build first (`make desktop-cgo`), then the CGO-free one (`make desktop`) for the last row. Nothing is marked passed until you run it.

| # | Step | Expected | Result |
| --- | --- | --- | --- |
| 1 | Open a book and press play in the window. | Sound comes from the speakers. The bar, the tray and the Now playing panel show the same book and chapter. | |
| 2 | Press the keyboard's Play/Pause, Next and Previous keys with the window in front, then with another application in front. | The book pauses, resumes and skips by the same seconds as the on-screen buttons, in both cases. | |
| 3 | Minimise the window (or close it with Keep playing on) and press Play/Pause. | The hidden window keeps playing sound and the key still pauses it. | |
| 4 | Click the tray icon, then Pause, Skip back and Skip forward in its menu. | The menu names the book. Each item acts once on the book. | |
| 5 | Let a short book end, or set the sleep timer to stop it, with the window behind another one. Click the notification. | A notification appears at the end. Clicking it brings the window to the front. | |
| 6 | In Files (Nautilus), drag one audiobook file onto the window, as an owner or admin of a library with uploads on. | Add books opens with the file chosen and the upload can start. | |
| 7 | Drag a whole folder onto the window. | A toast says folders are not accepted; nothing is uploaded. | |
| 8 | Drag a copy-protected file (`.aax`) onto the window. | It is refused with the Add books words and nothing is sent. | |
| 9 | Press Ctrl+Q. | The window and the tray icon close, and the process exits (no leftover `tendshelf-desktop` in the process list). | |
| 10 | Look at the app icon on the panel or dock and in the tray at 16 px and at 22 px. | Both are recognisable and not blurred, on a dark and a light panel. | |
| 11 | Log in to an X11 session (not Wayland) and repeat steps 1, 2, 4, 9. | The title bar, drag, resize edges and maximise work; the tray and keys behave the same. | |
| 12 | Open the app in real WebKit and play a book through the bar, the tray and the sleep timer. | `desktop-shell.js` reports the state to the tray; no console errors in the inspector. | |
| 13 | On the CGO-free build, play a book for one hour with the window visible, seeking and pausing a few times. | No crash, hang or sound dropout; the process stays alive (the risk is WebKitGTK's garbage collector signal through purego). | |
| 14 | On an ultrawide monitor (wider than 1,920 px, for example 3440 x 1440), open the app. Press Maximise on the bar, then again. Drag an edge and a corner out as far as they go. Double-click the bar, and press Super+Up. Close while fitted and start it again. | Maximise gives a window 1,920 px wide and as high as the screen (centred on X11), with the Restore icon; again goes back to the earlier size. Edges and corners stop at 1,920 px. The other maximises give the same fit, never a full-width window with a blank strip. The app restarts fitted. On a screen of 1,920 px or less, Maximise is a normal maximise. | |

Do not switch `make desktop` to the CGO-free build until row 13 is recorded.

## Android Auto (A8): what only a phone and a car can prove

The emulator's Android Auto is a stub, so no head unit has run this app. Everything below needs a real phone with this build and either a car or the Desktop Head Unit
(`$ANDROID_HOME/extras/google/auto/desktop-head-unit`) against a phone on USB: Android Auto settings, tap Version ten times, Developer settings, Unknown sources on,
Start head unit server, then `adb forward tcp:5277 tcp:5277` and start the head unit. Signed in to a server with a queue, a shelf, a downloaded book and a book with a cover.

| # | Step | Expected | Result |
| --- | --- | --- | --- |
| A8-1 | Open Tendshelf in the car's media list. | Four tabs: Continue, Up next, Shelves, Downloaded. Covers show on books that have one. Day and night look right. | |
| A8-2 | Tap a book in Continue. | It plays from the place Tendshelf has (not from the start); the title is the chapter; Next and Previous move by chapter; the speed button reads 1.0x and steps 1.25x, 1.5x, 1.75x, 2.0x, 0.75x. | |
| A8-3 | Say "Play <a book title> on Tendshelf", then a title you do not own. | The first plays; the second says "I couldn't find that book." and nothing changes. | |
| A8-4 | Disconnect the phone from the car mid-chapter and connect again. | The last book resumes from its place when you press Play; nothing starts by itself on connecting. | |
| A8-5 | Sign out on the phone, then open Tendshelf in the car. | "Open Tendshelf on your phone to sign in." instead of an empty or spinning list. | |
| A8-6 | Airplane mode on the phone, then open Tendshelf in the car. | Downloaded and Continue work; Up next and Shelves say "Can't reach your library. Downloaded books still play." | |
| A8-7 | Change the place from another device, then start the book in the car. | The question appears on the phone only; the book plays on in the car; nothing moves until you answer. | |
| A8-8 | Lock the phone in a pocket for an hour, then start a book from the car at ignition. | Sound starts, or the car says "Open Tendshelf on your phone to keep listening."; never a silent hang. | |
| A8-9 | Play with a radio, a call and a navigation prompt in turn. Use the steering-wheel buttons. | Pauses for the call, ducks for the prompt, steering-wheel next and previous move by chapter. | |
| A8-10 | Tunnel: lose the network mid-chapter. | The 5 minute buffer plays on; no error in the car until it runs out. | |
| A8-11 | Update Android Auto while the app is sideloaded (Unknown sources on). | Tendshelf is still listed and keeps its place. This alone decides how the app is distributed. | |
| A8-12 | Open the voice and driving-mode limits of the car's screen: a shelf of 48 books. | The list is usable or the car trims it; note the car's make and any trimming. | |

## Android app (one row per package, from A5b)

Needs the debug or release APK on a real phone, signed in to a server with a series of two short books and a long one.

| # | Step | Expected | Result |
| --- | --- | --- | --- |
| A5b-1 | Play a book, pause, lock the phone and wait an hour (deep Doze). Start a book from the car or a headset button. | Sound starts, or the notification says "Tap to keep listening"; never a silent hang. | |
| A5b-2 | Change the place from another device, wait over a minute paused, then resume with the app open. | The page asks "You were at ... Continue there?" with the other device's name; nothing moves until you tap. | |
| A5b-3 | Repeat with the screen off. | A notification asks the same question; opening the app shows one question on the page, not two. | |
| A6-1 | Set a 15 minute timer, lock the phone and leave it. | Sound fades over the last 10 seconds and stops; the next Play is at full volume. | |
| A6-2 | During the fade, shake the phone twice within a second (screen off, then on). | The sound comes back to full volume and the timer gains 10 minutes. | |
| A6-3 | Set the timer, pause for a minute, play again; repeat at speed 2. | Paused time does not count; minutes are real minutes at any speed. | |
| A6-4 | Choose "End of chapter" on the last chapter of a book with another one queued. | It stops just before the end and the queued book does not start. | |
| A7-1 | Download a three-file book on Wi-Fi, then turn on airplane mode, close the app from recents and open it. | The Downloads page lists the book; Play sounds within about 2 seconds; chapters and skip work; the screen off for 10 minutes keeps playing. | |
| A7-2 | Start a download of a long book, lock the phone and leave it for an hour (deep Doze, and on Android 15 and later the 6 hour data-sync limit). | The notification keeps its progress; at the limit it says "Downloads paused. Open Tendshelf to finish." and opening the app finishes the book without starting over. | |
| A7-3 | Tap Sign out on the Profile page. Check Settings, Apps, Tendshelf, Storage. | Downloads are gone and the app's data is back to its small size. A session that merely ran out (revoke the device from another browser) keeps the downloads. | |
| A7-4 | Sign in as someone else on the same phone. | The first person's downloads are gone before the second person sees any. | |
| A7-5 | Fill the phone's storage, then try to download a long book. | "Not enough room on this phone for this book." and nothing is half stored. | |
| A9-1 | In the app, open a book with text and tap Download on its page. Wait for Ready offline, switch on airplane mode, close the app from recents and open it. | The Downloads page lists the book with a Read button; Read opens the reader; pages turn, contents opens, pictures show, the time left is shown. Search, marks and settings are not offered. | |
| A9-2 | Read three pages in airplane mode, close the app, switch the network on and open any page of the server. | Your place is on the server (open the book in a browser at the same place); the reading time shows once on the Reading page. | |
| A9-3 | On a phone on mobile data with the server behind a VPN that is switched off, open the downloaded book. | The reader opens in about 2 seconds, with no long white screen. | |
| A9-4 | Sign out on the Profile page, then look at Settings, Apps, Tendshelf, Storage. | The books' text is gone with the audio; the app's data is small again. | |
| A9-5 | Switch the phone to a language and a font size at 200 % and open a downloaded book offline. | Text stays readable; the offline reader matches the online one. | |
| A10-1 | Make your own key as in docs/android.md, run `make apk` with the four variables set, then `adb install -r` the APK over a phone that has the test (debug) build. | Android refuses the update (different key). Uninstall Tendshelf, install the release APK: the app opens with the new icon and the server page asks you to connect. | |
| A10-2 | Build a later version signed with the same key and install it over the first. | It installs over it; downloads and sign-in stay. `apksigner verify -v --print-certs` shows the same SHA-256 as the first. | |
| A10-3 | Run `make apk` with no key variables. | The output file ends in `-unsigned.apk` and the script says UNSIGNED; Android will not install it. | |
| A10-4 | Push a test tag on a copy of the repository with the secrets set, then with them missing. | The server release is published both times; the `apk` job attaches a signed APK, or an `-unsigned.apk` with a plain message in its log. | |
| AF-1 | On an Android 9, 10 and 11 phone or emulator, install and open the app, play a book, change the system font size. | No crash at launch; the page stays where it was after the size change. | |
| AF-2 | Play a book, pause, swipe the app away in Recents, press the headset play button. | The book resumes (the `MediaButtonReceiver` path). | |
| AF-3 | Join another network that has another server (or none) at the same address and open the app and the Downloads page. | Nothing is deleted, you are not signed out, and the other server never sees your token. | |
| AF-4 | Fill the phone's storage to the last byte while a book plays. | Sound goes on; once there is room the place is saved. | |
| AF-5 | Connect the phone to a different server. | A dialog names both and what goes; Cancel changes nothing; Change sends the saved places first and removes downloads only after the new sign-in. | |
| AF-6 | Connect Android Auto and the system media controls while a book plays. | Browse, title, progress and buttons still work (another app cannot read them). | |
| QR-1 | Show an invite or "Add a phone" QR on a computer; point the stock camera of a Pixel and of a Samsung at it. | The camera offers the address; the server page opens in the browser (guess: both do). | |
| QR-2 | On that page tap "Open in the app" in Chrome, Firefox and Samsung Internet, with the app running and not running. | The app opens once with "Connect to this server?" showing the exact address; the browser's fallback (not the Play Store) shows if the app is missing (guess). | |
| QR-3 | Rotate the phone while that dialog is up, then press Back and open the app from Recents. | No second dialog; nothing connects. | |
| QR-4 | Join from an invitation over `http` to a LAN address (not `localhost`). | Joining works, or the server's "This form expired" is seen (see roadmap, Android status). | |
| QR-5 | Open `tendshelf://open?link=...` from another app or a web page while signed in. | Only the dialog or "already signed in" shows; nothing signs out. | |
| QR-6 | Scan a dark-themed QR on a screen with the camera. | The code still reads. | |


## L8: the next book is ready on the phone (decision 0035)

Use the shared AVD with `-gpu host` under `flock -w 3600 .local/android-emulator.lock`. A real start, never a `DEBUG` op. Book A has under 2 hours left; book B is next in the queue. Server log and `adb logcat -s Tendshelf` open.

| ID | Check | Expect | Result |
| --- | --- | --- | --- |
| L8-E1 | Switch off. Play A on Wi-Fi for 2 minutes. | No `GET /api/v1/queue/next` in the server log, no row for B. | |
| L8-E2 | Switch on. Put the app in the background, screen off (`adb shell input keyevent KEYCODE_SLEEP`), play A on Wi-Fi for over 60 s. `adb shell dumpsys activity services app.tendshelf.android` and `adb logcat -d \| grep -i "Background started FGS"`. | `TsDownloadService` with `isForeground=true`, no `Disallowed`, B reaches ready after A is paused. If refused, report it: the honest result narrows to "app open". | |
| L8-E3 | During E2's download: `adb shell cmd netpolicy set metered-network "AndroidWifi" true`. | Bytes stop within about 2 s, server log quiet, Downloads row says "Waiting for Wi-Fi". Set it back to `false` with A playing: it goes on by range within about 60 s. | |
| L8-E4 | Near the end of A on a metered Wi-Fi: no peek. Set it unmetered, open the app. | No peek while metered (`autoWant` set); B starts after the app opens. | |
| L8-E5 | Press Download on a waiting automatic book on metered Wi-Fi. Then press Download on another book during an automatic download. | The first runs now. The second book finishes first. | |
| L8-E6 | Switch off during a download; then a server book larger than the room; then a book whose downloads are refused for this person. | Row and partial bytes gone, a finished automatic book stays; Downloads note and no bytes; no row and no toast. Method: fill the AVD with `fallocate` to about 2.02 GiB free for the room case; for the refusal, make the test person a member with an `all` grant with `can_download = 0` in the test database. | |
| L8-E7 | Sign out. Then, before it, let `POST /queue/next` run at the end of A with the network off after B was fetched. | Every book, mark and `autoWant` gone. The pop still happens once and B plays from the phone. The second half: with B fetched, cut Wi-Fi and mobile data (`adb shell svc wifi disable`, `svc data disable`) before the end of A, screen off. | The pop is not asked; B starts from the phone ("next book: starting from the phone"). With the network back and the app in front: one `DELETE /api/v1/queue/{B}` ("queue tidied (204)"), B gone from the queue, A's finished state on the server. If B was removed by hand first, or a sleep timer ended A: no start and no crash. | |


## Pages reader and the flip on the desktop app and the Android app (2026-11-09)

Run by script, not by hand. Desktop: `make build desktop-probe`, then `TS_MONITOR=1440x900 scripts/isolated-display.sh desktop/probe/pages-run.sh` (private headless GNOME Shell, WebKitGTK 2.52.6, Wails 3.1.0, 1440 x 868 window, software rendering, own server on port 18097, synthetic EPUB). Android: a temporary AVD (Pixel 9, Android 16, WebView 133, x86_64, `-gpu host`, 412 x 846 CSS px at 2.625), debug build, own server through 10.0.2.2:18110, signed in through the app's normal flow, WebView read over the devtools socket. Evidence: the PNGs the report names. Limits: one Android version, an emulator, software or host-GPU frames, no physical phone, no real GPU on the desktop run.

| ID | Check | Result |
| --- | --- | --- |
| PG-D1 | Desktop: the reader opens in Pages, two facing pages, page numbers, foot line. | Pass: `flow=pages`, 2 columns, 576 px a page, "Pages 1-2 of about 28", edges at both sides. |
| PG-D2 | Desktop: a flip runs without breakage (leaf not clipped, `preserve-3d`, `backface-visibility`). | Failed, then fixed: past the half turn the mirrored front of the leaf showed over the back (WebKit hides a back face only when the face has its own transform). Fix: `.ts-flip__side { transform: rotateY(0.001deg) }`. After: forward and back flights at 25, 50, 75 % and in slow motion all show the right page, leaf whole. |
| PG-D3 | Desktop: no CSP violation; `ts.flip.slow` not set; frame gaps. | Pass: 0 violations in the page after the probe attached; flag unset after 7 flights; build 3-4 ms; rAF gaps p50 16 ms, p90 17 ms, max 23 ms (software rendering, 60 Hz). Violations before the probe attached are not seen. |
| PG-D4 | Desktop: edges, ribbon, Slide, None, Flip again. | Pass: both edge buttons turn (13-14 to 15-16 and back), the ribbon toggles and toggles back, Slide and None turn without a stage, Flip again shows one. |
| PG-D5 | Desktop: the page change out of the reader (`#main` has no view transition name there). | Pass: reader `#main` name `none`; the boosted link to Home ran one view transition and the page swapped. Not seen: the transition's frames. |
| PG-D6 | Desktop: any boosted page change with `WEBKIT_DISABLE_DMABUF_RENDERER=1` set. | WebKitGTK 2.52.6 segfaulted inside the window on a boosted link (Home to Me) with that variable set, reader or not; without it, or with `startViewTransition` removed, no crash. Fixed: the desktop window does not ask for View Transitions (`boostAttrs`). Many NVIDIA setups set that variable. See the roadmap note. |
| PG-A1 | Android: the reader opens in Pages, bar hidden, then a centre tap shows it. | Pass: pages, bar hidden, "1 of about 33" and "18 pages left in this chapter"; centre tap shows the bar and "Page 4 of about 33". |
| PG-A2 | Android: the first-use hint shows once. | Pass: the touch hint ("Tap the sides or swipe to turn the page. Tap the middle for the menu.") on the first open; not again after a reload (`ts.reader.hinted`). |
| PG-A3 | Android: side taps flip; a swipe drags the leaf. | Pass: a right-side tap flips (slow-motion frames show the fold); a drag lifts the leaf with the finger and the page turns on release. |
| PG-A4 | Android: frame gaps, `ts.flip.slow`. | Pass: flag unset after 3 flights; build 9 ms; rAF gaps p50 16.7 ms, p90 16.8 ms, max 33 ms (22-226 frames). |
| PG-A5 | Android: the ribbon marks a page. | Failed, then fixed: nothing happened on tap because `crypto.randomUUID` is missing on plain http that is not localhost (also hit highlights and bookmarks in `reader-marks.js`). Fix: a fallback id in both files. After: the ribbon pressed (`c2.p9.s0` bookmark) and the mark shows. |
| PG-A6 | Android: volume keys turn pages when enabled. | Pass: with "Turn pages with the volume buttons" on, volume down 4 to 5 to 6, volume up back to 5. |
| PG-A7 | Android: numbers read "n of about m"; service worker. | Pass: "n of about 33"; `navigator.serviceWorker` is absent in the app, so none is used. |
| PG-A8 | Android: read aloud follows the pages. | NOT VERIFIED: the strip started (pause icon) but this emulator has no voice that speaks, so no sentence advanced in 25 s. |
| PG-X | Desktop and Android in sepia and contrast; Firefox at 2000 px; Wails on Wayland with a real GPU; a physical phone. | Not run. |

## S7 sign-in with a provider (decision 0037)

Needs a real provider reachable over https from the browser (Authelia 4.39, Authentik, Keycloak). Settings: Settings, Your server, Sign-in. Not run yet.

| ID | Check | Result |
| --- | --- | --- |
| S7-1 | Authelia 4.39 (claims in userinfo): set up the provider, sign in with a linked account, link from Account (confirm card), unlink. | Not run. |
| S7-2 | Authentik with a signing key: sign in. Then with no signing key (HS256): saving with the switch on is refused in words. | Not run. |
| S7-3 | Keycloak realm, confidential client: sign in; a wrong secret shows the failed message; with the provider stopped, `/login` still takes a password and the button says the provider is not answering. | Not run. |
| S7-4 | Android app through each provider to `/app/connect` and back (P3 built: unit-tested in `android/shared`, `make apk-debug` builds; needs a real provider and the emulator). | Not run. |
| S7-5 | Desktop window: the provider's pages stay in the window and the bridge answers nothing on them. | Not run. |
| PA1-1 | Podcasts A1: with podcasts on, play an episode from its page in the app. Lock screen and notification show the episode title, the show as artist and the show's picture (not a book's, not a chapter name as the title). | Pass (2026-11-10, emulator Pixel 9 API 36, debug build, throwaway server, HistoryExtra and Podnews episodes). Media session metadata: title = episode title, artist and album = show; the quick-settings media card shows the episode title, the show name and the show's picture. Not checked on a real lock screen (the emulator has no screen lock). |
| PA1-2 | A show with speed 1.4, intro 30 s and outro 45 s: the first audible sample is at 1.4x and after the intro; near the end the episode finishes by itself 45 s early and is marked played. A show with none of these plays from 0 at the personal speed. | Pass (emulator). A show with 1.4x, intro 30 and outro 45 (NASA on Soundcloud, 3 min episode): first sample at position 30.0 s, session speed 1.4; the episode ended by itself at about 135 s (last sample 130.7 s, then ended), played = true on the server. A show with no settings (HistoryExtra) started at 0 at speed 1.0. Not measured by ear; read from the media session. |
| PA1-3 | Resume: pause, wait 30 s, press play: smart rewind steps back; kill the app, reopen the episode: it starts where it stopped; the place appears on the web (and the other way round). | Pass (emulator). Pause 32 s then play: position stepped back (77.1 s to 75.3 s a moment after play). Force-stopped the app and reopened the episode: resumed at 82.5 s (paused at 84.5 s, minus the rewind) and the server held 84.5 s. Other way round: a progress post to the server (position 600 s, new device id) made the app start the episode at 601 s. The save from the web page itself was not clicked, a form post stood in for it. |
| PA1-4 | Airplane mode while playing buffered audio, move on, come back online: the saved place reaches the server (one batch to `/api/v1/podcasts/progress/batch`), no duplicate. | Pass (emulator, airplane mode via `cmd connectivity`). While offline the outbox logged `saved (pause)` and `not sent (IOException), 1 rows kept` (one row per episode, no duplicate); back online `outbox: sent 1, settled 1` about 4 s later and the server place moved to 618.3 s. Counted from the app log, not from the server's access log. |
| PA1-5 | Chapters: an episode with Podcasting 2.0 chapters shows the chapter under the title; Next/Previous move between them; with none, Next does nothing. | Pass (emulator, Podnews Weekly Review, 11 chapters from Podcasting 2.0 data). The player shows "Chapter 1 of 11" and the chapter title under the episode title; Next chapter jumped to 140 s then 472 s and Previous went back to 140 s. With an episode with no chapters (HistoryExtra) Next chapter did nothing (position kept playing). The "nothing" half was read from the media session only. |
| PA1-6 | Sleep timer "end of episode": playback stops 1 s before the end and no queue is asked; screen off for an hour, Bluetooth play/pause and lock-screen seek work. | Partial (emulator). "End of episode" sleep timer: playback ended and logcat says `sleep: the book ended under the timer, queue not asked` (and `next book: a sleep timer ended this book, queue not asked`) with a book queued behind it; nothing started. Not run: that it stops exactly 1 s before the end (the sample after the end read 180.9 s of 180.8 s, no sample inside the last second), screen off for an hour, Bluetooth play/pause, lock-screen seek. |
| PA1-7 | Finish an episode with a book queued (Up next holds both kinds): the episode is marked played and the queued item starts, whichever kind it is; with the queue empty nothing starts. | Pass (emulator). Episode with the Probe Book queued: at the end the episode was marked played, the book left Up next and started (log `next book: starting`). Book finished with an episode queued: the episode started at the show's speed. Not run: finishing with the queue empty (the second finish was not awaited). Seen: the first replay of the same episode in the same app process after it had ended under the sleep timer did not hand off or mark played until the app was restarted (looks like `NextBook.handled` is not reset for a repeated episode id, `android/.../NextBook.kt`); not reproduced on a clean run. |
| PA3-1 | Podcasts A3, Android Auto (DHU or a car): Shelves tab lists Podcasts first (only with podcasts on); Podcasts shows New, Downloaded and Shows. New lists unplayed episodes with the show's picture, show name and length; Shows lists each followed show with its picture and "N new", and a show opens its newest 50 episodes. No row is blank or has a missing picture. | Not run (the emulator's Android Auto is a stub, as the A8 section says; the DHU exists on this machine but there is no phone-side head unit server). |
| PA3-2 | Podcasts, Downloaded: lists the downloaded episodes (newest first), also with the phone in airplane mode, and the Downloaded tab lists them next to downloaded books. | Not run (no head unit, see PA3-1). |
| PA3-3 | Pick an episode in the car: it starts at the server's place; played rows show the played mark and partly played rows a progress bar. | Not run (no head unit, see PA3-1). |
| PA3-4 | Row actions on an episode in the car (long-press or the row's action button): Mark as played / unplayed and Star / Unstar reach the server (check the web page); reopen the list and the row shows the new state. Download and Play next are not offered (A2, P5). | Not run (no head unit, see PA3-1). |
| PA3-5 | Widget with an episode playing: title is the episode, second line is the show, the picture is the show's; play, back and forward work. Reboot the phone: the widget still shows it and Play resumes the episode; the media button on a headset does the same. Tap the widget: the episode's page opens. | Not run (widget not added: no practical way to pin it by adb; reboot not tried). |
| PA2-1 | Podcasts A2: on an episode page in the app press Download: the button shows the percent, then "Ready offline" with a link to Downloads. Force a network drop halfway (airplane mode for a minute, then off): the download goes on from where it stopped, one file only, and Downloads shows the real size, not the feed's. | Partial (emulator). Download button showed "Downloading…", "70%" and then "Ready offline" with the link to /me/downloads (three episodes, 9 to 90 MB, each done in about 1 to 3 s over the emulator's host link). Not run: the network drop halfway (the downloads finish before a drop can be made; `adb emu network speed` did not slow the host route) and the real size against the feed's: the Downloads row shows no size. |
| PA2-2 | Airplane mode with no server reachable: Downloads > the episode > Play starts it with its picture and chapters; pause, kill the app, reopen offline: it resumes where it stopped; back online the place reaches the server (one batch to `/api/v1/podcasts/progress/batch`). | Pass (emulator). Airplane mode on: Downloads (offline page) listed the three episodes; Play started the episode with its title and the show's picture (no chapters on that one); paused, force-stopped, reopened offline: it resumed near the place (about 81 s after pausing at 73 s); back online the outbox logged `sent 1, settled 1` and the server held 91.2 s. Chapters offline were not checked (no downloaded episode had chapters). |
| PA2-3 | Downloads > Podcasts on this phone: a followed show shows Turn on. Turn it on with Wi-Fi: its newest N unplayed episodes (the show's "keep the latest") download and say "Downloaded on its own". Turn Wi-Fi off (mobile data): nothing moves a byte and a waiting one says "Waiting for Wi-Fi"; Wi-Fi on: it goes on. | Not run (blocked): the emulator's data partition has about 1.2 GiB free, below the 2 GiB floor, so turning on NASA on Soundcloud (Wi-Fi, unmetered) fetched nothing and the page stayed silent (a foreground check ran, `checkedAt` was set, `Offline.addEpisode` returned at `AutoPlan.NoRoom`). Turn on and Turn off worked, the line reads "Downloading new episodes on Wi-Fi · keeps the latest 3". Needs a device or AVD with more than 2 GiB free. Cosmetic: the lead text of the Podcasts list is drawn in the red alert colour (`start__note` in `StartPages.kt` line 150). |
| PA2-4 | Play an automatic episode to the end (or mark it played on the web) and open the app again: it is gone from Downloads. An episode you downloaded yourself with the same show on stays after it is played. | Not run (no automatic episode could be fetched, see PA2-3). |
| PA2-5 | Quota: with 5 GiB of automatic books and episodes kept, a new automatic episode is not fetched and the page says so; remove an automatic one and it is. Free space under 2 GiB after the episode: not fetched. | Partial (emulator). Free space under 2 GiB: a new automatic episode was not fetched, as intended. The page did not say so: `autoNoRoom` is set only for the next-item path (`nextItem`), not for a show's check (`Offline.kt` around line 844). Not run: the 5 GiB quota and the positive case. |
| PA2-6 | A new episode arrives with the app closed (publish one, `adb shell cmd jobscheduler run -f app.tendshelf.android <id>` for the job `tendshelf-episodes`, unmetered network): it downloads and a notification names it; turning every show off cancels the job (`adb shell dumpsys jobscheduler` lists none). | Partial (emulator). With a show on, WorkManager's job exists (`dumpsys jobscheduler`: one `androidx.work.systemjobscheduler` job, network not metered, battery not low); turning the show off removed it (zero jobs). A forced run is held back by WorkManager ("executed before schedule"), and no episode could be downloaded anyway (PA2-3), so the fetch and the notification were not run. The job name `tendshelf-episodes` is a WorkManager unique name, not a JobScheduler tag. |
| PA2-7 | Remove an automatic episode by hand: the next check does not fetch it again. Sign out: downloads, choices and the job are gone. | Partial (emulator). Sign out (Account, Sign out): the downloads list went to 0 rows, `episodeauto.xml` became an empty map (choices gone) and the job was gone from `dumpsys jobscheduler`. Not run: removing an automatic episode by hand and the next check not fetching it again (no automatic episode existed, PA2-3). |
| PA2-8 | Play next on an episode row in the car (long-press): Up next on the web lists it first; finishing the playing item starts it. | Not run (car row; the emulator's Android Auto is a stub, no DHU session). |
