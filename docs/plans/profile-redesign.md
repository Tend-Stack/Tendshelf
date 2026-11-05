# Profile redesign (plan, 2026-11-05)

Decision [0021](../decisions/0021-profile-redesign.md) (read it first). Board `docs/design/boards/Profile.html`: its block
"New: port these" is the CSS to port, its markup is the target. Three builder packages in order: **PR1 devices**,
**PR2 page**, **PR3 Home row**. PR3 waits for the operator's word on Home's image budget. English only; no scribe package.

Reused: `ContinueBooks`, `FinishedBetween`, `Shelves`, `shelfRow`, `homeHeroView`/`textHeroView`, `listenRulerFor`,
`deviceRows`, `playLinkFor`, `link.button`, `readButton`, `mayReadBook`, `withCardProgress`, `cardView`, `BookCard`,
`Cover`, `ListeningStats`, `pageStyled`, `prefsPage`, the player's resume watcher. Extended: `CardView` (+`Left`),
`ListeningStats` (links wrapper), prefs list (+devices). Replaced: Profile's layout. No migration, module, JS or query.

Test prefix for all new tests: `TestBookFirst`; helpers `bookfirst`; captures `bookfirst-*.png`; new test files
`me_bookfirst_test.go`, `me_bookfirst_devices_test.go`, `me_bookfirst_home_test.go`, `me_bookfirst_browser_test.go`.

## Invariants (all packages)
1. Every listed book comes through a `library.Scope` read; nothing is fetched and filtered in Go.
2. All data is the signed-in person's: `currentUser(r)`; no person id from the request; no admin view of these lists.
3. At most one resume control per book on `/me` and on Home's row (Home's row has none). Never the other format's button.
4. A control is drawn only when the person has that right (`playLinkFor` non-nil, `mayReadBook` true).
5. Copy-deck words stay exact. No `style=""`, no new JS, no `hx-on`. CSS in rem, tokens only.
6. Sign out keeps working per session, and "Sign out everywhere else" is unchanged in effect.
7. Stats remain private: "Only you see these." stays; the admin person page does not change.
8. No page gains a request from the new stylesheet (Home stays at or under 61).

## New copy (`internal/web/copy.go` unless noted; voice BRAND.md)
`profileAlso = "Also in progress"`; `profileFinished = "Finished lately"`; "See all" is a literal, as on Home;
`profileEmptyTitle = "Nothing in progress"`; `profileEmptyBody = "Start a book and it will wait for you here."`;
the action reuses `listenEmptyAction`; `profileAccount = "Account"`; no shelves reuses `shelfEmptyListHead`;
`rowResume = "Resume %s"`; `rowContinueReading = "Continue reading %s"`.
`settings_copy.go`: `devTitle = "Your devices"`; `devLead = "Everywhere you are signed in. Signing out of one leaves the
others as they are."`; `devSignIns = "%d sign-ins · %s"`; `devLineOnly = "Only this device"`; `devLineOne = "This device and
1 other"`; `devLineMany = "This device and %d others"`; `devOneAria = "Sign out %s, %s"`.

---------------------------------------------------------------------------------------------------------------
## PR1: Your devices — about 100 tool calls
1. `internal/web/pages/profile.templ`: `ProfileDevice` gains `Session bool`. `ProfileView.Devices`/`Others` become
   `DevicesLine string`; delete the devices section; add the row (`href="/me/devices"`, title "Your devices", caption
   `DevicesLine`) where the section was, for now.
2. `internal/web/pages/devices.templ` (new): `type DeviceLine struct { ProfileDevice; Sessions []ProfileDevice }`,
   `type DevicesView struct { CSRF string; Lines []DeviceLine; Others int }`, `templ Devices(v)` as on the board: h1
   `title-2 ts-me__title`, lead, then for each line either the existing row markup (keep `.ts-me__device`,
   `aria-label="Sign out {Title}"`, no button when `Current`) or, when `len(Sessions) > 1`,
   `<details class="ts-me__devgroup"><summary class="ts-me__row ts-me__device">` icon, Title, Meta, `ts-me__chev`
   `</summary>` then one `.ts-me__row.ts-me__devone` per session: `Meta` and its form, `aria-label` from `devOneAria`
   (pass it in `ProfileDevice`, new field `Aria`; the single row sets it to `"Sign out " + Title`). Closed by default.
   "Sign out everywhere else" when `Others > 0`, unchanged.
3. `internal/web/me_views.go`: `deviceRows` sets `Session: true` on browser rows; unchanged otherwise.
   `func groupDevices(rows []pages.ProfileDevice) []pages.DeviceLine`: keeps `rows` order; a non-current row with `Session`
   joins the line of the first non-current session with the same `Title`; a group of one is a plain line; a group's
   `Meta` is `fmt.Sprintf(devSignIns, n, newest.Meta)`. `func devicesLine(others int) string`: 0, 1, many.
4. `internal/web/routes_me.go`: `mux.HandleFunc("GET /me/devices", s.getDevices)`; `getDevices` reads as `profileView` does
   and calls `s.prefsPage(w, r, 200, devTitle, "profile", "devices", false, pages.Devices(v), nil, pages.MeStyles...)`.
   Redirect targets at lines 293, 308, 322 become `/me/devices`; `/login` redirects stay. `profileView` keeps the two
   list reads for `DevicesLine`.
5. `internal/web/pages/prefs.templ`: append `{"devices", "Your devices", "/me/devices"}` to `prefsPersonal`; doc comment of
   `Current`.
6. `internal/web/static/css/pages-me.css`: the board's four `ts-me__devgroup`/`ts-me__devone` rules.
7. Update pins: `me_devices_test.go` (page from `/me/devices`), `me_test.go` `TestSessionSignOut` (page and `Location`),
   `routes_matrix_test.go:562` and the sign-out-others row, `routes_matrix_devices_test.go:47` (target `/me/devices`),
   add `sweepGet("GET /me/devices", sweepStatic("/me/devices"), sweepSame(200))`; `a11y_pages_test.go` row `me-devices`;
   `nav_test.go` `"/me/devices": "profile"`; `me_devices_browser_test.go` (navigate to `/me/devices`); `visual_me_test.go`
   (add `/me/devices` capture).
### Tests (`me_bookfirst_devices_test.go`)
- `TestBookFirstGroupDevices`, table: no sessions; current only; current + same label (current stays alone, one plain
  line); three same label; two labels; apps with equal names never fold; order follows the newest of each group.
- `TestBookFirstDevicesPage`: 6 logins with one user agent: one `<details>`, 6 forms, each signs out only its own session
  (303 to `/me/devices`, the rest stay valid); another person's sessions never appear; anonymous is sent to login.
- `TestBookFirstDevicesLine`: `/me` has the line in all three wordings and no `/signout"` session form.
- `TestBookFirstDevicesFold` (browser, 390 and 1440): open the fold by keyboard, sign out one, targets at least 44 px.
Gates: `go test -race ./internal/web/...`, `make generate`, `make check`, `make build`.

---------------------------------------------------------------------------------------------------------------
## PR2: the page — about 200 tool calls
1. `internal/web/components/cover.templ`: `CardView.Left string` ("4 h 2 m left", "Chapter 9 · 37 %"). `book_card.templ`:
   after the byline, `if v.Left != "" { <span class="ts-card__author ts-num">{ v.Left }</span> }` (not truncated).
2. `internal/web/ruler_views.go`: extract `func bookLeftText(posMs, durMs int64, speed float64) string` (the existing
   lines, including the `"0 m"` fallback) and call it from `listenRulerFor`.
3. `internal/web/pages/home_listening.templ`: extract `templ HeroCard(v HomeHeroView)` (the `<section>`); `HomeHero` keeps
   the `#home-hero` wrapper and calls it. Add class `ts-book-block` to `ts-hero__cover`.
4. `internal/web/home_views.go`: split `func (s *server) heroFor(ctx, r, l library.Listening) (*pages.HomeHeroView, string, error)`
   out of `homeHeroView` (the text branch and the audio branch); behaviour unchanged.
5. `internal/web/pages/shelves.templ`: extract `templ ShelfRow(r ShelfRowView)` (the `<a class="ts-row ts-shelf-row">`) and
   use it in both lists.
6. `internal/web/components/listening_stats.templ`: wrap the three links in `<div class="ts-stats__links">`, drawn only when
   `v.More != ""`. Keep each link's exact markup (tests pin it). `pages-me.css`: add `.ts-stats__links`.
7. `internal/web/me_books.go` (new):
   `const (profileBooks = 9; profileShowAll = 6; profileFinishedN = 6; profileShelvesN = 3)`.
   `func (s *server) profileBooksView(r *http.Request, speed float64) (pages.ProfileBooksView, error)`:
   - `ls := ContinueBooks(ctx, s.scope(r), u.ID, profileBooks)`, then pop from the front until `heroFor` returns non-nil;
     the rest are tiles. `SeeAllURL = "/libraries?show=" + library.ShowUnfinished` when the first `len(ls)`, before popping, is above `profileShowAll` (the phone shows the hero and five rows).
   - Tiles: `prog := progress.ForBooks`, `reads := ReadingForBooks`, `s.lib.Book` (`ErrNotFound` skips the tile; other errors
     return), `link := playLinkFor(ctx, u, book, prog[id] or nil)`, `mayRead := mayReadBook`.
     Control: `l.Format == BookText && mayRead` → read; `l.Format != BookText && link != nil` → play; `mayRead` → read;
     `link != nil` → play; else none. Read: `readButton(book, rp, false)`, `Round = true`,
     `AriaLabel = fmt.Sprintf(rowContinueReading, title)`. Play: `link.button(fmt.Sprintf(rowResume, title), true)`.
   - `Card := cardView(l.Book)`; `Left`: text → `fmt.Sprintf(readWhere, l.ChapterIdx, l.PercentBP/100)`; audio with
     `DurationMs > 0` → `fmt.Sprintf(bookWhereShortBook, bookLeftText(l.PositionMs, l.DurationMs, speed))`; else empty.
     `withCardProgress` over all tiles.
   - `Finished`: `FinishedBetween(ctx, s.scope(r), u.ID, 0, s.now().UnixMilli()+1, profileFinishedN)`, `cardView`;
     `FinishedURL = "/libraries?show=" + library.ShowFinished` when any.
   `func (s *server) profileShelvesView(r) ([]pages.ShelfRowView, error)` for `canShelve`: `s.lib.Shelves`, first
   `profileShelvesN` through `shelfRow`.
8. `internal/web/pages/profile.templ`, rewritten after the board. Root `<div class="ts-me ts-profile">` (`.ts-me` stays for
   the swap test). `var ProfileStyles = []string{"css/pages-home.css", "css/pages-library.css", "css/pages-shelves.css",
   "css/pages-me.css", "css/pages-profile.css"}`. New types: `ProfileBook{Card components.CardView; Play *components.PlayButtonView;
   Read *components.ReadButtonView}`, `ProfileBooksView{Hero *HomeHeroView; Also []ProfileBook; SeeAllURL string; Finished
   []components.CardView; FinishedURL string}`. `ProfileView` gains `Books ProfileBooksView`, `ShelfRows []ShelfRowView`,
   `CanShelve bool`. Structure:
   - `header.ts-profile__head`: `Avatar` lg, `.ts-profile__who` (`h1.title-2.ts-profile__name`, `.ts-me__sub`),
     `a.ts-btn.ts-btn--quiet.ts-profile__settings href="/me/settings"` "Settings". Banner under it.
   - `.ts-profile__cols` > `.ts-profile__books`: `HeroCard` or the empty state (`.ts-empty.ts-profile__empty`, h2 title,
     body, link to `/libraries`); "Also in progress" section when `len(Also) > 0` (`ts-home__rowhead`, h2 `section`,
     See all when set; `ul.ts-pbooks` of `li.ts-pbook` with `BookCard` then `PlayButton` or `ReadButton`); "Finished
     lately" when any (`div.ts-profile__done` of `BookCard`).
   - `.ts-profile__side`: `ListeningStats`; `section.ts-profile__card` Your shelves when `CanShelve` (overline, See all to
     `/shelves`, `ul.ts-shelf-list` of `ShelfRow`, or a caption with `shelfEmptyListHead`); `section.ts-profile__card
     .ts-profile__account`: overline Account, Change password or Keep your place, Your devices; then Your server rows
     (unchanged, with `h2.ts-overline.ts-profile__group`, drop `ts-me__hub` and `ts-group`); Sign out; footer.
   - Delete the "Settings" row. Keep `aria-labelledby` pattern; ids `pf-stats`, `pf-also`, `pf-done`, `pf-shelves`, `pf-account`.
9. `internal/web/routes_me.go`: `getMe` calls `s.pageStyled(w, r, http.StatusOK, "Profile", "profile", pages.Profile(v), nil,
   pages.ProfileStyles...)`. `profileView` fills `Books` (pass `set.Speed`), `ShelfRows`, `CanShelve`; drop `Shelves` text.
   Comment on `Stats` ("the first of Profile") updated.
10. `internal/web/static/css/pages-profile.css` (new): the board's "New" block, minus the `ts-me__dev*` rules. Plus
    `.ts-profile .ts-stats { margin: 0; }`, `.ts-profile .ts-me__signout { margin-top: var(--space-8); }`,
    `.ts-profile .ts-me__foot { margin-top: var(--space-4); }`. In the app `.ts-home__hero`'s viewport rules already apply;
    add `.ts-profile__books .ts-home__hero { margin-block: 0; }`. Do not squeeze: report any overrun of 3 KB.
    `pages-settings.css:49`: remove `.ts-me__hub`.
11. `internal/web/components/layout.templ`: add `"css/pages-email.css"` to `noPrefetch`, comment why.
12. Update pins: `swap_styles_test.go:46` (`/me` wants the five names); `swap_styles_browser_test.go` (phone row to shelves is
    now the See all in Your shelves; `.ts-me` check stands); `me_test.go` `TestProfileAdminRow` (still passes: "Your server"
    rows stay); `a11y_pages_test.go` stays; `visual_me_test.go` (drop `ProfilePhone` copy, copy nothing in its place);
    `visual_stats_test.go` stands. `stats_test.go`, `listening_test.go`, `reading_stats_test.go`, `invite_test.go`,
    `users_admin_test.go`, `settings_prefs_test.go` must pass unchanged; if one does not, the page is wrong, not the test.
13. `internal/web/perf_weight_test.go`: add `sweepPerfPage{"profile", "/me"}` and a `"profile"` row, set by the file's rule
    (maximum over three runs and four widths, times 1.15, next KiB; requests maximum plus 2). Run with
    `TENDSHELF_TEST_PERF=1`. Do not touch another row.
14. `docs/design/screens.md`: `ProfilePhone` row points to `docs/design/boards/Profile.html`, capture `TestBookFirstVisual`.
    Do not touch `docs/roadmap.md`, `docs/manual-checks.md` or `desktop/` here; report the roadmap line for the lead.
### Tests (`me_bookfirst_test.go`)
- `TestBookFirstOrder`: on the page, hero < "Also in progress" < "Finished lately" < "Your listening" < "Your shelves" <
  "Account" < Sign out; exactly one h1.
- `TestBookFirstNoShell`: `/me` has no `ts-prefs__nav`, for owner, member and guest; `href="/me/settings"` present;
  `/me/settings` still has its person card linking to `/me`.
- `TestBookFirstFormats`, table on one person: audio last; text last; both, audio newer; both, text newer; finished audio
  with text under way (read control, never "Listen again"); text last with no read right (play from the real place); no
  right at all (card, no control). Each: one control for the book, `aria-label` exact, no "Listen" or "Read" secondary.
- `TestBookFirstOneResume`: for 9 books, `data-ts-play="{id}"` plus reader links count one per book.
- `TestBookFirstScope`: a book taken out of a member's reach disappears from hero, Also and Finished; another person's
  progress never shows; a guest sees no shelves card.
- `TestBookFirstCaps`: 12 under way: 9 shown, See all has `show=unfinished`; 6 under way: no See all; 1: no "Also in
  progress"; 0: the empty state; 8 finished: 6 shown, See all has `show=finished`.
- `TestBookFirstLeftLine`: speed 1.5 divides the time left; unknown duration leaves no line; text shows "Chapter 9 · 37 %".
- `TestBookFirstStats`: "Only you see these." and all three links on `/me`; the admin person page unchanged.
- `TestBookFirstNoInlineStyles`, and `TestBookFirstRequests`: `noPrefetch` keeps every Layout page's prefetch count as before.
- `TestBookFirstBrowser` (`me_bookfirst_browser_test.go`, 390, 768, 1024, 1440): no horizontal overflow; targets at least
  44 px; Tab reaches each control with the ring; at 390 five rows, at 1440 tiles with the control inside the cover box;
  a click on a tile's control plays that book and the mini-player stays hidden while the control is on screen; at 1440 with
  the panel open, one column; 200 % text wraps, the time left whole.
- `TestBookFirstVisual`: `bookfirst-{theme}-{width}.png` for dark, light, sepia, contrast at 390, 768, 1024, 1440; compare
  side by side with the board, list differences in the report.
Gates: new and changed tests, `go test -race ./internal/web/...`, `make generate`, `make check`, `make build`, CSS budget
(`internal/web/static`), `TENDSHELF_TEST_PERF=1 TestPageWeight`. `make visual-compare` for Home's only change, the page edges.

---------------------------------------------------------------------------------------------------------------
## PR3: Home's row — about 60 tool calls. Blocked on the operator's word on the budget.
1. `internal/web/pages/home_listening.templ`: `HomeHeroView` gains `Also []components.CardView`, `AlsoHref string`. `HomeHero`,
   inside the wrapper after `HeroCard`, when `len(v.Also) > 0`: `section.ts-home__row` with `ts-home__rowhead`, h2 "Also in
   progress", See all to `AlsoHref`, `div.ts-home__cards.ts-home__cards--three` of `BookCard`. No new CSS. `HeroCard` ignores them.
2. `internal/web/home_views.go`: `homeHeroView` asks `ContinueBooks(..., 4)`, hero from the first usable, `Also` from up to
   three after it (`cardView`, `Left` as PR2, `withCardProgress`), `AlsoHref = "/me"`. Extract the `Left` rule into
   `func continueLeft(l library.Listening, speed float64) string` in `me_books.go` and use it in both places.
3. `internal/web/components/layout.templ`: add `css/pages-invite.css`, `css/pages-folders.css`, `css/pages-upload.css` to
   `noPrefetch` (three covers, three requests).
4. `internal/web/perf_weight_test.go`: `home` `Img` to the figure the operator approves (proposal 41984); nothing else.
### Tests (`me_bookfirst_home_test.go`)
- `TestBookFirstHomeRow`: 5 under way: 3 cards, hero's book not among them, See all `/me`; 1: no row; the row has no
  `data-ts-play` or `data-ts-read` of its own; a book out of scope never shows.
- `TestBookFirstHomeFragment`: `/hx/home/hero` carries the row, and after a save of a row book it becomes the hero and leaves
  the row.
- `TestBookFirstHomeRequests`: Home's requests at or under 61 with the row (`TENDSHELF_TEST_PERF=1 TestPageWeight`).
Gates: as PR2, plus `TestBookFirstHomeVisual`: `bookfirst-home-{theme}-{width}.png` at the four widths in dark and contrast.

## Out of scope (all packages)
Live refresh of the Profile lists; finished ebooks in "Finished lately"; session expiry or cleanup; a bulk "sign out these";
Bookmarks, Finished or Downloads rows on Profile; changes to the admin person page, the admin Devices tab, `/api/v1`, the
service worker, `/me/settings`, the library filters; anything under `desktop/`.
