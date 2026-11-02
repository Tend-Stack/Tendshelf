# Screens: boards against built pages

Which design board (`tendshelf-handoff/screens/`) each built page answers to, which test captures it,
and what differs on purpose. Recorded at the P1b exit audit (2026-11-01, main after P1b-9b). No board is
compared pixel by pixel: the visual tests probe every capture (overflow, 44 px targets, labels, contrast
rules) and copy the board PNG beside the screenshot for a person to compare.

Matrices: a **main** page runs dark 390, dark 1440, light 768, sepia 1024 and contrast 390 in `make visual`
and all 4 themes × 4 widths in `make visual-full`; a **secondary** page runs dark 390 and light 1440, and
dark and light × 4 widths in the full run. The nightly CI job runs `make visual-full`.

| Board(s) | Width | Capturing test | Matrix | Declared differences |
| --- | --- | --- | --- | --- |
| HomePhone, HomeTablet, HomeDesktop (+ Light) | 390, 1024, 1440 | `TestVisualBrowsePages` `home`; `TestHomeVisualListening` (hero, series, because) | main | No Shared with you row (P3); no "All series" link; the hero has no load trigger; series sits after New on the shelf on every width (the desktop board puts it beside the hero) |
| BookPhone, BookDesktop (+ Light) | 390, 1440 | `TestVisualBookPages` `book-about`; `TestHomeVisualBookPlay` (play region, chapter links) | main | No download or share (P4, P3); no two-column player (P1c-8); no Start series link or percentages; Add to shelf added; desktop uses two columns instead of the board's full-width tab row |
| BookTablet (+ Light) | 1024 | `book-details` | secondary | As BookDesktop |
| BookmarksPhone | 390 (also 768, 1024, 1440) | `TestBookmarkVisual` `bookmarks-tab` | main | It is the book page's Bookmarks tab, so the board's small cover header is not repeated (the book header shows the cover); no Highlights chip and no quotes until R4; "Play from here" is in the markup but hidden until the player lands (P1c-5); the active chip carries a check as well as colour |
| BookCover | 120 | kitchen sink `ks-cover`, every cover page | — | None |
| LibraryPhone, LibraryTablet, LibraryPage | 390, 1024, 1440 | `TestVisualBrowsePages` `library-grid`, `library-filtered`; `TestVisualLibraryFilters` | main | Unfinished and Downloaded filters wait for P1c and P4 |
| LibraryListPhone, LibraryFiltersPhone, LibraryEmptyPhone, LibraryScanningPhone | 390 | `library-list`, `library-filters` (+ `TestVisualLibraryFilterSheet`), `library-empty`, `library-scanning` | secondary | None |
| SearchPhone, SearchTablet, SearchPage | 390, 1024, 1440 | `TestVisualBookPages` `book-search`; `TestVisualBookTabsAndSearchLive` | main | Totals cap at 1,000; series results without stacked covers |
| SeriesPhone, SeriesTablet, SeriesPage | 390, 1024, 1440 | `book-series` | main | No Start series link, percentages or stacked covers (P1c) |
| AuthorTablet, AuthorPage | 1024, 1440 | `book-author` | main | No sort on person pages |
| NarratorPhone | 390 | `book-narrator` | secondary | No sort on person pages |
| Onboarding | 390 | `TestVisualAdminPages` `setup-server` | secondary | Account step comes first (decision 0002) |
| OnboardFolder | 390 | `setup-folder`; `TestVisualFolderBrowser` | secondary | Browse panel added |
| OnboardAccount | 390 | `TestVisualAuthPages` `setup` | main | Setup code, username and password fields; no passkey buttons (passkeys later) |
| OnboardDone | 390 | `TestVisualBrowsePages` `onboard-done`; `TestVisualAdminPages` `setup-done` | secondary | Zero counts left out of the summary line |
| SettingsPhone | 390 | `TestVisualMePages` `settings` | main | Text size and Larger text mode go with P3; several rows wait for their features |
| ProfilePhone | 390 | `me-profile` | secondary | No stats, bookmarks or finished rows; no Users, Metadata or Research entries |
| The mini-player (component README MiniPlayer; on LibraryListPhone, SearchTablet, BookDesktop) | 390, 768, 1024, 1440 | `TestPlayerBrowserLayout` | own run (`make player`, dark and light) | Opens the full player (the link goes to the book page without script); desktop bar adds a compact scrubber; a long press (500 ms) opens the player with the speed sheet (P1c-5b); from 1280 px the bar adds Speed, Sleep and a volume slider, which the board does not show; the Back button carries its seconds as a number on the icon; Forward shows from 1280 px; hidden while a play control for the loaded book is on screen |
| PlayerPhone, PlayerPhoneLight, PlayerTablet, PlayerTabletLight, PlayerDesktop, PlayerDesktopLight | 390, 768, 1024, 1440 | `TestFullPlayerBrowserLayout` (`full-<theme>-<width>.png`) | own run (`make player`, dark and light; four themes with `make visual-full`) | Below 1280 px the player is a full-screen dialog; tablet uses the phone layout centred (the board's left-aligned clock and 8-column grid are not built). From 1280 px a 400-wide side panel stands in on any page, not the two-column view on the book's own page (P1c-8 decides). No output-device item (no portable API), no "Playing on this computer" line, no split-view icon; the desktop settings bar shows Chapters beside Speed, Sleep and Bookmark. Speed, Sleep and Bookmark work since P1c-5b. The bar stays visible beside the panel |
| StatePlayerHints | 390 (also 768, 1024, 1440) | `TestFullPlayerBrowserHints` (`hints-<theme>-<width>.png`) | own run (`make player`, dark and light; four themes with `make visual-full`) | The rewind hint and the sleep fade card with "+10 min" (the board's sleep fade card; the hint sits in the player's own toast region while the player is modal). The Sleep sheet's Custom choice has a Start timer button, which the board's "no OK button" sheet rule does not have: a typed number needs a commit. The card sits above the settings bar, not over the transport row. The hint text names shaking only on touch devices |
| LibrariesPhone, LibrariesTablet, LibrariesPage, LibProblems | 390, 1024, 1440, 420 | `TestVisualAdminPages` `libraries` | main | No folder removal, watching or scan history |
| StateLoading | 390 | `TestVisualLibraryInfiniteScroll`; kitchen sink `ks-skeleton` | 390 only | None |
| StateNoResults | 390 | `book-search-none`, `library-nomatch` | secondary | None |
| StateError | 390 | none (handler tests only) | — | "Couldn't reach your server" needs the service worker (P4) |

Not built yet, by owning phase: ComparePhone (P2);
Invite*, RosaHome, Share*, Users* (P3); DownloadsPhone, StateOffline (P4); Research* (P6); reader boards (R).
Sepia and contrast are captured only for main pages. The WebKit run (`make visual-webkit`) covers eight
real pages in dark and light at 447 and 1440 px.
