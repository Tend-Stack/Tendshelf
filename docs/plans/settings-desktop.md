# Plan: Settings as a preferences window, and the wide layout for the remaining pages

Decision record: `docs/decisions/0010-desktop-settings-and-width.md`. Architect plan, 2026-11-03.

## 1. Invariants
- Every category is a canonical URL that returns a full page. Moving between categories uses the body's existing
  `hx-boost` (target `#main`), so the player keeps playing. No new JS, no inline style, no `hx-on`.
- Authorization does not change. Each server route keeps its own `s.allow` check, and a refusal is still a 404. The
  category list only hides links, using the same `share.Can` calls, so it never grants anything.
- Settings save as now: `POST /me/settings` merges only the keys it receives (`settingsFromForm` starts from `cur`).
  No new settings keys and no migration.
- Existing URLs keep working: `/me`, `/me/settings`, `/me/password`, `/admin/users[/…]`, `/admin/libraries[/…]`,
  `/admin/folders/…` and `/admin/email`. Every `/hx/` fragment is unchanged.
- The layout follows the space it actually gets, by container width, not viewport width. That covers large text, the
  rail and the open Now playing panel. Range syntax and rem only.
- Every theme works (dark, light, sepia, contrast). Targets are at least 44 px, focus is visible, every control is
  labelled.
- New CSS goes in `pages-*.css` only (core CSS has about 95 B left). An overrun is reported, never minified away.

## 2. Decisions (with reasons)
1. **The shell is a container-query layout.** `.ts-prefs` is the container `prefs`. It shows two columns at
   `@container prefs (width >= 44rem)` and one column below that. Reason: at 768 px the content area is about 632 px,
   too narrow for a list plus a usable pane, so tablet portrait gets the phone pattern. Two columns at 1024, 1440, 1920,
   at 1440 with the panel open (about 716 px), and at 1440 in large text. This departs from "tablet ≥768"; see 0010.
2. **Phone pattern.** `/me/settings` is the category list, and each category is a page with a "Settings" back link.
   With two columns, `/me/settings` shows the list plus the Playback pane. One URL, no redirect that depends on the
   viewport.
3. **Categories.** Each entry gives the key, its label, the URL and the handler. Personal:
   - `account`: avatar card "Profile", `/me`, `getMe`.
   - `playback`: "Playback", `/me/settings/playback`.
   - `appearance`: "Appearance", `/me/settings/appearance`.
   - `accessibility`: "Easier to use", `/me/settings/accessibility`.
   - `privacy`: "Privacy", `/me/settings/privacy`.

   All four sections use the one handler `getSettingsSection`. "Your server":
   - `users`: "Users & invites", `/admin/users`, `getUsers`. Shown when `s.usersLink(r)` shows it.
   - `libraries`: "Libraries", `/admin/libraries`, `getAdminLibraries`. Shown when `Can(ManageLibraries, server)`.
   - `email`: "Email", `/admin/email`, `getEmail`. Shown when `Can(ManageServer, server)`, the owner only, because an
     admin may manage users and libraries but not the server.

   Sub-pages render in the shell under their parent: `/me/password` under account; `/admin/users/{id}` and its
   stop/disable confirms under users; add-folder, folder emptied and `/admin/libraries/{id}/lookups` under libraries.
   Reason for the URLs: every existing URL is kept, and sub-routes are added only where a split is new.
4. **Marking the current category.** A category page puts `aria-current="page"` on its own item. The index puts
   `aria-current="true"` on Playback, because it is the selected item of a set, not the page. In the one-column index
   that highlight is not drawn.
5. **Panels.** A pane is made of `.ts-group` cards: `--surface`, a 1 px `--line` border, `--radius-lg`, with rows split
   by `--line`. The cards sit in `.ts-groups`, a grid of `repeat(auto-fill, minmax(min(100%, 24rem), 1fr))` with gap
   `--space-24` and `align-items: start`. Auto-fill leaves empty tracks instead of stretching cards, so the measure
   stays at roughly 24-40 rem up to 1920.
6. **Skip back, Skip forward and Text size become rows** (`.ts-pref-row`): the label on the left, `.ts-seg` on the
   right, and the control wraps under the label when the card is narrow. This is the "side by side" that was asked for.
7. **The category list has no icons.** Text items only: no new SVGs and no added bytes. Icons can come later.
8. **The server panes keep their markup.** P2 changes the wrapper and the `@container pane` conversion only, with no
   regrouping into cards.
9. **Downloads becomes a minimal honest page** at `/me/downloads` with no controls. Removing the link was the
   alternative, but the link is in the handoff's navigation (all three navs) and pinned by tests, and today it is a 404.

## 3. Shell component
- File `internal/web/pages/prefs.templ` declares
  `type PrefsNav struct { Current string; Index bool; UserID, Name, Role string; Users bool; UsersWaiting string; Libraries, Email bool }`
  and `templ PrefsShell(n PrefsNav, pane templ.Component)`.
- Markup, outside in:
  - `div.ts-prefs`, and inside it `div.ts-prefs__frame`, which also gets `ts-prefs--index` when `n.Index`.
  - `nav.ts-prefs__nav aria-label="Settings"`, holding:
    - the avatar card link to `/me`: `components.Avatar(UserID, Name, "md")`, the name, and the role;
    - `h2.ts-overline` "Settings" and a `ul` of the four items;
    - only when `Users || Libraries || Email`: `h2.ts-overline` "Your server" and a `ul` of the allowed items, with
      the `UsersWaiting` badge as in the sidebar.
  - `div.ts-prefs__pane`, holding the back link `.ts-prefs__back` → `/me/settings` "Settings", then `@pane`. The back
    link reuses the `shelfBack` markup at `shelves.templ:55` and is left out when `Current == "account"`.
- Items are `a.ts-prefs__item`: at least 44 px high, `--radius-control`, with the current item tinted.
- `internal/web/prefs.go` (new) has two functions:
  - `func (s *server) prefsNav(r *http.Request, current string, index bool) pages.PrefsNav` uses `s.usersLink(r)` and
    `s.can.Can` exactly as `profileView` and `pageStyled` do. It adds no new rule.
  - `func (s *server) prefsPage(w, r, status, title, sideCurrent, current string, pane, sheet templ.Component, styles ...string)`
    calls `pageStyled` with `pages.PrefsShell(...)` and appends `"css/pages-settings.css"`. The sidebar's `current`
    values do not change.

## 4. Layout by content width (`.ts-main` content box)
- **390, and 768 (about 632 px):** one column.
  - `/me/settings` shows the list only; `.ts-prefs--index` hides the pane.
  - Category pages show the back link and the pane.
  - `/me` keeps its hub rows: Settings, the Your server rows, and a new Email row.
- **1024 (about 888 px):** two columns, with nav `minmax(12rem, 15rem)` and pane `minmax(0, 1fr)`, gap `--space-32`.
  The nav is `position: sticky; top: var(--space-24); align-self: start`. Panes hold 1-2 cards per row.
- **1440 (about 1136 px):** two cards per row; with the panel open (about 716 px), one card per row.
- **1920 (about 1616 px):** three tracks.
- **Large text:** the rem threshold grows with the text size. 1440 (rail, about 1304 px) stays at two columns; 1024
  drops to one column.
- **Whenever there are two columns:** hide `.ts-prefs__back` and `.ts-me__hub` (the Settings row and the Your server
  section on `/me`), since the list already holds them.

## 5. Other pages (fill, columns or a narrow card)
- **Shelves list:** drop the 45rem cap. `.ts-shelf-list` becomes a grid of
  `repeat(auto-fill, minmax(min(100%, 24rem), 1fr))` with column gap `--space-32`. A set of shelves has no reading
  order.
- **One shelf:** one column capped at 48rem, while the header fills the width. It is an ordered list with move
  controls, which columns would scramble.
- **Pages that stay narrow cards:**
  - ShelfDelete stays at 30rem, and the admin confirm and add-folder pages (`.ts-admin--narrow`) at 35rem, inside the
    pane. A confirmation stays a short read.
  - Password: 30rem, left-aligned in the pane.
  - Email: 35rem becomes 40rem.
- **Share:** drop the 75rem cap and keep its grid ratios, so it spreads to 1920 without a stranded column.
- **Queue page:** `.ts-upnext-page` goes from 40rem to 48rem, one column, ordered like one shelf.
- **Search:** the field stays at 35rem, since a field 1,500 px wide is worse.
  - `.ts-sf__results` becomes a grid of `repeat(auto-fit, minmax(min(100%, 26rem), 1fr))`. The Books, People and
    Series groups then sit side by side, each in its ranked order.
  - One group alone (`.ts-sf__group:only-child .ts-lib-rows`) lays its own rows out on that grid.

## 6. Steps (packages of about 250 calls or fewer)
**P1: the shell and the personal categories.** P1 and P3 can run in parallel; P2 waits for P1.
1. Add `pages/prefs.templ` and `prefs.go` as in section 3.
2. Add `static/css/pages-settings.css`, holding `.ts-prefs*`, `.ts-groups`, `.ts-group*` and `.ts-pref-row`
   (`flex-wrap: wrap; justify-content: space-between`). The default 3 KB pages budget applies.
3. `routes_me.go`:
   - Add `GET /me/settings/{section}` → `getSettingsSection`. It accepts `playback|appearance|accessibility|privacy`;
     anything else gets `s.errorPage(w, r, 404)`.
   - `getSettings` renders `playback` with `Index` set.
   - `getMe` and the password pages render through `prefsPage(..., "account")`.
   - `postSettings` reads `section`. When it is one of the four, a non-htmx success answers 303 to
     `/me/settings/{section}` and a 422 renders that section; otherwise the current behaviour stays.
4. `me_views.go`: add `SettingsView.Section`, and `ProfileView.Email` set from `Can(ManageServer, server)`.
5. `pages/settings.templ`:
   - `Settings` becomes `SettingsPane(v)`, which switches on `v.Section` to the playback, appearance, accessibility
     and privacy panes.
   - Each pane has an `h1` with the category label, then the existing lead, banner and error, then one form with the
     same `hx-post`/`hx-trigger="change"`/`hx-swap="none"`, plus `_csrf`, a hidden `section` and `.ts-groups`.
   - The groups:
     - Playback: card 1 holds Usual speed, Skip back and Skip forward; card 2 holds the two switches.
     - Appearance: card 1 holds the theme tiles; card 2 holds Match my phone and Tint.
     - Easier to use: card 1 holds Text size and its caption; card 2 holds Bigger player buttons.
     - Privacy: one card with the statement and no form.
   - Keep every control id and name. Drop the old "Profile" back link.
6. `pages/profile.templ`:
   - Put the "Yours" and "Your devices" sections in `.ts-groups` cards.
   - Add `ts-me__hub` to the Settings row and to the Your server section.
   - Add an Email row (`/admin/email`) shown when `v.Email`.
7. `pages/password.templ`: drop its back link, and keep `.ts-me--narrow`.
8. `pages-me.css`: drop `max-width: 40rem` from `.ts-me`. Fit `.ts-me__line` spacing inside the cards.
9. Run `make generate`. Re-baseline `visual_me_test.go`.

**P2: the server categories in the shell.**
1. Switch from `pageStyled` to `prefsPage`:

   | Location | Category |
   | --- | --- |
   | `routes_users.go` lines 119, 245, 404 | `users` |
   | `routes_admin.go` lines 169, 298 (keep `sheet`), 499 | `libraries` |
   | `routes_lookups.go` line 144 | `libraries` |
   | `routes_email.go` line 100 | `email` |

2. `pages-settings.css`: add `.ts-prefs__pane { container: pane / inline-size; }`.
3. `pages-admin.css`: change `@media` to `@container pane` only for rules whose selectors appear in shell pages alone.
   - Check each selector with `grep -l` over `pages/*.templ`. Home loads `pages-admin.css` through `BrowseStyles`, so
     shared rules stay `@media`.
   - Map the breakpoints:

     | `@media` | `@container pane` |
     | --- | --- |
     | `768px` | `36rem` |
     | `768–1023px` | `(36rem <= width < 50rem)` |
     | `1280px` | `56rem` |

   - If this goes over 4 KB, move the `.ts-users*` rules into a new `pages-users.css` and report it.
4. `pages-email.css`: `.ts-email` becomes 40rem.

**P3: the other pages and Downloads.** It shares no files with P1.
1. `pages-shelves.css`, `pages-share.css`, `pages-search.css` and `now-panel.css` (`.ts-upnext-page`) as in
   section 5. Main has uncommitted edits to `now-panel.css` and `bundle.css`, so the orchestrator commits or settles
   them before P3 starts.
2. Add `pages/downloads.templ` with `templ Downloads()`: an `h1` and the existing empty state from
   `components/empty_state.templ`, and no buttons.
3. Add `routes_downloads.go` with `registerDownloads`, which serves `GET /me/downloads` through
   `s.page(w, r, 200, "Downloads", "downloads", pages.Downloads())`. Wire it beside the other `register*` calls (not in
   `routes_me.go`).

## 7. New copy (BRAND voice; listed for the operator)
- New: the list heading "Settings", and the back link "Settings". Existing: the labels "Playback", "Appearance",
  "Easier to use" and "Privacy".
- Profile hub Email row: "Email", with the caption "Send invitations from Tendshelf".
- Downloads:
  - The h1 "Downloads", then "No downloads yet", from the copy deck.
  - A new body: "Saving books to listen without a connection is on its way. Until then, books play while your server
    is in reach."
  - Not the deck's "Download a book…", because there is no download control yet.

## 8. Acceptance tests and gates
**httptest** (`settings_prefs_test.go` for P1, `prefs_server_test.go` for P2, `downloads_test.go` for P3)
- Each of the four sections returns 200 for a member and for a guest; `/me/settings/bogus` returns 404.
- On the index, Playback carries `aria-current="true"`. On `/me/settings/appearance`, Appearance carries
  `aria-current="page"`.
- Posting only `theme=light&section=appearance` leaves speed and skips unchanged, and returns 303 to
  `/me/settings/appearance`. `section=../x` returns 303 to `/me/settings`. A bad value with a section returns 422 and
  renders that section.
- The list per role:
  - member: no "Your server" heading and no `/admin/` links;
  - admin: Users and Libraries but no Email, and `GET /admin/email` returns 404;
  - owner: all three.
  - The existing member 404s on `/admin/*` still hold.
- `GET /me/downloads` returns 200, contains no `<button`, and the nav marks Downloads current.

**Browser and visual** (the opt-in helpers)
- P1 captures `/me`, `/me/settings` and the four sections at 390, 768, 1024, 1440 and 1920, plus 1440 in large text
  and 1440 with the panel open.
- P2 captures the server panes at the same widths. P3 captures Shelves, Share, Queue, Search and Downloads at 390,
  1024, 1440 and 1920.
- One settings pane in all four themes.
- On every capture, assert that `#main` has `scrollWidth <= clientWidth`.
- `no_reload_browser_test.go`: click Playback, then Appearance, then Users in the list. The page-load marker and the
  audio that is playing must survive. A theme change still refreshes, by design.
- Tab reaches every list item, and the focus ring shows on each.

**Gates:** `make check`, `make build`, the budgets in `motion_test.go`, `media_range_test.go`, `rem_units_test.go`, and
a clean tree after `make generate`.

## 9. Out of scope
- Offline downloads (M3 Pocket).
- The board's not-yet-built settings: shake, high contrast, reduce motion, listening stats, export or delete.
- New settings keys and migrations.
- Icons in the list.
- Regrouping the Users, Libraries and Email markup.
- Share inside the shell.
- `/welcome`, the sign-in pages, and TV.
