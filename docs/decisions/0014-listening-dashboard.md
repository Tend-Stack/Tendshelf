# 0014 Your listening: a personal dashboard (M3b)
Status: proposed (2026-11-04). Plan: `docs/plans/m3b-dashboard.md`. Board: `docs/design/boards/ListeningDashboard.html`.
Amends decision 0013 point 1 ("no stats page") and its day rule ("a session counts on the day it started").

## Context
- The operator asked for "a nice dashboard with graphics and charts about the user's listening time, what they did etc.,
  sunset and such, so the user can keep track of their reading/listening time and behaviours".
- **"Sunset" is read as time of day**: when a person listens (morning, afternoon, evening, night), drawn over a fixed
  day and night band with sunrise, sun, sunset and moon marks. Nothing looks up a location or real sunset times.
- What exists (M3b package S): `progress.Stats` (this week, streak, finished in the year, day marks; zone-aware),
  `ui.time_zone` per person, the "Your listening" panel on Profile and on the admin's person page, `stats_copy.go`,
  the panel CSS in `pages-me.css`. Data: `listening_sessions` (wall-clock `listened_ms`, `speed_pct`, `book_id`,
  `device_id`, index `(user_id, started_at)`), `progress.finished_at`, `book_people` (author, narrator), and the
  scope-filtered Home reads in `internal/library/listening.go`.
- A session is one stretch on one device; it ends after a 60 s pause, a speed change or a jump. Its `device_id` is a
  random per-browser id with no name; device names exist only on `progress.device_label` (newest save per book).

## Decision
1. **One page, `GET /me/listening`, for the signed-in person only.** Title "Your listening", reached from a link
   under the Profile panel ("See your listening over time") and its own back link to Profile. The nav marks Profile
   as current. No person id is ever read from the request; there is no admin view of it (the admin keeps the three
   numbers on the person page, decision 0013 point 3), no API route and no `/hx/` fragment.
2. **Range by plain links.** `?range=week|month|year` (default `month`) and `?at=YYYY-MM-DD` (any day inside the
   period; default today in the person's zone). A segmented control of three links (`aria-current="page"`) and
   Earlier/Later links step the period. The body's inherited `hx-boost` swaps `#main`, so the player keeps playing,
   the URL is shareable and it works without JavaScript. **Zero new JavaScript.** Weeks start on Sunday (0013).
   An unknown `range`, an unparseable `at` or a year before 2000 is 404; an `at` after today shows the current period.
3. **Charts, all server-rendered SVG from templ, styled by classes only** (no `style=""`, CSP unchanged):
   - Summary numbers: Listened, Days you listened ("18 of 31", counting days so far), Finished, Usual speed.
   - **Listening time** bars: per day (week, month) or per month (year), with a sentence ("31 h 18 m in October
     2026, about 1 h 7 m on the days you listened").
   - **Time of day** (the "sunset" chart): 24 hour bars over a fixed band, daytime 6 am to 6 pm, night shaded;
     moon, sunrise, sun and sunset icons at midnight, 6 am, noon and 6 pm; then four parts with icon, words, time
     and share: Morning (6 am to noon), Afternoon (noon to 6 pm), Evening (6 pm to 10 pm), Night (10 pm to 6 am),
     and "You listen most in the evening." A strip, not a radial clock: it reuses the bar chart and its labels
     stay legible at 390 px.
   - **Day by day** (year only): a calendar heat grid of that calendar year, Sunday-first columns, a sequential
     single-hue ramp (`--accent-fill` at four opacities over `--raised`) with a numeric key: No listening, Under
     15 m, 15 to 45 m, 45 m to 1 h 30 m, 1 h 30 m or more. Fixed thresholds, not quantiles, so a colour means the
     same every year. On a phone it scrolls sideways and opens at the newest week.
   - **Days of the week** (month, year): seven bars, Sunday first, "You listen most on Fridays."
   - **Most listened**: top 5 books (cover, title, author), top 3 authors, top 3 narrators, by time, each with a
     thin meter scaled to the first row. A book with two authors credits each in full.
   - **Finished**: the covers of books finished in the period, newest first, with the date (BRAND: celebrate with
     the covers, not words).
   - **Year by year** (year range, when two or more years have listening): one bar per year, "Listening since
     March 2024."
   - Style: one hue (`--accent-fill`) plus neutrals, light gridlines (`--line`), a `--line-strong` baseline, thin
     round-capped bars, direct labels and two ticks (the rounded maximum and its half) instead of legends. No
     animation, so reduced motion needs nothing.
4. **Left out, and why.**
   - Devices used: sessions carry only an unnamed per-browser id, which changes when storage is cleared, so a count
     or a chart would mislead. It needs a device name per session (a migration); later.
   - Longest session and streak history: these are records, which decision 0013 excludes ("not gamey"). The
     current streak stays on Profile; the heat grid shows runs without ranking them.
   - Goals, badges, comparisons with other people, notifications: not gamey (brief line 53).
5. **Splitting time.** A session's `listened_ms` is spread evenly over its wall-clock span and cut at every local
   hour boundary in the person's zone (`ui.time_zone`, else the server's), so a session from 23:30 to 00:30 gives
   half to each day and to hours 23 and 0. Cuts use `time.Date(..., hour+1, 0, 0, 0, loc)`: correct for DST (the
   repeated 1 am hour all lands in hour 1; the missing 2 am hour gets nothing) and for half-hour zones. Integer
   shares; the last piece takes the remainder, so the pieces sum to `listened_ms`. A span over 24 hours counts only
   its last 24 hours (a bad clock must not spread one row over years). **Profile's panel adopts the same rule**, so
   "This week" on Profile equals the week total here; no existing test crosses midnight, so they stay as they are.
6. **Privacy.** Every number comes from the person's own rows (`user_id = ?` bound from the session). Totals and
   charts name no book, so they count all of the person's listening, like Profile. Anything that names a book,
   author or narrator (Most listened, Finished) goes through the existing scope SQL (`library.Scope`), so a book
   the person can no longer see never appears by name; its time still counts in the totals. Nothing leaves the
   server; the page is `no-store` and the service worker never caches it (it is not in its list).
7. **Cost, indexes and caching.** Each view is a handful of reads bounded by `user_id` and a `started_at` range on
   the existing `listening_sessions_user` index (lookback 24 h for sessions crossing the period start), one grouped
   scope query each for books, authors and narrators, one for finished books, and on the year range one pass for
   year totals (at most the last 10 years). A heavy listener has about 4,000 to 11,000 sessions a year (a guess from
   the 60 s pause rule). **No new index and no migration**; a test pins the query plan and an opt-in performance
   test on 3 years of synthetic sessions bounds the year view at 150 ms. No cache: the numbers change with every
   save, the page is per person and the reads are bounded.
8. **Empty states.** No session ever: "Nothing to show yet" with "Listen to a book and your time will show here, by
   day and by hour." and a "Go to your library" link. A period without listening: "No listening in {period}." in
   place of the charts; the range and period controls stay.
9. **Layout.** Phone first at 390: one column of cards, the summary in two columns. 768: the summary and the four
   day parts go four across, still one column. 1024 and up: two columns of cards, left-aligned up to
   `--size-content-max`; the heat grid, Finished and the summary span both. Every theme through tokens; the
   contrast theme uses its own `--accent-fill`.
10. **Accessibility.** Each SVG is `aria-hidden` and has a text equivalent: the visible summary sentence plus a
    visually hidden table (`ts-sr`) with a caption. Bars and cells carry an SVG `<title>` as a mouse tooltip only.
    No focusable data points (the tables do that job better). Nothing is told by colour alone: the parts of the day
    have icons and words, the key has numbers, the night band has icons and a caption. The sideways-scrolling heat
    grid is a labelled, focusable region.

## Not decided here
- A week starting on Monday, 12- or 24-hour clock labels by locale, and real sunrise and sunset by location.
- The ramp's first step against "No listening" is faint in the light and sepia themes; the side-by-side captures
  decide whether `.ts-heat__c1` needs a stronger opacity.

## Consequences
- New: route `/me/listening`, `css/pages-listening.css` (default 3 KB budget), three icons (`sun`, `sunrise`,
  `sunset`; the moon is the existing `sleep`), new copy strings (listed in the plan). No migration, no new module,
  no JavaScript.
- Changed: `progress.Stats` cuts sessions at local midnight instead of counting them on the day they started.
