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

-


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

-

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
