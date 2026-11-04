# 0013 Listening statistics, Eyes-free, shake and scheduled backups (M3b)
Status: proposed (2026-11-03). Plan: `docs/plans/m3b-pocket.md`.

## Context
- Roadmap "M3 Pocket" lists four items that are left after M3a (decision 0012): listening statistics, Eyes-free (car)
  mode, shake to extend sleep "where permitted", and scheduled database backups. The accessibility audit and the
  performance budgets are M3c. The two-column full player still has no board and stays out.
- What exists:
  - `listening_sessions` (migration 0006): wall-clock `listened_ms` per stretch, indexed by `(user_id, started_at)`,
    written by `internal/progress/sessions.go`. `progress.finished_at`.
  - The Profile board `screens/people/ProfilePhone` draws a "Your listening" panel: "This week", "In a row",
    "Finished in 2026" and a Sunday-to-Saturday row of day marks. There is no tablet or desktop stats board.
  - The board `screens/reader/EyesFreePhone` and `HANDOFF.md` ("three huge targets and no text to follow"); the CSS
    class `.ts-eyesfree__zone` is already in `bundle.css`. Nothing else.
  - Shake to extend sleep is built (`player-full.js` `onMotion`, `sleepPlus`, the "Shake your phone to add 10
    minutes" hint) and tested with a synthetic `devicemotion` in `player_full_speed_browser_test.go`. The phone check
    skipped it because motion sensors need HTTPS.
  - `internal/db/backup.go` writes verified `VACUUM INTO` copies before a migration (`tendshelf-v<N>-<UTC>.db`,
    newest three kept). The jobs runner, `GET /hx/jobs/{id}` with per-kind views, the server settings KV
    (`db.GetSetting`/`PutSetting`), the per-person KV `user_settings`, and the preferences window with its
    "Your server" group (decision 0010) all exist.
- Nobody stores a time zone today; every time is UTC milliseconds.

## Decision
1. **Statistics live in Profile, at every width.** One `ListeningStats` panel is the first panel of the Profile pane
   (`/me`): a page on a phone, the account category of the preferences window on tablet and desktop, where the
   panels already sit side by side. No new route, no stats page, no tablet or desktop board needed.
   - "This week": the sum of `listened_ms` of sessions that started since Sunday 00:00 local time (the board's week).
   - "In a row": consecutive local days with at least 1 minute of listening, ending today, or yesterday when today has
     none yet (the board's "9 days ... not yet today"). A session counts on the day it started.
   - "Finished in {year}": books with `finished_at` in the current local calendar year.
   - Day marks: filled for a day with at least 1 minute, a dashed ring for today without listening, a plain ring
     for an earlier empty day and for days still to come; the row is one `role="img"` with a sentence label, so the
     marks never speak by colour alone.
   - Counts are of the person's own activity and name no book, so they are not filtered by today's grants.
   - Not gamey (brief): no badges, goals, records, comparisons or notifications.
2. **The day boundary is the person's time zone, stored per person and taken from their browser.**
   - `user_settings` key `ui.time_zone` (an IANA name). The layout renders it on `<body data-ts-tz>`; `shell.js`
     compares it with `Intl.DateTimeFormat().resolvedOptions().timeZone` once per page load and, when they differ,
     posts it to the existing `POST /me/settings` (the `saveSpeed` pattern). Empty means the server's zone.
   - Why stored rather than sent with each request: an admin's view of a person (point 3) and any later server-side
     use must agree with what the person sees, and a plain no-JavaScript page load must still have a zone.
   - Why from the browser rather than a Settings field: most self-hosted servers run in UTC containers; a field
     nobody fills would cut days at the wrong midnight. A person who travels follows their device, which is what a
     streak on a phone means.
   - The binary imports `time/tzdata` (standard library, about 450 KB) so a container without zoneinfo works.
3. **Who sees whose statistics.** A person sees their own on Profile. The brief lists "listening stats" for admins
   under Users; the admin sees the same three numbers, without the day marks and without any title, on the person's
   page `/admin/users/{id}`, exactly when that page is open to them today (`share.Can(viewer, ManageUsers,
   Target{Kind: TargetUser, Role})`: the owner sees everyone, an admin sees members and listeners). Not in the list
   of people: a column of everyone's hours side by side is a leaderboard. Nobody else, and no API route.
4. **Eyes-free is a mode of the player, not a route.** A second modal `<dialog id="eyes-free">` rendered beside
   `#player-full` (so it exists wherever the player does, never in link mode), entered from "Eyes-free" in the full
   player's ⋯ menu, left with "Back to the player", Escape or the browser's dialog cancel, after which focus returns
   to the ⋯ button. Why not a route: the board is full-screen with no shell, navigation swaps only `#main`, and a mode
   needs no URL to be bookmarked.
   - Targets: the board's five buttons in its three rows (Back and Forward; Play or Pause across the full width; Bookmark
     and Sleep). `HANDOFF.md`'s "three huge targets" is read as these three rows; the board is the more specific
     source and the two do not conflict on anything else. Back and Forward use the person's skip seconds.
   - Sleep cycles Off, 15 min, 30 min, 60 min, End of chapter, Off; the button shows what is left. Bookmark saves
     the live position and confirms with a short toast without the "Add a note" link (nothing to read while driving)
     and a 40 ms vibration where the device has one.
   - The screen stays on through the Screen Wake Lock API while the dialog is open and the page visible (asked
     again after the page returns); where it is missing, the device's own timeout applies. After 10 seconds without a
     touch the dialog dims; the first tap only wakes it and never presses a button.
   - Code: `js/eyes-free.js`, imported by `player-full.js` on first use (it is in the worker's precache, so it also
     opens offline). It drives playback only through `window.tsPlayer` and a small public `window.tsPlayerFull`
     (`sleep`, `sleepLeftMs`, `bookmark`). Its styles are `css/pages-eyes-free.css`, linked by the script on first use.
5. **Shake: verify only.** No code gap was found. A tap on Eyes-free's Sleep starts the timer inside the tap, so iOS
   can still ask for motion access. The proof is a browser test of a shake during a fade started from Eyes-free, plus
   the HTTPS phone step in `docs/manual-checks.md`.
6. **Scheduled backups.**
   - Schedule: Off, Every day (default) or Every week, in the server settings KV as `backup.schedule`. Default on,
     because a family's listening history is worth more than a few megabytes of disk.
   - Files: `<data>/backups/tendshelf-copy-v<user_version>-<UTC stamp>.db`, mode 0600, beside the pre-migration copies.
     Retention is separate: the newest seven copies are kept; the pre-migration rotation (`tendshelf-v...`, three)
     never touches a copy and the copy rotation never touches a pre-migration file (different name patterns).
   - How: a job kind `db.backup` (concurrency 1, one attempt) runs `VACUUM INTO` on its own read-only connection
     (`mode=ro`), so readers and the writer keep going; the copy is verified with the existing `verifyBackup` and a
     failed copy is deleted. A scheduler goroutine (the `writes.Run` pattern) checks every 15 minutes whether the
     newest copy is older than the interval and enqueues one job, never a second while one is queued or running, and
     waits an hour after a failure.
   - Who: the owner only (`share.ManageServer`, like Email): a copy holds every account's data, including password
     hashes. Everyone else gets 404.
   - What the owner sees at `/admin/backups` (a "Your server" category, and a Profile row on the phone): the schedule,
     the last copy (when and how big) or why the last one failed, when the next is due, the kept copies, the folder
     they are in, "Back up now" (a job whose status polls `/hx/jobs/{id}`), and how to restore a copy behind a
     disclosure.

## Not decided here (later, and why)
- Restoring from the web page, downloading a copy and a backup folder outside `<data>`: a restore must stop the
  server, a download moves every password hash over the network, and a free path lets the web page write anywhere.
- A week that starts on Monday: the board starts on Sunday; a locale-based start can follow without a migration.
- A person hiding their numbers from admins: not in the brief; a Privacy switch can follow.

## Consequences
- No migration. New keys: `ui.time_zone` (per person), `backup.schedule` (server).
- App JavaScript grows by about 3.25 KB gzip (Eyes-free, the public player API, the time-zone post); the budget raise
  is the operator's call.
- The server binary grows by the embedded zone database.
