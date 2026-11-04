# M3b Pocket: statistics, Eyes-free, shake, scheduled backups

Decision: [0013](../decisions/0013-pocket-stats-eyes-free-backups.md). Three builder packages (S, E, K) that run in
parallel worktrees, then a scribe step (H). No migration anywhere. Every package: `make check`, `make build`, its own
browser tests and captures (`TENDSHELF_TEST_BROWSER=1`), `go run ./internal/testutil/capturediff` where it claims
"unchanged". Copy marked NEW is not in the deck; list it in the report, do not edit `docs/copy-additions.md` (H does).

## Step 0 (orchestrator, before dispatch, after the operator approves)
- `internal/web/static/motion_test.go`: `jsTotalGzip` to `55*1024 + 768` with a comment line naming M3b (0013).
  Packages edit only their own per-file rows, so nobody else touches this constant.

## Shared files and who owns which hunk
| File | S | E | K |
| --- | --- | --- | --- |
| `internal/web/server.go` | `pageCached`: `TimeZone` field | - | the `registerX` list: `s.registerBackups(mux)` after `registerDownloads` |
| `internal/web/pages/profile.templ` | `Stats` field after `ServerName`; panel first in `.ts-groups` | - | `Backups` field after `Email`; row after the Email row |
| `internal/web/pages/prefs.templ` | - | - | `Backups` flag and list item |
| `internal/web/static/motion_test.go` | `shell.js` row | `player.js`, `player-full.js` rows, new `eyes-free.js` row at the end | - |
| `internal/web/components/layout.templ` | `data-ts-tz` on `<body>` | - (E renders inside `PlayerFull`) | - |
Test-name prefixes: S `TestStats*`, E `TestEyesFree*`, K `TestBackups*` (not `TestBackup*`: taken in `internal/db`).
Matrix: only K adds routes, in its own `routes_matrix_backups_test.go` plus one `append` line in `sweepRows`.

---

## Package S: listening statistics (builder, sonnet)
Invariants
- A person's numbers are computed only from their own rows; the admin path uses the target's id after the existing
  `loadPerson` gate. No route, no API, no title anywhere in the panel.
- Day boundaries use the person's `ui.time_zone`, else `time.Local`. DST-safe: day starts via `time.Date(...,loc)`.
- Bounded reads: every query is by `user_id` and a `started_at` / `finished_at` range on existing indexes.

Steps
1. `cmd/tendshelf/main.go`: `import _ "time/tzdata"` with a one-line comment (decision 0013).
2. `internal/users/settings.go`: `Settings.TimeZone string` (""= server zone); key `keyTimeZone = "ui.time_zone"`;
   load/save like `TextSize`; `func ValidTimeZone(s string) bool` = `s != "" && s != "Local" && len(s) <= 64 &&
   time.LoadLocation(s) == nil-error`; `SaveSettings` rejects an invalid non-empty value with `ErrInvalidSetting`.
   `Look` gains `TimeZone` (add the key to its `IN (...)`).
3. `internal/web/me_views.go` `settingsFromForm`: field `time_zone`; present and invalid -> `ok=false` (422 path).
4. `internal/web/theme.go` `lookFor`: also return the zone string; `internal/web/server.go` `pageCached`: pass it as
   `components.Page.TimeZone`; `components/layout.templ`: `data-ts-tz={ p.TimeZone }` on `<body>`.
5. `internal/web/static/js/shell.js`: on module start, read `Intl.DateTimeFormat().resolvedOptions().timeZone`; if
   truthy, different from `document.body.dataset.tsTz`, and different from `sessionStorage["ts.tz.sent"]`, store it
   there and `fetch("/me/settings", POST, headers X-CSRF-Token (read like player-full.js `csrf()`), HX-Request: true,
   form type, body time_zone=<encoded>)`, errors ignored. Skip when `document.body.dataset.tsLink` is set.
6. `internal/progress/stats.go` (new): `const StatsDayMinMs = 60_000`;
   `type DayMark struct{ Weekday time.Weekday; State string }` with State one of `listened`, `today` (today, none yet),
   `missed`, `ahead`; `type Stats struct{ WeekMs int64; Streak int; Finished int; Year int; Days [7]DayMark; TodayDone bool }`;
   `func (s *Service) Stats(ctx, userID string, loc *time.Location, now time.Time) (Stats, error)`:
   - week = Sunday 00:00 local of `now`; one query `SELECT started_at, listened_ms FROM listening_sessions WHERE user_id=?
     AND started_at >= ? AND started_at < ?` for `[min(weekStart, today-63d), tomorrowStart)`, bucketed by local date.
   - streak: from today (if >= StatsDayMinMs) else yesterday, walk back; when the walk reaches the window start, load
     the previous 64 days and continue; stop at the first empty day, or when the window is older than
     `SELECT MIN(started_at)`; hard cap 3660 days.
   - finished: `SELECT COUNT(*) FROM progress WHERE user_id=? AND finished_at >= ? AND finished_at < ?` (local year).
7. `internal/web/stats_copy.go` (new): the words and `func statsView(st progress.Stats) components.ListeningStatsView`:
   "Your listening", "This week", "In a row", "Finished in {year}", "1 day"/"{n} days", week time in the Home
   word forms rounded down ("6 h 12 m", "45 m", "0 m"; reuse the format.go helper behind "left in book" if it can
   round down, else a local `weekTime`), day letters `S M T W T F S`, label sentence (below).
   - Label: "Listened on {days} this week" where days are full names joined "A, B and C", a run of 3 or more as
     "Sunday to Friday"; append "; not yet today, {Weekday}" when today is `today`; none at all: "No listening yet
     this week" (+ the not-yet clause). NEW: every label form except the board's own sentence.
8. `internal/web/components/listening_stats.templ` (new): `ListeningStats(v ListeningStatsView, withDays bool)`,
   markup of the board's `pf-stats` section with `ts-` classes (`ts-stats`, `ts-stats__grid`, `ts-stats__value`,
   `ts-stats__days`, `ts-stats__day--listened|today|missed|ahead`); `h2` id `pf-stats`; values in `.time`.
9. `internal/web/static/css/pages-me.css`: the panel rules (tokens only; 28 px marks as rem; dashed `--line-strong`
   for today, `--line` ring for missed and ahead, `--accent-fill` for listened). If `pages-admin.css` needs them for
   the admin page, put the shared rules in `pages-me.css` and link `pages.MeStyles` on the person page.
10. `internal/web/routes_me.go` `profileView`: zone from settings, `s.progress.Stats(..., s.now())` (use the existing
    progress service field; name it in the report), `v.Stats`. `pages/profile.templ`: render it first in `.ts-groups`.
11. `internal/web/routes_users.go` / `pages/admin_user.templ`: on the person page, the same panel with
    `withDays=false` and heading "Listening" (NEW), computed with the target's zone.

Acceptance tests
- `internal/progress/stats_test.go` table: empty; week sum; a session at 23:30 local counts that day; DST week
  (America/New_York, 2026-11-01); streak through yesterday when today is empty; a 59 s day breaks it; streak longer
  than 64 days (window paging); finished in this year only; another user's rows never counted.
- `internal/users/settings_test.go`: `ValidTimeZone` table (Europe/Madrid ok, "", "Local", "Mars/Base", 65 chars).
- `internal/web/stats_test.go`: `TestStatsProfilePanel` (numbers and label text with a fixed `now` and zone),
  `TestStatsTimeZonePost` (204 with HX-Request, 422 for "Mars/Base", a later `speed=` post keeps the zone),
  `TestStatsAdminView` (owner sees an admin's numbers; admin sees a member's; admin on another admin and a member on
  anyone: 404, unchanged from today), `TestStatsNoTitles` (rendered panel contains no seeded book title).
- `internal/web/stats_browser_test.go`: `TestStatsBrowserZonePost` (CDP `Emulation.setTimezoneOverride` to
  `Asia/Tokyo`; after one page load the stored setting is that zone; a second load posts nothing).
- Captures `visual_stats_test.go` `TestStatsVisual`: Profile at 390, 768, 1024, 1440 in dark and light beside
  `ProfilePhone.png` (390); the person page at 1440. Seeded sessions relative to the fixed visual clock.
- Budget: `shell.js` row to `5*1024` (report the measured bytes); `pages-me.css` stays inside its budget.
Out of scope: a stats page, charts, per-book history, Home, the users list, the API, a week start setting.

---

## Package E: Eyes-free mode and the shake check (builder, sonnet)
Invariants
- Eyes-free never touches `#audio`; it calls `window.tsPlayer` and `window.tsPlayerFull` only. No inline style or
  script, no `hx-on`; every word comes from `data-ts-msg-*` rendered by the server.
- The first tap on a dimmed screen only wakes it. Wake Lock is released when the dialog closes or the page hides.
- Not rendered and not reachable in link mode (`m.Link`).

Steps
1. `internal/web/eyes_free_copy.go` (new): consts and `eyesFreeMsgs()`: "Eyes-free" (menu item and overline),
   "Back to the player" (NEW; the board's "Back to reading" is the reader's), "Chapter {n} · {m} min left in chapter"
   (board), "Back {n} s", "Forward {n} s" (board), aria "Back {n} seconds"/"Forward {n} seconds" (existing), "Play",
   "Pause", "Bookmark", "Sleep", "Bookmarked at {time}" (existing toast words, no link), "Big buttons, no text to
   follow. The screen dims after 10 seconds; tap anywhere to wake it." (board), sleep states reuse "End of chapter",
   "{n} min", "in {m} min", "Turn off".
2. `internal/web/components/player.templ`: `PlayerMsgs.EyesFree EyesFreeMsgs` (new struct in `eyes_free.templ`);
   `internal/web/copy.go` `playerMsgs()`: fill it from `eyesFreeMsgs()` (one line).
3. `internal/web/components/eyes_free.templ` (new): `EyesFree(m PlayerMsgs)`: `<dialog id="eyes-free"
   class="ts-eyesfree" aria-labelledby="eyes-free-title" hx-preserve data-ts-eyes-free data-ts-msg-*>` with: header
   (Back button `data-ts-ef-close`, overline), `h1#eyes-free-title` `data-ts-ef-title`, `p` `data-ts-ef-chapter`,
   grid of five `button.ts-eyesfree__zone` (`data-ts-ef-back|forward|toggle|bookmark|sleep`, icons from the board,
   toggle with `--main`), the footer line, a `[data-ts-full-toasts]` host, `aria-live="polite"` `data-ts-ef-status`.
4. `internal/web/components/player_full.templ`: in the ⋯ menu body after Mark as finished, `<button type="button"
   class="ts-player__menuitem" data-ts-eyes-free-open data-ts-src={ asset url of js/eyes-free.js }
   data-ts-css={ url of css/pages-eyes-free.css } if m.Link { hidden }>`; after `</dialog>`: `if !m.Link { @EyesFree(m) }`.
   If `PlayerMsgs` has no asset URLs, add `EyesFreeJS, EyesFreeCSS string` to `PlayerMsgs` filled in `pageCached`
   from `s.assets.URL(...)`: that is a one-line hunk in `server.go` next to `Player: playerMsgs()`; note it.
5. `internal/web/static/js/player-full.js`: (a) click on `[data-ts-eyes-free-open]`: close the menu, then
   `import(src).then((m) => m.openEyesFree(summaryOfMenu))`; (b) `window.tsPlayerFull` gains `sleep(choice, minutes)`
   (calls `setSleep`, synchronously), `sleepInfo()` -> `{mode, leftMs}`, `bookmark({quiet})` (quiet: the toast has no
   link); (c) toast host: `#eyes-free:modal [data-ts-full-toasts]` first, then the existing selector.
6. `internal/web/static/js/player.js` line 140: the same host order (one selector added).
7. `internal/web/static/js/eyes-free.js` (new, exported `openEyesFree(opener)`): link the CSS once (await `load`);
   fill title and chapter line from `tsPlayer.info()`/`state()` and on `ts:player:tick`; buttons call
   `tsPlayer.skip(-back)`/`skip(+fwd)`, `toggle()`, `tsPlayerFull.bookmark({quiet:true})`, sleep cycle
   off -> 15 -> 30 -> 60 -> chapter -> off via `tsPlayerFull.sleep`; status text for the live region; `navigator.vibrate?.(40)`;
   `showModal()`, focus the toggle; wake lock request on open and on `visibilitychange` visible; dim timer 10 s reset
   on `pointerdown`/`keydown`; capture-phase `click` on the dialog while `is-dim`: undim, `preventDefault`,
   `stopPropagation`; keys ArrowLeft/ArrowRight skip, Escape via `cancel`; on `close`: release lock, clear timers,
   remove listeners, focus `opener`.
8. `internal/web/static/css/pages-eyes-free.css` (new): full-viewport dialog (`100dvh`, safe areas), the board grid
   (`1fr 1fr` / rows `1fr 1.6fr 1fr`, gap 12 px in rem), content capped at `40rem` and centred from 768 px; landscape
   with `max-height: 32rem`: three columns, the toggle spanning both rows in the middle; `.is-dim::after` overlay
   `--ground` at 0.7, no transition under `prefers-reduced-motion`; targets far above 44 px; all four themes.

Acceptance tests (`internal/web/eyes_free_browser_test.go`, `TENDSHELF_TEST_BROWSER`)
- `TestEyesFreeOpenClose`: menu item opens it, focus on toggle, Escape and Back close it, focus on the ⋯ button,
  `#audio` keeps playing across open and close; link mode: no item, no `#eyes-free`.
- `TestEyesFreeButtons`: back/forward move by the person's seconds, toggle pauses and plays, bookmark POSTs once and
  a toast without a link shows inside `#eyes-free`, sleep cycles through the five states with matching labels.
- `TestEyesFreeDim`: after a real 10.5 s wait the class is set (no test hook in shipped markup); the first click
  undims and does not toggle playback; the second does.
- `TestEyesFreeWakeLock`: with a stub `navigator.wakeLock`, request on open, release on close, re-request on visible.
- `TestEyesFreeShake`: sleep 15 min from Eyes-free, `tsPlayerFull.debug.advanceSleep` into the fade, dispatch two
  synthetic `devicemotion` peaks: about 10 minutes are added (the shake proof of decision 0013).
- `internal/web/player_full_test.go`: the menu item renders with the asset URLs; hidden when `Link`.
- `internal/web/static` precache test: `/static/<hashed eyes-free.js>` and the CSS are in `Precache()`.
- Captures `visual_eyes_free_test.go` `TestEyesFreeVisual`: 390x844 beside `EyesFreePhone.png`, 768, 1024, 1440,
  390 landscape (844x390), dark and light, plus dimmed at 390; `make visual` before/after proves other pages unchanged.
- Budgets (rows in `motion_test.go`): `eyes-free.js` new row `{3 * 1024, 24 * 1024}`, `player-full.js` to
  `13*1024 + 256`, `player.js` to `13 * 1024`; report the measured bytes; never minify.
Out of scope: the reader's Eyes-free (R-phases), voice output, a route or URL, Eyes-free from the bar or Home.

---

## Package K: scheduled backups (builder, sonnet)
Invariants
- Only `share.ManageServer` (the owner) reaches any backup route or job status; everyone else gets 404.
- A copy is either verified and complete or removed. Copy rotation touches only `tendshelf-copy-*`; the
  pre-migration rotation stays byte-for-byte as it is (its tests must pass unchanged).
- A backup never blocks requests (own `mode=ro` connection) and never runs twice at once.

Steps
1. `internal/db/backup.go`: `copyName` regex `^tendshelf-copy-v(\d+)-(\d{8}T\d{6}\.\d{3}Z)\.db$`, `CopyKeep = 7`;
   refactor `rotateBackups(dir, keep)` into `rotateMatching(dir, re, keep)` (the old name calls it with `backupName`);
   `func WriteCopy(ctx, st *Store, dataDir string, now time.Time) (Copy, error)`: mkdir 0700, open
   `sql.Open("sqlite", roDSN(st.Path()))` + busy_timeout, `VACUUM INTO ?`, close, `os.Chmod(dest, 0o600)`,
   `verifyBackup(ctx, dest, st.SchemaVersion())`, on any error remove dest; then rotate copies.
   `type Copy struct{ Name string; At time.Time; Bytes int64 }`; `func ListCopies(dataDir string) ([]Copy, error)`
   newest first (missing dir = empty, no error). Guess to check: if `mode=ro` refuses `VACUUM INTO`, use a
   dedicated reader conn without `query_only`; report which.
2. `internal/backups/backups.go` (new package): `const Kind = "db.backup"`; settings key `backup.schedule` values
   `off|daily|weekly` (missing = `daily`); `type Service`, `New(st, runner, dataDir, now, log)`, `Register()` (Kind,
   Concurrency 1, MaxAttempts 1, handler: `db.WriteCopy`, result `{"name","bytes"}`, error
   `jobs.Error{Code:"backup_failed", Text: "The backup didn't finish. Check that the data folder has free space.",
   Permanent: true}`), `Schedule(ctx)`, `SetSchedule(ctx, v) error` (`ErrBadSchedule`), `Start(ctx, by, reason)
   (jobID string, err)` returning the active job when one exists (`ActiveFor(Kind, "target", "database")`; payload
   `{"target":"database","reason":"manual|scheduled"}`), `Due(ctx) (bool, error)` (off: false; newest copy older
   than 24 h / 168 h minus 15 min, or none; false while a job is active or the latest failed < 1 h ago), `Run(ctx)`
   (first check after 2 min, then every 15 min; `Options.Every` for tests).
3. `cmd/tendshelf/hooks.go`: `registerBackups(d deps) (*backups.Service, error)`; `cmd/tendshelf/main.go`: call it
   before `runner.Start`, pass `Backups:` in `web.Options`, `go bk.Run(ctx)` beside `go writes.Run(ctx)`.
4. `internal/web/server.go`: `Options.Backups`, `server.backups`; `s.registerBackups(mux)` in the list.
5. `internal/web/routes_backups.go` (new): gate `s.allow(w, r, share.ManageServer, Target{Kind: TargetServer})`.
   - `GET /admin/backups` page via `s.prefsPage(..., "admin-backups", "backups", ...)`.
   - `POST /admin/backups/schedule` (`schedule`): bad value 422 (htmx: error toast; plain: page with the field
     error); ok 303 `/admin/backups`.
   - `POST /admin/backups/now`: htmx: 200 status fragment polling `/hx/jobs/{id}` every 2s; plain: 303
     `/admin/backups?job={id}`.
   - `routes_jobs.go` `jobViews[backups.Kind] = (*server).getBackupJob`: owner only else 404; running fragment
     polls; done/failed fragment stops polling (the email test job is the sibling).
6. `internal/web/backups_copy.go` (new, all NEW words): title and category "Backups"; "Automatic backups"; choices
   "Off", "Every day", "Every week"; "Save"; "Last backup {when} · {size}"; "No backup yet"; "Next backup {when}";
   "Copies kept here: {dir}"; "Back up now"; "Backing up…"; "Backed up {when}"; the job error text; "Tendshelf keeps
   the newest 7 copies. Copies made before an update are kept separately."; disclosure "How to restore a copy" with
   "Stop Tendshelf.", "Replace tendshelf.db in the data folder with the copy, and delete tendshelf.db-wal and
   tendshelf.db-shm if they are there.", "Start Tendshelf again." Dates with the helper the Libraries row uses.
7. `internal/web/pages/admin_backups.templ` (new): panels (`ts-group`) like `admin_email.templ`; the schedule as a
   radio group form with a Save button; copies as rows (date, size, mono name); `#backup-status` region.
8. `pages/prefs.templ` + `prefs.go`: `PrefsNav.Backups` (ManageServer), item after Email.
   `pages/profile.templ` + `routes_me.go`: `ProfileView.Backups` ("Last backup {when}" or "No backup yet"), a row
   after Email linking `/admin/backups`.
9. `docs/architecture.md`: the routes row for `/admin/backups`, `internal/backups` in the package table, the restore
   steps.

Acceptance tests
- `internal/db/backup_copy_test.go`: `TestBackupsWriteCopy` (header, quick_check, mode 0600), `TestBackupsRotation`
  (8 copies -> 7; three pre-migration files untouched; and `backupBefore` leaves copies alone),
  `TestBackupsWhileWriting` (a writer transaction open during the copy commits; the copy is consistent),
  `TestBackupsFailureRemovesFile` (unwritable dir).
- `internal/backups/backups_test.go`: `Due` table (off, none, fresh, stale daily/weekly, active job, recent
  failure), `Start` returns the active job, `SetSchedule` rejects "hourly", `Run` enqueues once per due period.
- `internal/web/backups_test.go`: `TestBackupsPage` (owner sees schedule and copies), `TestBackupsForbidden`
  (admin, member, guest: 404 on all three routes and on `/hx/jobs/{backup job}`), `TestBackupsSchedule` (422 and
  303), `TestBackupsNow` (htmx fragment polls, plain 303, a second press returns the same job), CSRF missing: 403.
- `internal/web/routes_matrix_backups_test.go`: `backupsSweepRows()` with `sweepOwnerOnly` (GET 200/404, POSTs
  303/404) and the `append` in `routes_matrix_test.go`.
- Captures `visual_backups_test.go` `TestBackupsVisual`: 390, 768, 1024, 1440, dark and light, empty and with three
  copies and a failure; no board: compare for pattern against the Email page captures.
Out of scope: restore and download from the web, a backup folder setting, encryption, off-site copies.

---

## H: scribe (haiku), after S, E and K merge
- `docs/manual-checks.md`: Eyes-free steps (open from ⋯, five buttons, dim and wake, screen stays on, Escape/Back)
  and the shake step over HTTPS, iOS and Android.
- `docs/copy-additions.md`: the NEW strings reported by S, E and K. `docs/roadmap.md`: the M3b status line.

## Operator decisions (defaults stated; only the first blocks dispatch)
1. JavaScript budget (blocks Step 0): app JS total 52.5 -> 55.75 KB gzip; `eyes-free.js` 3 KB (new, loaded on first
   use only), `player-full.js` 12.75 -> 13.25 KB, `player.js` 12.75 -> 13 KB, `shell.js` 4.75 -> 5 KB.
2. Admins see a person's three numbers on `/admin/users/{id}` (the brief's "listening stats", narrowed: not in the
   list). Default: as stated; say "off" to keep statistics private to each person.
3. Backups default to Every day, owner only, newest 7 kept in `<data>/backups`. Default: as stated.
