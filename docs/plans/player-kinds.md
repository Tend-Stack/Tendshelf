# Plan: one player, three kinds (book, episode, station). P4-P5, after P1-P3 of player-phone-plan.md are merged
PHONE = `@media (width < 768px) and (height > 30rem)`, as in P1. Layout work is PHONE only. Option and correctness fixes apply at every width.
Kind values stay what the server sends today: `""` (book), `"episode"`, `"radio"` (player.js `EPISODE`, `RADIO`). The dialog attribute below spells the book as `book`.

## 1. Invariants
1. One player, one dialog `#player-full`, one data fragment. No second player and no kind-specific template. Kind is decided in one place: `drawKind()` in player-full.js.
2. A book renders exactly as P1-P3 left it, at every width (P1-P3 tests pass unchanged).
3. The fragment addresses stay the same (`/hx/player/{id}`, `/hx/player/episode/{id}`, `/hx/player/radio/{id}`; the service worker caches the episode one). Only data attributes are added.
4. No new server route and no new table. Every button opens an existing sheet, an existing fragment, or links to an existing page.
5. No element in `#player-full` carries an address meant for another kind. A station never gets `/books/…` or `/hx/books/…`, and an episode never POSTs to `/books/{id}/bookmarks`.
6. Authorization is unchanged. Notes come from `/hx/player/episode-panel/{id}` (same `loadEpisode` gate), and the speed goes to `/podcasts/shows/{id}/settings` (same `loadShow` gate).
7. CSP, tokens, 44 px targets, 13 px text, four themes. "Live" is a word, not only a colour. No `style=""`. Budgets move by measurement, never by minifying.
8. `android/`, chapter-bar.js, waveform.js, player.css and bundle.css are not edited. `is-live` stays as an alias: player.css and radio_browser_test.go key on it.

## 2. Decisions
- K5 One attribute pair on the dialog, set only in `drawKind()`: `data-ts-kind="book|episode|radio"` and `data-ts-chapters="one|many"` (many = `book.chapters.length > 1`). Reason: CSS and tests key on one fact instead of scattered toggles.
  Layout pieces (wave, live line, ruler, prev/next) are hidden by CSS on these attributes. Per-item facts (has notes, has chapters, LINK) keep `hidden`, set in `drawKind()` as today. Reason: smallest change, no rewrite of the existing toggles.
- K2 Settings bar, in order. The P1 grid gives hidden items no column.
  - Book: Speed, Sleep, Chapters, Read along (unchanged).
  - Episode: Speed, Sleep, Chapters (only when chapters > 1), Show notes (only when it has notes). That is 2 to 4 columns.
  - Station: Sleep, Radio (a link to `/radio`, which exists).
  Reason: each item is a real feature. Navigation for an episode (the show) goes in the menu, beside "Go to episode".
- K3 More menu:
  - Book: unchanged.
  - Episode: Go to episode, **Go to show** (new item, `book.showHref`), Up next, Mark as played, Eyes-free.
  - Station: the tools stay hidden (player.css:239). `drawKind` also sets `hidden` on Go to book, Mark as finished, Up next and Eyes-free, and gives them safe addresses.
  Reason: nothing in the menu applies to a station, and the hidden DOM must still carry no wrong address.
- K4 Sleep is shared. The sheet: minutes (15/30/60) and Custom for all kinds; End of chapter for books; End of episode for episodes (existing relabel); neither for stations (existing).
  - Night sleep is extended to stations. `getPlayerRadio` emits the night attributes. Reason: falling asleep to the radio is the main use; `maybeStartNightSleep` is kind-agnostic, and a pause already stops the stream.
  - The sleeping label in the bar is P1's D-e for every kind.
- K6 Show notes open as a sheet inside the player, filled from the existing episode-panel fragment (fetch + DOMParser, the pattern player.js:569 uses).
  - Only the `.ts-podnotes` node is imported, so no duplicate `#now-ep-notes` id.
  - The `.ts-podnotes` rules move from pages-podcasts.css to now-panel.css, which every player page loads. This also fixes the 1280+ panel on non-podcast pages.
  - Reason: the text and the gate exist, and leaving the player for a page loses the place on screen.
- Episode speed "like a book": a speed change from the player is saved to the show (`POST {showHref}/settings`, field `speed` only; the handler keeps fields that are not sent).
  - The sheet's sentence becomes the show's.
  - "Make it the usual speed" is hidden for episodes. Today it shows a "usual speed set" toast and saves nothing: a confirmed fault.
- K1 Station wave slot: a static "live line" (`span.ts-player__live`, aria-hidden), 3.5 rem tall. It holds flat 3 px bars on a 5 px pitch, 8 px tall, in `--ts-mark`, drawn with a repeating gradient. No needle and no motion, so it is reduced-motion safe; it is hidden under forced-colors.
  Reason: it keeps the book's rhythm so the screen has no hole, and "all of it is now" reads as live. The word "Live" stays in the header (A1).
- Episode without chapters, PHONE: no book ruler or book label (one segment would repeat the wave). At every width: no prev/next chapter buttons (they do nothing).
- K7 Mini bar, bubble and 768+ bar: no redesign. The fixes that also reach them: the B key, the speed sentence, the Notes and Radio items and Go to show (all widths).
- K8 P4 and P5 run in sequence (both edit player-full.js, player_full.templ and player-phone.css).

## 3. K1 table (PHONE)
| Block | Book | Episode | Station |
|---|---|---|---|
| Header line | chapterWords header (A1) | "Episode" when chapters <= 1, else chapter words (existing) | "Live", accent, uppercase (existing) |
| Cover | cover | show art | logo or initials tile `ts-stnlogo` (existing) |
| Title block | title / chapter title · author | episode title / chapter title · show | station name / now-playing line, one line, ellipsis, keeps its height when empty |
| Wave | yes | yes | live line |
| Times row | elapsed, left in chapter | elapsed, left in chapter or "left in episode" | absent |
| Book ruler + label | yes | chapters > 1: yes, label "… left in episode"; else absent | absent |
| Transport | 5 buttons | chapters > 1: 5; else back, toggle, forward | play/pause centred (existing) |
| Settings bar | Speed, Sleep, Chapters, Read along | Speed, Sleep, [Chapters], [Show notes] | Sleep, Radio |
| Menu | unchanged | + Go to show | not drawn (existing) |

## 4. Markup and data shapes
- `components/player_data.templ`, `PlayerDataView`: add `HasNotes bool`. Inside the `if v.Kind != ""` block: `if v.HasNotes { data-has-notes="1" }`.
- `podcasts_player.go` `episodePlayerView`: `HasNotes: rss.CleanHTML(ep.NotesHTML, rss.NotesMax) != ""`.
- `routes_radio_pages.go` `getPlayerRadio`: add `NightSleep: set.NightSleep, NightFrom: set.NightFrom, NightTo: set.NightTo, NightMinutes: set.NightMinutes`.
- `player.js` parse (near line 241): `hasNotes: d.hasNotes === "1"`. `info()` (line 1515): add `hasNotes` to both lists.
- `player_full.templ`:
  - After the `<p class="body ts-player__author">` line: `<span class="ts-player__live" aria-hidden="true" data-ts-full-live></span>`.
  - Menu, after `data-ts-full-book`: `<a class="ts-player__menuitem" href="/" data-ts-full-show hidden>{ m.GoToShow }</a>`.
  - Bar, after the Chapters item:
    - `<button type="button" class="ts-player-bar__item" data-ts-open-notes hidden aria-haspopup="dialog">@Icon("info", false)<span class="ts-player-bar__label">{ m.ShowNotes }</span></button>`
    - `<a class="ts-player-bar__item" href="/radio" data-ts-full-radio data-ts-close-player hidden>@Icon("radio", false)<span class="ts-player-bar__label">{ m.RadioTitle }</span></a>`. Copy the Up next link's close behaviour.
  - The speed sheet's `<p>` in `.ts-player__forbook` gets `data-ts-speed-for`.
  - After the sleep sheetwrap: `<div class="ts-player__sheetwrap" data-ts-notes-sheet hidden>`, the same structure as the chapters sheet:
    - title id `player-full-notes-title`, text `{ m.ShowNotes }`;
    - body `<div class="ts-player__notes" data-ts-notes-body></div>`.
- Copy (`copy.go` PlayerMsgs and `components/player.templ`):
  - Reused words: `GoToShow: podGoToShow` ("Go to show"), `ShowNotes: podAboutEpisode` ("Show notes"), `RadioTitle: radioTitle` ("Radio").
  - New messages, as data-ts-msg-* attributes:
    - `msgSpeedForShow = "This speed is for this show. Other shows keep your usual speed."` as `data-ts-msg-speed-for-show`;
    - `msgNotesFailed = "The show notes did not load. Try again in a moment."` as `data-ts-msg-notes-failed`.
  - List both in `docs/copy-additions.md`.

## 5. Steps
P4 (builder, sonnet): kinds in the layout and the correctness fixes. Files: player_full.templ (+_templ.go), components/player.templ, copy.go, static/js/player-full.js, static/css/player-phone.css, new `internal/web/player_kinds_browser_test.go`, player_full_test.go, budget tests.
1. Markup: the live line, Go to show and the Radio item, plus `data-ts-speed-for` (section 4; the Notes pieces wait for P5). Copy: GoToShow, RadioTitle, SpeedForShow. Then `make generate`.
2. `player-full.js`:
   - ui list += `full-live`, `full-show`, `full-radio`, `speed-for`.
   - In `drawKind()`:
     - `dialog.dataset.tsKind = live ? "radio" : episode ? "episode" : "book"`; `dialog.dataset.tsChapters = book.chapters.length > 1 ? "many" : "one"`;
     - `ui["full-show"].href = book.showHref || "/"`, `.hidden = !episode || !book.showHref || !!LINK`;
     - `ui["full-radio"].hidden = !live`;
     - for live: `ui["full-book"].href = "/radio"`, `ui["full-finish"].action = "/radio"`, and `hidden = live || !!LINK` on full-book, full-finish, `.ts-player__menuqueue` and `[data-ts-eyes-free-open]` (keep the existing episode and book branches otherwise);
     - `wordsFor(ui["speed-for"], episode, msg("speedForShow"))`;
     - `ui["speed-usual"].hidden = episode || live`.
3. `player-full.js` `addBookmark`: guard `book.kind !== ""` (was `=== "radio"`). This fixes B on an episode. `saveSpeed` stays as is in P4.
4. `player-phone.css`, outside PHONE:
   - `.ts-player__live { display: none; }`;
   - `.ts-player[data-ts-kind="episode"][data-ts-chapters="one"] :is([data-ts-full-prev],[data-ts-full-next]) { display: none; }`;
   - `.ts-player[data-ts-kind="radio"] .ts-player__author { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-height: 1lh; }`.
5. `player-phone.css`, inside PHONE:
   - `.ts-player[data-ts-kind="radio"] .ts-player__live { display: block; order: 1; margin-top: auto; height: 3.5rem; background: repeating-linear-gradient(90deg, var(--ts-mark) 0 3px, transparent 3px 5px) center / 100% 0.5rem no-repeat; }`;
   - `@media (forced-colors: active)` hides it;
   - `.ts-player[data-ts-kind="episode"][data-ts-chapters="one"] :is(.ts-player__ruler, .ts-player__booklabel) { display: none; }`;
   - with 3 transport buttons the toggle stays centred.
6. Tests (section 6, P4 rows). Fix selectors only in existing tests that P4 breaks (likely `TestEpisodePlayerBarVisuals` prev/next). Re-measure budgets.
P5 (builder, sonnet): notes, night sleep, episode speed, docs. Files: player_data.templ, podcasts_player.go, routes_radio_pages.go, player.js, player-full.js, player_full.templ, player.templ, copy.go, now-panel.css, pages-podcasts.css, player-phone.css, tests, docs.
7. Server and parse lines from section 4 (HasNotes, radio night sleep, `hasNotes`).
8. Notes markup and copy (section 4).
9. `player-full.js`:
   - ui += `open-notes`, `notes-sheet`, `notes-body`;
   - `sheetWraps.notes = ui["notes-sheet"]`;
   - `actions["open-notes"] = () => openSheet("notes", ui["open-notes"])`;
   - in `openSheet`: `if (name === "notes") fillNotes();`, and the focus fallback becomes `… || wrap.querySelector("button[data-ts-sheet-close]")`;
   - `drawKind`: `ui["open-notes"].hidden = !(episode && book.hasNotes)`;
   - when `book.id !== ui["notes-body"].dataset.tsFor`, empty the body.
   - `fillNotes()`:
     - returns if `dataset.tsFor === book.id`. Otherwise keep `const ep = book`.
     - `fetch("/hx/player/episode-panel/" + encodeURIComponent(ep.id), { headers: { "HX-Request": "true" } })`.
     - On `!res.ok` or a throw: the body gets one `<p class="meta">` with `msg("notesFailed")`, and tsFor is not set.
     - Otherwise parse with DOMParser and take `doc.querySelector(".ts-podnotes")`. If it is missing, use the same failure line. If `book === ep`: `replaceChildren(document.importNode(node, true))` and `dataset.tsFor = ep.id`.
10. `saveSpeed`: an episode with `book.showHref` POSTs `book.showHref + "/settings"`, body `speed=` + `toFixed(2)`, with the same headers and keepalive. A station returns early. A book is unchanged.
11. CSS:
   - move pages-podcasts.css:92-97 (`.ts-podnotes` rules) verbatim to now-panel.css;
   - player-phone.css outside PHONE: `.ts-player__notes { padding: 0 var(--space-16) var(--space-16); max-height: 60dvh; overflow-y: auto; overscroll-behavior: contain; }`.
12. Docs:
   - `docs/decisions/0043-player-kinds.md`: the K1 table, K2-K6, the `data-has-notes` addendum to 0038's fragment shape;
   - roadmap status and changelog;
   - copy-additions rows.
   Captures go to `.local/tests/player-kinds/<run-id>/`.

## 6. Acceptance tests (`player_kinds_browser_test.go` unless named; helpers `radioBrowserWorld`, `podWavWorld`/`podPlayServer`, `fullpOpenPlayer`)
P4:
1. `TestPlayerKindStationPhone` (390x844):
   - `data-ts-kind="radio"`;
   - not rendered: the wave input, `.ts-scrub__times`, `.ts-player__ruler`, open-speed, back, forward, prev, next;
   - only the toggle in the transport, its centre x = viewport centre ±1;
   - header text "Live"; the live line visible, 56±1 px;
   - the bar has 2 equal items, Sleep and Radio;
   - choosing 15 min sets the bar label `^\d+:\d\d$`. `tsPlayer.debug` fast-forwards the sleep timer (the same method as `TestRadioLivePlayer`), then `state().playing` is false and the audio src is let go.
2. `TestPlayerKindStationMenuSafe`: no `[href]` or `[action]` in `#player-full` contains `/books/` + the station id or `/hx/books/`. Go to book, Mark as finished and Eyes-free are not rendered.
3. `TestPlayerKindEpisodeChapters` (390x844, an episode with 3 chapters seeded via `chapters_json`):
   - `data-ts-chapters="many"`; 5 transport buttons; 3 segments; the bar Speed, Sleep, Chapters;
   - the menu has "Go to show" with href `/podcasts/shows/{id}`.
4. `TestPlayerKindEpisodeWhole` (no chapters): no ruler and no booklabel, no prev/next (also at 1440); the bar Speed, Sleep; the header "Episode".
5. `TestPlayerKindEpisodeNoBookmark`: B on an episode sends no request to `/books/…/bookmarks` (`seen()` from podPlayServer) and adds no toast. The speed sheet shows the show sentence and no "make usual" button.
6. `TestPlayerKindsNoDeadBand` (book, episode with and without chapters, station × 360x640, 390x844, 430x932):
   - the body does not scroll and nothing overlaps;
   - the auto gaps between consecutive visible blocks are equal ±1 px (the gap before the bar +4 ±1) and ≥ 7.5.
7. `TestPlayerKindsThemes` (episode and station × dark, light, sepia, contrast):
   - the live line `--ts-mark` against `--ground` ≥ 3;
   - "Live" ≥ 4.5;
   - the now-playing line clipped only by ellipsis (one line).
8. `TestPlayerKindBookUnchanged`: the P1-P3 `TestPlayerPhone*` pass untouched. `data-ts-kind="book"`. The bar has the 4 items as before.
9. `player_full_test.go TestFullPlayerMarkup`: the new attributes (`data-ts-full-live`, `data-ts-full-show hidden`, `data-ts-full-radio hidden`, `data-ts-speed-for`).
P5:
10. `podcasts_player_test.go TestPlayerDataEpisodeHasNotes`: notes → `data-has-notes="1"`; none, or notes `<script>x</script>` only → absent.
11. `radio_pages_test.go TestPlayerDataRadioNightSleep`: night sleep on → `data-night-sleep="1"` plus from/to/minutes; off → absent.
12. `TestPlayerKindEpisodeNotes` (390x844 and 1440):
    - the Show notes item opens the sheet with the notes text; focus on its close button; Escape restores focus;
    - no duplicate ids in the document;
    - an episode without notes has no item;
    - a 500 from the panel shows the failure line, and a second open retries.
13. `TestPlayerKindEpisodeSpeedSaved`: choosing 1.5 POSTs `/podcasts/shows/{id}/settings` with `speed=1.50`. A reload of the episode plays at 1.5. Books still POST `/hx/books/{id}/speed`.
14. `TestPodcastNotesStyleMoved`: an episode page's `.ts-podnotes` computed font-size is 17 px, as before.
15. `TestPlayerKindsCaptures` (opt-in): `kind-{book,episode-chapters,episode-whole,station}-dark-390x844.png`, seeded ids.
Gates per package: `make generate` (clean tree), `make check`, `make build`, the touched browser tests at 390 and 1440 with `TENDSHELF_TEST_BROWSER`, `radio_browser_test.go` and `podcasts_player_browser_test.go` in full.

## 7. Budgets expected to move
- `motion_test.go`/`perf_weight_test.go` rows: `player-phone.css`, `player-full.js`, `player.js` (P5), `now-panel.css` (+) and `pages-podcasts.css` (−). Raise to the measured size rounded up to 256 B, with the comment "player kinds (2026-11-14)".
- player.css and bundle.css must not move.

## 8. Out of scope
- Saving or removing a station from the player (`postRadioSave` answers with a page redirect; it needs its own fragment answer).
- Favouriting an episode from the player.
- A now-playing history.
- Share and download in the player.
- An episode "end of chapter" sleep choice.
- Eyes-free for stations.
- Redesigning the mini bar, the bubble, or 768+.
- `android/` and native sleep changes.
- Translations.

## 9. Open questions (recommended answer in brackets; work is not blocked)
1. Should an episode's speed change from the player save to its show, like a book's? [Yes, step 10. Drop step 10 if not.]
2. Should night sleep also start for stations? [Yes, step 7, one struct line.]
3. Should the station bar's second item be a link to Radio, or should Sleep stand alone, full width? [The Radio link.]
4. Should the station's wave slot be the static live line, or nothing, so the gaps grow? [The live line.]
