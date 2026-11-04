# M3b dashboard: Your listening (`/me/listening`)
Decision `docs/decisions/0014-listening-dashboard.md`; board `docs/design/boards/ListeningDashboard.html` (its `ts-dash`,
`ts-chart`, `ts-heat` rules are the stylesheet to port). Two builder packages (sonnet): **A** data (Go only), then **B**
page; B starts after A merges. Translations: none (English only today). Reused: `progress.Stats`, `statsLocation`,
`weekTime`, `joinNames`, `statsPrivate`, `msgSpeedValue`, `library.Scope`, `visible`/`fromVisible`/`r.cards`,
`components.Cover`, `components.EmptyState`, `components.Icon`, `pageStyled`, `s.notFound`, the stats test helpers.

## Invariants
- Own rows only: every `listening_sessions`/`progress` read binds `user_id` from `currentUser(r)`; nothing reads a person id
  from the request. No admin view, no API, no `/hx/` route, no JavaScript.
- Titles, authors and narrators appear only through `library.Scope` SQL; totals and charts count all own sessions.
- One split rule (`splitSession`) for Profile's `Stats` and the dashboard; pieces of one session sum to `listened_ms`.
- Reads are bounded by `user_id` + a `started_at` range on `listening_sessions_user`; no migration, no new index.
- No `style=""`; CSS in rem (TestRemUnits), tokens only; every word in HTML, never as SVG text except heat labels.

## Package A: data (internal/progress, internal/library)
1. `internal/progress/split.go` (new):
   - `const SplitLookbackMs = 86_400_000` (also the span cap).
   - `func splitSession(startMs, endMs, listenedMs int64, loc *time.Location, fn func(at time.Time, ms int64))`:
     `listenedMs <= 0` → return; `endMs-startMs > SplitLookbackMs` → `startMs = endMs - SplitLookbackMs`;
     `endMs <= startMs` → `fn(start.In(loc), listenedMs)`; else loop `c` from start: `lt := c.In(loc)`,
     `next := time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour()+1, 0, 0, 0, loc)`, if `!next.After(c)` then
     `next = c.Add(time.Hour)`; `e := min(next, end)`; share `listenedMs*(e-c)/span` (ms ints), last piece gets
     `listenedMs - sumSoFar`; `fn(lt, share)`.
   - `func (s *Service) eachPiece(ctx, userID string, loc *time.Location, from, to time.Time, fn func(at time.Time, ms int64, speedPct int)) error`:
     `SELECT started_at, ended_at, listened_ms, speed_pct FROM listening_sessions WHERE user_id = ? AND started_at >= ? AND started_at < ? AND (ended_at > ? OR started_at >= ?)`
     with `from-SplitLookbackMs, to, from, from`; split each row; call `fn` only for pieces with `from <= at < to`.
2. `internal/progress/stats.go`: `load` uses `eachPiece` (key = civil date of `at`); nothing else changes.
3. `internal/progress/dashboard.go` (new):
   - `const (RangeWeek = "week"; RangeMonth = "month"; RangeYear = "year")`; `var ErrRange = errors.New("progress: unknown range")`.
   - `type Period struct { Range string; First time.Time /* civil date, UTC midnight */; Days int; From, To time.Time /* local midnights */ }`
   - `func PeriodOf(rng string, day time.Time, loc *time.Location) (Period, error)`: week = Sunday on/before `day`, 7 days;
     month = the 1st, days in month; year = 1 Jan, 365/366. `From`/`To` = `time.Date(civil..., 0,0,0,0, loc)`.
   - `func (p Period) Shift(n int, loc *time.Location) Period` (n periods earlier/later; month via `AddDate(0, n, 0)` from the 1st).
   - `type Listening struct { Period Period; Days []int64; Hours [24]int64; Weekdays [7]int64 /* Sunday first */; TotalMs int64;
     DaysListened, DaysSoFar int; SpeedPct int; Finished int; Ever bool }`
   - `func (s *Service) Listening(ctx, userID string, loc *time.Location, p Period, now time.Time) (Listening, error)`:
     one `eachPiece` pass; `DaysListened` = days with `>= StatsDayMinMs`; `DaysSoFar` = days of `p` up to and including
     today (all if past, 0 if future); `SpeedPct` = `round5(Σ ms·pct / Σ ms)` (`(x+2)/5*5`), 0 when `TotalMs == 0`;
     `Finished` = `COUNT(*) FROM progress WHERE user_id=? AND finished_at >= ? AND finished_at < ?`;
     `Ever` = `EXISTS (SELECT 1 FROM listening_sessions WHERE user_id = ?)`.
   - `type YearTotal struct { Year int; Ms int64; Finished int }`;
     `func (s *Service) YearTotals(ctx, userID string, loc *time.Location, now time.Time) ([]YearTotal, error)`: years from
     the local year of `MIN(started_at)` (at most the last 10) to `now`'s year, oldest first; one `eachPiece` pass and one
     `SELECT finished_at FROM progress WHERE user_id=? AND finished_at >= ?`, bucketed by local year. Empty → nil.
4. `internal/library/listening_top.go` (new; copy `ContinueListening`'s `visible(sc, true)`, `fromVisible`, `r.cards` pattern):
   - Clip expression (same rule as `splitSession`; comment points to it), with `es = MAX(s.started_at, s.ended_at - 86400000)`:
     `CASE WHEN s.ended_at <= es THEN s.listened_ms ELSE s.listened_ms * (MIN(s.ended_at, ?to) - MAX(es, ?from)) / (s.ended_at - es) END`,
     rows filtered as in `eachPiece`. Per-book sums in a CTE `t(book_id, ms)`, joined to `b`.
   - `type TopBook struct { Book BookCard; Ms int64 }`, `type TopName struct { ID, Name string; Ms int64 }`,
     `type TopListened struct { Books []TopBook; Authors, Narrators []TopName }`;
     `func (r *Repo) TopListened(ctx, sc Scope, userID string, from, to int64, books, people int) (TopListened, error)`:
     visible books only; authors/narrators via `book_people` (`role`), full book time to each person; drop sums
     `< 60_000`; order `ms DESC`, then `b.sort_title COLLATE NOCASE` / `people.name COLLATE NOCASE`, then id.
   - `type FinishedBook struct { Book BookCard; At int64 }`;
     `func (r *Repo) FinishedBetween(ctx, sc Scope, userID string, from, to int64, limit int) ([]FinishedBook, error)`:
     visible, `finished_at` in `[from, to)`, newest first, `limit` capped by `listeningLimit`.
### Package A tests (`go test -race ./internal/progress/... ./internal/library/...`)
- `split_test.go` table (`fn` collects `(date, hour, ms)`): 23:30–00:30 → 30/30 on two days, hours 23 and 0; 10:50–11:10;
  7 ms over 3 pieces sums to 7; `ended == started`; 30 h span counts last 24 h only; America/New_York 2026-03-08 01:30 EST
  to 03:30 EDT (pieces in hours 1 and 3); 2026-11-01 01:00 EDT to 01:00 EST (all in hour 1, 1 Nov); Asia/Kolkata cuts at
  local :00; Australia/Lord_Howe (30-min DST) loop terminates.
- `dashboard_test.go`: `PeriodOf` (Sunday itself starts its week; Feb 2028 = 29; 2028 = 366; unknown → `ErrRange`); `Shift`
  across a year; `Listening` table: month totals; Oct 31 23:30–Nov 1 00:30 gives half to each month; another user's rows
  never counted; `DaysSoFar` past/current/future; `SpeedPct` (60 m at 100 + 20 m at 200 → 125); `Finished` local bounds;
  `Ever` false when empty. `YearTotals`: three years; 12 years capped to 10; finished per year.
- `stats_test.go`: existing cases unchanged; add "23:50–00:10 local marks both days".
- `TestListeningQueryPlan`: `EXPLAIN QUERY PLAN` of the `eachPiece` query contains `listening_sessions_user`.
- `internal/library/listening_top_test.go`: ranking; clip at period edges; hidden book (`listenScope`) absent from Books,
  Authors and Narrators; two authors both credited; under a minute dropped; ties by title; another user ignored;
  `FinishedBetween` bounds, scope, order; per-book sum equals `Listening.TotalMs` for one visible book (±1 ms per row).
- Perf (`internal/web/listening_perf_test.go`, skips without `TENDSHELF_TEST_PERF=1`, joins `make perf`): 4 users × 3 years
  × 30 sessions a day (~33,000 each, 200 books, fixed seed); after one warm-up, median of 5 `GET /me/listening?range=year`
  ≤ 150 ms without `-race`; report the number. Over budget: `PLAN GAP`, do not add an index.

## Package B: page (internal/web, components, CSS)
5. `internal/web/components/iconset.go`: add `sun`, `sunrise`, `sunset` (paths from the board's `ICON` svgs, 24 grid,
   1.75 stroke); `icons_test.go` count 34 → 37. Night uses `sleep`.
6. `internal/web/components/chart.go` (pure, tested) + `chart.templ`:
   `type ChartBar struct { Label, Icon, Title string; Ms int64 }`; `type ChartRow struct { Head string; Cells []string }`;
   `type BarChartView struct { Bars []ChartBar; MaxMs int64; MaxLabel, MidLabel string; Night, Edge bool; Caption string; Heads []string; Rows []ChartRow }`.
   Geometry: slot 10 units, `viewBox="0 0 {n*10} 160"`, `preserveAspectRatio="none"`, base 156, top 4, cap 6 when
   `n <= 12` (class `ts-chart--wide`) else 3; bar `x = i*10+5`, `y1 = 156-cap`, `y2 = 156 - h + cap` with
   `h = Ms/MaxMs*152`, `y2 = min(y2, y1-0.01)`; `Ms == 0` → no line; values formatted `%.2f`. Gridlines y 4 and 80,
   baseline 156; `Night` adds rects x 0 w 60 and x 220 w 20. `templ BarChart(v)`: markup exactly as the board
   (`ts-chart__ticks` five spans, `svg.ts-chart__plot aria-hidden`, `<g><title>` per bar, `ts-chart__x` labels with
   optional icon, then `table.ts-sr` with caption, `th scope`).
7. `internal/web/components/heat.go` + `heat.templ`: `type HeatCell struct { Col, Row, Level int; Title string }`;
   `type HeatGridView struct { Label string; Cols int; Cells []HeatCell; Months []HeatMonth{Col int; Label string}; Key []HeatKey{Level int; Label string}; Caption string; Heads []string; Rows []ChartRow }`.
   Cell `x = 32+Col*12`, `y = 20+Row*12`, 10×10, rx 2, class `ts-heat__c{Level}`; viewBox `0 0 {32+Cols*12} 104`; labels
   Mon/Wed/Fri at rows 1/3/5 (`y = 20+row*12+9`), months at `y = 13`. Region `div.ts-heat__scroll role=region tabindex=0 aria-label={Label}`.
8. `internal/web/listening_copy.go` (new): every string below as a const; `tickText(ms)` ("45 m", "2 h", "1 h 30 m");
   `niceMax(ms)`: smallest of minutes {10,20,30,60,90,120,180,240,300,360,480,600,720,900,1200,1500,1800,2400,3000,3600,
   4800,6000,7200,9000,12000,15000,18000,24000,30000,36000,48000,60000} ≥ max, else round up to 60,000 min; `speedText(pct)`
   ("1.25", "1.2", "1") into `msgSpeedValue`; `heatLevel(ms)`: `<60_000`→0, `<15m`→1, `<45m`→2, `<90m`→3, else 4;
   `periodLabel(p)`: week "1–7 Nov 2026" / "29 Oct – 4 Nov 2026" / "28 Dec 2025 – 3 Jan 2026", month "October 2026", year "2026".
9. `internal/web/listening_views.go` (new): `listeningView(...) pages.ListeningView` from the A results. Bars: week 7 days
   (S..S), month days (labels 1, 8, 15, 22, 29), year 12 months (J..D); future days no bar, no title. Hours: labels at 0, 6,
   12, 18 with icons `sleep`, `sunrise`, `sun`, `sunset`, `Edge`, `Night`. Parts: Morning 6–11, Afternoon 12–17, Evening
   18–21, Night 22–5; share = round(%); "most" ties go to the earlier of Morning, Afternoon, Evening, Night; weekday ties to
   the earlier day. Cards in order: [Day by day: year], Listening time, Time of day, [Days of the week: month, year],
   Most listened (when any), Finished (when any), [Year by year: year, ≥2 years with Ms > 0].
10. `internal/web/pages/listening.templ` (new): `ListeningView` + `templ Listening(v)`; markup and classes of the board
    (`ts-dash`, `__back`, `__title` `title-2`, `__lead`, `__bar`, `nav.ts-seg.ts-dash__range aria-label`, `__period` with
    `ts-iconbtn` links, `dl.ts-dash__nums`, `__grid`, `section.ts-dash__card aria-labelledby`, `h2.section`, `p.ts-dash__say`,
    `ol.ts-dash__top` with `components.Cover(c, "36")`, `ul.ts-dash__done` with `Cover(c, "fill")` in 5.5 rem cells).
    "Later" is a `span.ts-dash__navgap` for the current period. Empty: `components.EmptyState`.
11. `internal/web/routes_listening.go` (new): `registerListening(mux)` → `GET /me/listening`; call it beside
    `s.registerDownloads(mux)` in `server.go`. Handler: `range` "" → month, else `PeriodOf` (`ErrRange` → `s.notFound`); `at`
    "" → today in `statsLocation(set.TimeZone)`, else `time.Parse("2006-01-02")` (error or year < 2000 → `s.notFound`); a
    period starting after today's → today's. Calls `s.progress.Listening`, `s.lib.TopListened(..., 5, 3)`,
    `s.lib.FinishedBetween(..., 12)`, `s.progress.YearTotals` (year only). Render
    `s.pageStyled(w, r, 200, "Your listening", "profile", pages.Listening(v), nil, "css/pages-listening.css")`.
12. `internal/web/components/listening_stats.templ` + `stats_copy.go`: optional `More, MoreHref` on the view; Profile
    (`statsView(st, false)`) sets "See your listening over time" → `/me/listening`; admin view leaves it empty.
13. `internal/web/static/css/pages-listening.css` (new): port the board's "Proposed css/pages-listening.css" block
    verbatim minus `.ts-dash__cover*` (use `ts-cover`); add `.ts-dash__more` (link row under the Profile panel) to `pages-me.css`.
14. `docs/copy-additions.md`: list the new strings; `docs/roadmap.md`: M3b dashboard row.

## Copy (all NEW unless marked)
Your listening · Only you see these. (reused `statsPrivate`) · Profile (back) · See your listening over time · Show (range
label) · Week · Month · Year · Show {period} (Earlier/Later aria) · Listened · Days you listened · {n} of {m} · Finished ·
Usual speed · Listening time · {total} in {period}, about {avg} on the days you listened. · {total} in {period}. (no day ≥ 1 m)
· Time of day · You listen most in the morning. / in the afternoon. / in the evening. / at night. · Morning · Afternoon ·
Evening · Night · 6 am to noon · noon to 6 pm · 6 pm to 10 pm · 10 pm to 6 am · midnight · 6 am · noon · 6 pm · The light
band is daytime, 6 am to 6 pm. · Days of the week · You listen most on {Weekday}s. · Day by day · You listened on {n} days
in {year}. (1 day) · Every day of {year}, scrolls sideways · No listening · Under 15 m · 15 to 45 m · 45 m to 1 h 30 m · 1 h
30 m or more · Most listened · Books · Authors · Narrators · Year by year · Listening since {Month YYYY}. · Nothing to show
yet · Listen to a book and your time will show here, by day and by hour. · Go to your library · No listening in {period}. ·
captions: Listening time by day / by month · Listening by hour of the day · Listening by day of the week · Listening by month
· Listening by year · heads: Day · Month · Hour · Year · Time · Days you listened · Finished · titles: "Tue 6 Oct: 1 h 12 m",
"October: 31 h 18 m", "6 am to 7 am: 1 h 2 m", "2025: 412 h 5 m".

## Package B tests and gates
- `components/chart_test.go`: geometry table (0 → no line; tiny → dot `y1-0.01`; max → `y2 = 4+cap`; cap 6 vs 3 at 12/13
  bars; Night rects); heat cell x/y and viewBox width for 2026 (53 columns) and 2028.
- `internal/web/listening_test.go` (httptest, fixed clock 2026-11-07 10:00 UTC, `statsListen`/`statsFinish`/`statsZone`):
  `TestListeningPage` (month default; week, year; `at` one month back shows "Later"; current period has none; numbers and
  sentences); `TestListeningBadParams` (`range=decade`, `at=2026-13-01`, `at=1999-12-31` → 404; `at=2030-01-01` → 200 current);
  `TestListeningOnlyOwnData` (owner and member with distinct titles and totals; each page shows only its own; `?user=` ignored);
  `TestListeningHiddenTitle` (revoked grant: title absent, total includes it); `TestListeningEmpty` (never; empty period);
  `TestListeningProfileLink` (`/me` links `/me/listening`; the admin person page does not); `TestListeningNoStyleAttr`.
- `routes_matrix_test.go`: `sweepGet("GET /me/listening", sweepStatic("/me/listening"), sweepSame(200))`; anonymous → sign in.
- `swap_styles_browser_test.go`: Profile → "See your listening over time" → assert `.ts-dash__card` border radius.
- `visual_listening_test.go` `TestListeningVisual`: month and year at 390, 768, 1024, 1440 in dark and light, seeded like the
  board (sessions since March 2024, same clock); record side by side with the board in the report.
- Gates: `make generate` clean, `make check`, `make build`, `TestCSSBudget` (pages-listening.css ≤ 3 KB gzip; report bytes),
  `TestRemUnits`, the JS budget unchanged (no JS file touched).

## Out of scope
Devices chart (needs a per-session device name and a migration); longest session, best streak, goals, badges, comparisons,
notifications; admin or API access to a dashboard; `/hx/` fragments or JS tooltips; real sunrise/sunset by location; Monday
weeks or 24-hour labels; per-book listening history pages; export; caching; translations; any new index or migration.
