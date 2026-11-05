# 0021 Profile puts the person's books first
Status: proposed (2026-11-05). Plan: `docs/plans/profile-redesign.md`. Board: `docs/design/boards/Profile.html`.
Amends decision 0010 point 2 (Profile leaves the preferences window) and, under decision 0002, replaces the handoff's
`ProfilePhone` board as the target for `/me`.

## Operator's words (2026-11-05)
"the books the user is currently listening for, are not easily accessible, the only way to see them is to go to
Library and then having to filter by Unfinished. There should be an easier way. The user's profile is a good place for
this /me. Also, the profile needs serious redesign to accommodate things better and to look more like a books
listening reader app."

## Context
- `/me` today, on a wide screen: the Settings list on the left, then the person, a stats card, a small card of links
  and one row per browser session. No cover anywhere. Nine or more nearly identical sessions fill the page.
- What exists and is kept:
  - `library.ContinueBooks` (both formats, one row per book, newest first, scope-filtered, up to 24). Home asks for 1.
  - `library.FinishedBetween`, `library.Shelves`, `library.ShowUnfinished` and `ShowFinished`.
  - The hero (`homeHeroView`, `textHeroView`), `BookCard`, `Cover`, `withCardProgress`, `PlayButton`, `ReadButton`,
    `ListeningStats`, `deviceRows`, the shelf row, and the player's resume watcher. That watcher hides the mini-player
    while any `[data-ts-play]` of the loaded book is on screen, so the handoff's one-resume rule holds with no new
    JavaScript.

## Decision
1. **Order of the page.** Head; the book in progress first, as the hero; "Also in progress"; "Finished lately"; Your
   listening; Your shelves; Account and Your server; Sign out. Books come first on every width.
2. **Profile leaves the preferences window** (amends 0010 point 2). `/me` is a page of its own at full width, reached
   from the main navigation. Decision 0010's reason for the frame is Settings; a page led by covers does not want a
   list of categories beside it.
   - **Settings stays reachable:** one "Settings" link in the Profile head, at every width. Settings keeps its window
     and its person card, which still links to `/me`.
   - **Phone and wide screens now match:** both open Profile, then Settings. `/me/settings` is unchanged.
3. **The first book is the hero**, the same card as Home, with the same words and the same one Resume button.
   `HeroCard` is extracted from `HomeHero`. It gains the page edges (`ts-book-block`) the boards always showed, on Home
   too.
4. **"Also in progress" lists up to eight more books**, one `ts-pbook` each: `BookCard` plus one round control.
   - **Phone:** rows, five shown.
   - **From 36 rem of page width:** tiles, the control on the cover's corner.
   - **The line under the byline:** "4 h 2 m left" (`bookWhereShortBook`, at the person's speed) or "Chapter 9 · 37 %"
     (`readWhere`). The time left never truncates.
   - **See all** opens `/libraries?show=unfinished`, shown when more than six books are under way.
5. **One control per book, the format used last** (decision 0018). Listened last, the play button. Read last, the
   read button. If the person has lost the right to that format, the other format's button, resuming from its own real
   place. With neither right, no control and the card still links to the book. The other format's secondary button
   never appears on this page; it lives on the book page. `lastFormat` is not used here: it can pick a finished format,
   and `ContinueBooks` already says which unfinished place is newest.
6. **"Finished lately"**: up to six covers, three on a phone, from `FinishedBetween`; See all opens
   `/libraries?show=finished`. BRAND: celebrate with the covers.
7. **Your listening is unchanged and stays private** ("Only you see these."). Its three links, including Your word
   list, gain a wrapper so they stack.
8. **Your shelves** shows the first three shelves with their cover stacks, using the Shelves page's own row.
9. **Devices move to `GET /me/devices`**, a new item "Your devices" in the Settings list.
   - **Profile keeps one line:** "This device and 8 others".
   - **Nearly identical sign-ins fold:** browser sessions with the same label show once ("6 sign-ins · 2 hours ago") in a
     `<details>`, and Sign out still works per session inside it.
   - **Never folded:** this device, and apps (they have names of their own).
   - **After signing out:** the three sign-out posts now redirect to `/me/devices`. The admin's Devices tab does not
     change.
10. **Home gets a small row under the hero**: "Also in progress", three covers with their progress line and time left,
    no buttons, See all to `/me`. It lives inside `#home-hero`, so a save refreshes it with the hero and a book never
    shows in both. Three fixed covers, not a scrolling row, because that fragment is swapped on every save and a
    scrolled row would jump back.
11. **Layout measures the page, not the screen** (decision 0010 point 4): a container query on `.ts-profile`, so the open
    player panel gives one column. Two columns from 60 rem (1280 px and up, panel closed). Between 36 and 60 rem,
    Your listening and Your shelves pair up. All four themes through tokens.
12. **New stylesheet `css/pages-profile.css`** (3 KB default budget). A new page stylesheet costs every page one
    prefetch, so `pages-email.css` joins `noPrefetch` in the same commit and no page gains a request.
13. **Copy.** Listed in the plan. Reused words stay exact.

## Left out, and why
- **Live refresh of the list while playing.** A save every 10 s would move books under the person's hand and lose
  focus. The list is fresh on every visit. The buttons already follow the player.
- **Finished ebooks in "Finished lately".** `FinishedBetween` reads `progress`, audio only. Reading is not lost: Your
  reading has its own page. A later change can merge them.
- **Closing old sessions automatically, and a "sign out these six" button.** Folding answers the complaint; "Sign out
  everywhere else" already covers the sweep.
- **A Downloads row.** It is a main navigation item at every width.
- **Bookmarks and notes, and a Finished count.** The handoff's rows; no page gathers bookmarks across books.

## Consequences
- `ProfilePhone` stays in the handoff as exported; `Profile.html` is the target for `/me`, and `docs/design/screens.md`
  says so. Profile and Your devices need fresh captures; Home's hero changes by its page edges.
- New: route `GET /me/devices`, `pages-profile.css`, a `profile` row in the page-weight budgets. No migration, no new
  module, no JavaScript, no new query.
- Home's image budget has to rise for the three covers. That needs the operator's word (the budget rule in
  `perf_weight_test.go`).
