# Car Home: a start page in Android Auto and CarPlay (CH1, CH2)

Operator, 2026-11-19: the car has no home screen and no way to start a book that is not started yet; only Continue, Up next,
Shelves and Downloaded. "A nice looking start page Home would be nice to have." The operator approved the recommendations up front.

## What exists (inventory) and what this plan does with it
- `android/shared/.../LibraryTree.kt`: `Node`, `parseNode`/`nodeId`, `slice`, `BookCard`, `carMine`, `carDownloadedBooks`,
  `carContinueOffline`. **Kept and extended**: new nodes and a new `CarHome.kt` next to it.
- `android/app/.../LibraryTree.kt`: four tabs, `guarded` (5 s budget, `BUDGET_MS`), `online(offline){}`, group-less items.
  **Extended**: Home tab, Library tab, group titles, a late fallback for Home. `Node.Continue` and `Node.Shelves` stay (moved under Library).
- `ios/Tendshelf/CarLibrary.swift`, `CarPlay.swift`: the same four tabs over `CarAPI`. **Extended** the same way.
- Server: `library.ContinueListening`, `SeriesInProgress`, `BecauseYouListened`, `ListBooks` (`ShowNotStarted`, `Format`), and
  `podcasts.NewEpisodes` already back the web Home, all filtered by grants in SQL (`internal/library/grants_scope_test.go`).
  **Reused** by one new read endpoint `GET /api/v1/home`; `GET /api/v1/books?sort=&show=&format=&limit=` is **reused** unchanged
  for the Library lists. No parallel query code: the handler only calls these functions.
- Words: the car strings live in `android/app/src/main/res/values/strings.xml` and iOS reads the same keys (`CarWords.keys`).

## Invariants
1. Every book the car lists comes from a query that filters by the person's grants in SQL, and each book row is checked once more
   with `share.Can(u, share.ListenBook, target)` (the `/continue` rule). No query fetches everything and filters in Go.
2. The car never lists a text-only book (`Kind == library.BookText`): it cannot play one.
3. Every car list answers within 5 s (`BUDGET_MS`, `CarLibrary.budget`), with rows or plain words. Home, unlike the other lists,
   answers its offline sections when the network fails or is late, so Home is never an error while downloads exist.
4. No book appears twice on Home. Section order is fixed: Continue listening, Your series in progress, New on the shelf,
   Because you listened to ..., New episodes (offline: Continue listening, Downloaded).
5. Short lists (driver distraction): Home holds at most 26 rows (6+4+8+4+4); every other book list at most 120 rows; no text entry
   except the system's own search (Android Auto only).
6. Both cars show the same four tabs in the same order with the same words: Home, Up next, Library, Downloaded.
7. Requests go only to the signed-in server with the device token (unchanged); covers go through `CoverProvider` / `CarLibrary.picture`.
8. Node ids already in use (`continue`, `queue`, `shelves`, `downloaded`, `podcasts...`, `radio...`) keep their meaning.

## Decisions
- **Tabs: Home, Up next, Library, Downloaded.** Android Auto shows four; Continue folds into Home's first section, Shelves into Library.
  Up next stays a tab (it is the one list a driver sets up on purpose). Downloaded stays (it is the no-network place).
- **One request for Home** (`GET /api/v1/home`): five sequential calls would not fit 5 s on a phone link; one server call reuses the
  web Home's functions.
- **"Start a book not started" = two sections plus a Library row**: "Your series in progress" (the next *unstarted* book of a series
  the person is in), "New on the shelf" (newest added, `ShowNotStarted`, audio), and Library > Not started (A to Z, 120). The web's
  "New on the shelf" keeps `ShowNotFinished`; the car uses `ShowNotStarted` so it never repeats Continue. Same words, narrower list.
- **"Shared with you" is left out**: those books already appear in New on the shelf (the scope includes shared books).
- **Android Home is a grid with group titles** (`EXTRAS_KEY_CONTENT_STYLE_GROUP_TITLE` per item, `GRID_ITEM` playable style):
  covers read at a glance; Continue items carry `EXTRAS_KEY_COMPLETION_STATUS` 1 and `EXTRAS_KEY_COMPLETION_PERCENTAGE` (a bar).
- **CarPlay Home is one `CPListTemplate` with a `CPListSection` per section** (header = section title), Continue rows with
  `playbackProgress`. `CPGridTemplate` holds at most 8 buttons with symbols, no covers: wrong tool.
- **Fitting**: a shared `fitSections` gives each non-empty section up to 2 rows first, then fills in order, so a car that allows
  12 rows still shows New on the shelf. CarPlay passes `CPListTemplate.maximumItemCount`/`maximumSectionCount`; Android passes 26 / 6.
- **Library**: Continue listening (the old Continue list, 24), Not started (A to Z), Recently added, By title, By author, then
  Shelves (only when the person has one), Podcasts, Radio (only when on, as today). Book lists are one request of 120.
  Letter buckets for bigger libraries need a title-prefix filter on `ListBooks`: later (out of scope). Android Auto search covers the rest.
- **Old server (404 on /home)**: Home shows the Continue listening section from `/continue` alone. No version probing.
- **Refresh**: after a new item starts playing, Android calls `notifyChildrenChanged("home", ...)`; CarPlay refills Home when its tab
  is selected (it already refills every selected tab).

## Endpoint: GET /api/v1/home (CH1, server)
Signed-in person (cookie or device token, like every `/api/v1/` read). No query parameters (unknown ones ignored). `Cache-Control: no-store`.
```
{"continue":[{"book":BookCard,"positionMs","durationMs","updatedAt"}],      // ContinueListening(6), same item as /continue
 "series":[{"book":BookCard,"seriesName":string,"index":int,"total":int}],   // SeriesInProgress(8), Started rows skipped, cap 4
 "new":[BookCard],                                                           // ListBooks added/not_started/audio, limit 12, cap 8
 "because":{"seed":string,"books":[BookCard]}|null,                          // BecauseYouListened(8), cap 4; null when none left
 "episodes":[PodEpisode]}                                                    // NewEpisodes(4) as /podcasts/new items; [] when off
```
Arrays are `[]`, never `null`. Dedupe across sections in the order continue, series, new, because (a `seen` set of book ids).
Errors: 401 unauthenticated (existing middleware); 500 through `s.serverError`. Podcasts off or `share.UsePodcasts` refused (guests)
gives `"episodes":[]`, never a 404 for Home.

---

## CH1: shared module, server and Android Auto (one builder package)

### Steps
1. `internal/web/routes_api_home.go` (new): constants `carHomeContinue = 6`, `carHomeSeries = 4`, `carHomeNew = 8`,
   `carHomeBecause = 4`, `carHomeEpisodes = 4`; types `apiHomeSeries{Book apiBookCard "book"; SeriesName "seriesName"; Index "index";
   Total "total"}`, `apiHomeBecause{Seed "seed"; Books []apiBookCard "books"}`, `apiHome{Continue []apiContinueItem; Series
   []apiHomeSeries; New []apiBookCard; Because *apiHomeBecause; Episodes []apiPodEpisode}` (json names as above).
   `func (s *server) getAPIHome(w, r)`: `u := currentUser(r)`, `sc := s.scope(r)`; helper `keep(b library.BookCard) bool` = not
   `seen[b.ID]`, `b.Kind != library.BookText`, and `s.can.Can(ctx, u, share.ListenBook, share.Target{Kind: share.TargetBook, ID: b.ID,
   LibraryID: b.LibraryID})`; a kept book is added to `seen`. Then, in this order: ContinueListening(ctx, sc, u.ID, carHomeContinue);
   SeriesInProgress(ctx, sc, u.ID, carHomeSeries*2) skipping `sn.Started`, `Index: sn.NextIndex`, `Total: sn.Total`, `SeriesName:
   sn.Series.Name`, stop at 4; ListBooks(ctx, sc, library.ListQuery{Sort: library.SortAdded, Show: library.ShowNotStarted, Format:
   library.BookAudio, UserID: u.ID, Limit: carHomeNew + carHomeSeries}) stop at 8; BecauseYouListened(ctx, sc, u.ID, carHomeBecause*2)
   stop at 4, `Because` set only when at least one book is kept; episodes only when `s.podcastsOn(r) && s.can.Can(ctx, u,
   share.UsePodcasts, podServer)`: `s.podcasts.NewEpisodes(ctx, u.ID, carHomeEpisodes, 0)` mapped with `apiPodEpisodeOf`.
   Any error: `s.serverError(w, r, "api home", err)`. Write with `w.Header().Set("Cache-Control", "no-store")` and `api.Write(w, 200, v)`.
2. `internal/web/routes_api_settings.go`: register `mux.HandleFunc("GET /api/v1/home", s.getAPIHome)` beside `/api/v1/continue` (line 34).
3. `internal/web/routes_matrix_api_test.go`: add the row `api(sweepGet("GET /api/v1/home", sweepStatic("/api/v1/home"), ok))` beside the
   `/api/v1/continue` row (line 105).
4. `internal/web/api_home_test.go` (new): the Go tests below.
5. `docs/api.md`: a `### GET /api/v1/home` section under "App settings, per-book speed and Continue" with the shape above; add
   `format` (`audio`, `text`) to the `GET /api/v1/books` query list (it already works; undocumented).
6. `android/shared/.../LibraryTree.kt`: new nodes `Home` ("home"), `Library` ("library"), `LibraryNotStarted` ("library-not-started"),
   `LibraryRecent` ("library-recent"), `LibraryTitle` ("library-title"), `LibraryAuthor` ("library-author") in `Node`, `parseNode`, `nodeId`.
7. `android/shared/.../ApiPaths.kt`: `val homeUrl = "$base/api/v1/home"`; `fun booksUrl(sort: String, show: String?, limit: Int)` =
   `$base/api/v1/books?sort=<sort>&format=audio&limit=<limit>` plus `&show=<show>` when not null (`sort` one of `added`, `title`, `author`).
8. `android/shared/.../CarHome.kt` (new):
   - `enum class HomeKind { CONTINUE, SERIES, NEW, BECAUSE, EPISODES, DOWNLOADED }`; `const val HOME_MAX_ROWS = 26`,
     `HOME_MAX_SECTIONS = 6`, `LIBRARY_LIST_MAX = 120`; `fun homeCap(k: HomeKind): Int` = 6, 4, 8, 4, 4, 12.
   - `data class SeriesNote(val name: String, val index: Int, val total: Int)`;
     `data class HomeBook(val card: BookCard, val positionMs: Long = 0, val series: SeriesNote? = null)`;
     `sealed interface HomeRow { data class Book(val book: HomeBook) : HomeRow; data class Episode(val card: EpisodeCard) : HomeRow }`;
     `data class HomeSection(val kind: HomeKind, val seed: String, val rows: List<HomeRow>)`; `data class CarHome(val sections: List<HomeSection>)`.
   - `object CarHomeJson { fun home(body: String): CarHome }`: reads the shape above with `ApiJson.bookCard`; episodes through a
     per-object `PodcastJson.episodeCardOf(o: JsonObject): EpisodeCard?` extracted from `episodeCards` in `Podcast.kt` (same rules: bad
     id or `gone` gives null; `episodeCards` then calls it). Each section cut to `homeCap`; empty sections dropped; missing keys = empty.
   - `fun carHomeOffline(mine: List<OfflineBook>, hasCover: (String) -> Boolean): CarHome`: CONTINUE = `carContinueOffline` take 6;
     DOWNLOADED = `carDownloadedBooks` minus the CONTINUE ids, take 12; empty sections dropped.
   - `fun fitSections(sizes: List<Int>, max: Int): List<Int>`: pass 1 gives each section `min(size, 2)` in order while budget lasts;
     pass 2 extends each in order up to its size. Negative max = 0.
   - `fun fitHome(h: CarHome, maxItems: Int, maxSections: Int): CarHome`: drops empty sections, keeps the first `maxSections`, cuts rows by `fitSections`.
   - `fun homeProgress(b: HomeBook): Double?`: null when `positionMs <= 0` or `card.durationMs <= 0`, else `positionMs / durationMs` clamped to 0..1.
   - `fun homeTitleKey(k: HomeKind): String`: `car_home_continue`, `car_home_series`, `car_home_new`, `car_home_because`, `car_home_episodes`, `car_downloaded`.
9. `android/shared/.../PlayerParse.kt`: `fun parseCarHome(body: String): CarHome? = safely { CarHomeJson.home(body) }` and
   `fun parseListBooks(body: String): List<BookCard>? = safely { ApiJson.listBooks(body) }`; `ApiJson.kt`: `fun listBooks(body)` =
   `{"items":[BookCard],...}` (the `api.NewList` shape of `/books`).
10. `android/app/.../Api.kt`: `fun home(token): CarHome = CarHomeJson.home(String(get(paths.homeUrl, token, 1024 * 1024)))`;
    `fun books(token, sort: String, show: String?): List<BookCard> = ApiJson.listBooks(String(get(paths.booksUrl(sort, show, LIBRARY_LIST_MAX), token, 2 * 1024 * 1024)))`.
11. `android/app/src/main/res/values/strings.xml`: add `car_home` Home, `car_library` Library, `car_home_continue` Continue listening,
    `car_home_series` Your series in progress, `car_home_new` New on the shelf, `car_home_because` Because you listened to %1$s,
    `car_home_episodes` New episodes, `car_series_line` Book %1$s of %2$s · %3$s, `car_not_started` Not started, `car_recent`
    Recently added, `car_by_title` By title, `car_by_author` By author. Keep `car_continue` and `car_shelves` (rows inside Library use
    `car_home_continue` and `car_shelves`). Delete `car_continue` only if nothing else uses it (grep).
12. `android/app/.../LibraryTree.kt`:
    - `tabs()`: Home (`playableStyle = GRID`), Up next (LIST), Library (`browsableStyle = LIST, playableStyle = LIST`), Downloaded (GRID).
    - `bookItem(c, group: String? = null, detail: String? = null, percent: Double? = null)`; `episodeItem(c, group: String? = null)`:
      a non-null group puts `MediaConstants.EXTRAS_KEY_CONTENT_STYLE_GROUP_TITLE`; `detail` sets the subtitle; `percent` puts
      COMPLETION_STATUS 1 and COMPLETION_PERCENTAGE. Old callers unchanged.
    - `Node.Home`: compute `offline = carHomeOffline(mine()) {...}` first; then `online({ offline }) { api, t -> api.home(t) }`; an
      `ApiException` 404 gives `CarHome(listOf(HomeSection(CONTINUE, "", api.continueList(t).take(6).map { HomeRow.Book(HomeBook(it)) })))`;
      then `fitHome(h, HOME_MAX_ROWS, HOME_MAX_SECTIONS)` mapped by `homeItems(h)`: group = `sectionTitle(kind, seed)` (R.string by
      `when`; BECAUSE formats the seed), book detail = `car_series_line` for a series row, else the author; Continue rows get `homeProgress`.
      Store the answered size in `homeCount` (`@Volatile var`).
    - `guarded(work, late: (() -> LibraryResult<T>)? = null)`: the late runnable answers `late()` when given. `children()` passes,
      for Node.Home, a `late` that answers the stored offline items (an `AtomicReference<List<MediaItem>?>` set before the network
      call) or the existing no-connection error when null.
    - `Node.Library`: `libraryRows()` = folders Continue (Node.Continue, `car_home_continue`, LIST), Not started, Recently added,
      By title, By author (all `playableStyle = LIST`), then `Node.Shelves` (`car_shelves`) only when `api.shelves(t)` is non-empty
      (a 404 or error leaves the row out; 401 still signs out), then `podcastsRow()`, `radioRow()`.
    - `Node.Shelves` now lists only the shelves (Podcasts and Radio rows move to Library). `LibraryNotStarted` = `api.books(t, "title",
      "not_started")`, `LibraryRecent` = `("added", null)`, `LibraryTitle` = `("title", null)`, `LibraryAuthor` = `("author", null)`.
    - `itemOf`: tabs()[0..3] = Home, UpNext, Library, Downloaded; Continue, Shelves and the four list nodes from `libraryFolders()`.
13. `android/app/.../PlaybackService.kt`: in the `player.addListener` block (line 334) add `onMediaItemTransition` that calls
    `session?.notifyChildrenChanged(nodeId(Node.Home), tree.homeCount, null)`.
14. `docs/android.md`: "Podcasts in the car" says Podcasts and Radio are rows of the Library tab; add "## Home in the car (CH1)" (10 lines max).
15. `docs/manual-checks.md`: rows A8-13 to A8-17 (below); `docs/roadmap.md` status note; `CHANGELOG.md` line (below).

### Tests (CH1)
- Go `internal/web/api_home_test.go` (seed `syncSeed(t)`, which embeds `grantsLeakEnv`):
  `TestAPIHomeSections`: owner PUTs progress on `extra[0]`; `continue` holds it, `new` holds `extra[1]` and not `extra[0]`; no id twice
  across all sections; `Cache-Control: no-store`; a person with nothing gets the body with `"series":[]`, `"new"` present, `"because":null`.
  `TestAPIHomeFinishedAndText`: a finished book (`UPDATE progress SET finished_at`) is in no section; a book set to `kind='text'` by
  `UPDATE books` is in no section.
  `TestAPIHomeForbidden`: insert a progress row for the member on `e.hidden` (same SQL shape as existing progress tests); the member's
  Home has no `e.hidden.ID` in any section and the body does not contain `e.hidden.Title` or the hidden series name; no credentials: 401;
  the guest: 200 with `"episodes":[]`.
  `TestAPIHomeEpisodes`: podcasts off: 200 and `"episodes":[]`; on with a followed show (reuse the podcast API test helper): items listed.
  Matrix row from step 3.
- Shared `android/shared/src/commonTest/.../CarHomeTest.kt`: full JSON parses into five sections in order with caps; `because:null` and
  missing keys give no section; a `gone` episode and a bad id are dropped; garbage gives `parseCarHome` null; `carHomeOffline`
  (other user's row out, STALE in, episodes out, in-progress first, no id twice, caps 6 and 12); `fitSections` table: `[6,4,8,4,4]`/26
  -> same, /12 -> `[4,2,2,2,2]`, /5 -> `[2,2,1,0,0]`, /0 -> zeros, `[]` -> `[]`, `[0,3]`/2 -> `[0,2]`; `fitHome` drops empties and keeps
  `maxSections`; `homeProgress` (0, over-length, zero duration); `homeTitleKey` for every kind. `LibraryTreeTest.kt`: round-trip of the six new ids.
- Android app: there are no JVM unit tests in `android/app` (Robolectric is not approved); its logic is the shared functions above.
  Gates: `android/tools/check-android.sh` (`:shared:jvmTest`, `lintRelease`) and `make apk-debug`.
- Go gates: `gofmt`, `go vet ./internal/web/`, `go test -race ./internal/web/ ./internal/library/`; `make build`.

### Manual car checks (operator, Desktop Head Unit or a car; `docs/manual-checks.md`)
- A8-13 Open Tendshelf: tabs read Home, Up next, Library, Downloaded; Home shows covers grouped under Continue listening, Your series in
  progress, New on the shelf, Because you listened to ..., New episodes (those that have rows); Continue covers show a progress bar.
- A8-14 Tap a New on the shelf book: it starts at 0:00; back on Home it now leads Continue listening.
- A8-15 Phone in airplane mode, open Home: Continue listening and Downloaded from downloads within 5 s, no error.
- A8-16 Library: Continue listening, Not started, Recently added, By title, By author, Shelves, Podcasts, Radio open and play.
- A8-17 Voice "play <title>" and search still work.

---

## CH2: CarPlay (one builder package, after CH1 is merged and `make ios-shared` has rebuilt the framework)

### Steps
1. `ios/Tendshelf/PlayerApi.swift`: `CarAPI` gains `func home(token: String) async throws -> CarHome` (GET `paths.homeUrl`,
   1 MiB, `PlayerParseKt.parseCarHome`, nil -> `ApiError(status: 0)`) and `func books(token: String, sort: String, show: String?)
   async throws -> [BookCard]` (`paths.booksUrl(sort:show:limit: 120)`, 2 MiB, `parseListBooks`). Implement in `extension PlayerApi: CarAPI`.
2. `ios/Tendshelf/CarLibrary.swift`:
   - `CarRow` gains `case homeBook(HomeBook)`; `CarList` gains `case sections([CarSection])` with `struct CarSection: Equatable
     { let title: String; let rows: [CarRow] }`.
   - `CarWords.keys` add `car_home`, `car_library`, `car_home_continue`, `car_home_series`, `car_home_new`, `car_home_because`,
     `car_home_episodes`, `car_series_line`, `car_not_started`, `car_recent`, `car_by_title`, `car_by_author`.
   - `NodeHome`: `offlineHome()` = `CarHomeKt.carHomeOffline(mine:...)` mapped to sections; online: `api.home`; `ApiError` 404 ->
     Continue section from `continueList` (take 6); `URLError` or status 0 -> offline sections; title per section =
     `words(CarHomeKt.homeTitleKey(k:))`, BECAUSE with the seed as argument; book rows `.homeBook`, episode rows `.episode`. Empty -> `car_nothing_here`.
   - `load(_:)`: for `NodeHome` the timer answers `.sections(offlineHome())` when that is non-empty, else the no-connection words.
   - `NodeLibrary` rows as CH1 step 12 (Shelves only when non-empty, then Podcasts, Radio via `answers`); `NodeShelves` lists shelves only;
     the four list nodes call `api.books`. `picture(for:)` treats `.homeBook(h)` as `.book(h.card)`.
3. `ios/Tendshelf/CarPlay.swift`:
   - `tabs` = `("home","car_home","house")`, `("queue","car_up_next","list.bullet")`, `("library","car_library","books.vertical")`,
     `("downloaded","car_downloaded","arrow.down.circle")`.
   - `CarItemModel` gains `var progress: Double? = nil`; `items` maps `.homeBook`: text = title, detail = `words("car_series_line",
     "\(index)", "\(total)", name)` for a series row else the author, progress = `CarHomeKt.homeProgress(b:)`.
   - `static func sectionModels(_ sections: [CarSection], maxItems: Int, maxSections: Int) -> [(String, [CarItemModel])]` using
     `CarHomeKt.fitSections`; `show` handles `.sections` with `CPListSection(items:header:sectionIndexTitle: nil)`, passing
     `CPListTemplate.maximumItemCount` and `maximumSectionCount`; `listItem` sets `item.playbackProgress = CGFloat(p)` when present.
   - Pictures: the first `imageRows` rows across all sections (flattened order).
4. `ios/TendshelfTests/CarLibraryTests.swift`, `CarPlayItemsTests.swift`: the fake `CarAPI` implements `home` and `books`; tests below.
5. `docs/ios.md` "CarPlay (I4c)": the new tab set and Home; `docs/manual-checks.md` IOS-22 to IOS-25; roadmap status.

### Tests (CH2, XCTest via `make ios-sim`)
- `CarLibraryTests`: Home online gives sections in invariant-4 order with the right titles (seed in the Because title); Home with
  `URLError` gives Continue listening + Downloaded from stored rows of this user only; Home 404 gives only Continue listening; Home
  slower than a 0.2 s test budget answers the offline sections, not words; signed out gives `car_sign_in`; Library with podcasts off
  and no shelves lists exactly the five book rows; Shelves has no Podcasts row; the four list nodes call `books` with
  (`title`,`not_started`), (`added`,nil), (`title`,nil), (`author`,nil); every new `CarWords` key exists.
- `CarPlayItemsTests`: tabs are `home`, `queue`, `library`, `downloaded`; a series `homeBook` detail reads "Book 2 of 5 · <name>",
  a plain one the author; progress 0.5 for 30 of 60 min and nil at 0; `sectionModels` with maxItems 12 over sizes 6,4,8,4,4 gives 4,2,2,2,2
  and with maxSections 2 keeps the first two.

### Manual car checks (CarPlay Simulator window: I/O > External Displays > CarPlay)
- IOS-22 Tabs read Home, Up next, Library, Downloaded; Home sections have headers, covers and a progress bar on Continue rows.
- IOS-23 Tap a New on the shelf book: Now Playing opens; reselect Home: the book leads Continue listening.
- IOS-24 Network Link Conditioner "100% Loss": Home shows Continue listening and Downloaded within 5 s.
- IOS-25 Library rows open and play; a shelf, a podcast episode and a station still play.

## CHANGELOG (`## Unreleased`, `### Added`)
- **A Home page in the car.** Android Auto and CarPlay now open on Home: what you are listening to, the next book in your series, books
  you have not started yet, suggestions and new episodes, with covers. A new Library tab lists books by title, by author, recently
  added and not started, beside your shelves, podcasts and radio. With no connection, Home shows your downloads.

## Out of scope (and why)
- A to Z letter folders for libraries over 120 books: needs a title-prefix filter in `ListBooks`; Android Auto search covers it for now.
- Authors as folders: no authors list endpoint exists; By author lists books sorted by author.
- CarPlay `CPListImageRowItem` cover strips: a later polish once the sections are seen in a car.
- Text books and read aloud in the car; Siri and search on CarPlay (audio apps get neither); download actions in the car.
- Live updates of Home while it is on screen beyond the one `notifyChildrenChanged` on a new item.
