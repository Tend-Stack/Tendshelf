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
| HomePhone, HomeTablet, HomeDesktop (+ Light) | 390, 1024, 1440 | `TestVisualBrowsePages` `home` | main | No Continue listening hero, series or "Because you listened" rows (P1c, P3) |
| BookPhone, BookDesktop (+ Light) | 390, 1440 | `TestVisualBookPages` `book-about` | main | No play, download or share (P1c, P4, P3); no Start series link or percentages; Add to shelf added; desktop uses two columns instead of the board's full-width tab row |
| BookTablet (+ Light) | 1024 | `book-details` | secondary | As BookDesktop |
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
| LibrariesPhone, LibrariesTablet, LibrariesPage, LibProblems | 390, 1024, 1440, 420 | `TestVisualAdminPages` `libraries` | main | No folder removal, watching or scan history |
| StateLoading | 390 | `TestVisualLibraryInfiniteScroll`; kitchen sink `ks-skeleton` | 390 only | None |
| StateNoResults | 390 | `book-search-none`, `library-nomatch` | secondary | None |
| StateError | 390 | none (handler tests only) | — | "Couldn't reach your server" needs the service worker (P4) |

Not built yet, by owning phase: Player*, BookmarksPhone, StatePlayerHints (P1c); ComparePhone (P2);
Invite*, RosaHome, Share*, Users* (P3); DownloadsPhone, StateOffline (P4); Research* (P6); reader boards (R).
Sepia and contrast are captured only for main pages. The WebKit run (`make visual-webkit`) covers eight
real pages in dark and light at 447 and 1440 px.
