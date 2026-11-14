# 0043 One player, three kinds

Date: 2026-11-14. Status: accepted by the operator (2026-11-14). Plan: [player-kinds](../plans/player-kinds.md).

## Context

The full-screen player was built for a book. A podcast episode and a radio station used the same dialog but
showed controls that did nothing for them (a chapter wave on a live stream, Mark as finished for a station, a
bookmark key that posted to a book route for an episode), and offered nothing that belonged to them (the show,
the show notes, the Radio page). An episode's speed change was not saved, and "Make it my usual speed" showed a
confirmation while saving nothing.

## Decision

One dialog (`#player-full`), one data fragment, one place that decides the kind: `drawKind()` in `player-full.js`.
The fragment addresses do not change. No new route, no new table.

- **K5 Attributes.** `drawKind()` sets `data-ts-kind="book|episode|radio"` and `data-ts-chapters="one|many"` on the dialog.
  The stylesheet keys on them. Per-item facts (has notes, has chapters, a share link) keep `hidden`. `is-live` stays as an alias.
- **K1 Layout on a phone.**

| Block | Book | Episode | Station |
|---|---|---|---|
| Header line | chapter words | "Episode" without chapters, else chapter words | "Live" |
| Cover | cover | show art | logo or initials |
| Title block | title / chapter · author | episode / chapter · show | station / now-playing line, one line |
| Wave | yes | yes | a static live line (3.5 rem, flat bars, no motion, hidden under forced colours) |
| Times row | yes | yes | absent |
| Book ruler and label | yes | only with more than one chapter | absent |
| Transport | 5 buttons | 5 with chapters, else 3 | play and pause, centred |
| Settings bar | Speed, Sleep, Chapters, Read along | Speed, Sleep, [Chapters], [Show notes] | Sleep, Radio |
| Menu | unchanged | adds Go to show | not drawn |

- **K2 Settings bar.** Each item is a real feature; an episode's navigation to its show sits in the menu.
- **K3 Menu.** A station's hidden items carry safe addresses (`/radio`), never a book or episode address.
- **K4 Sleep is shared.** Minutes and Custom for every kind, End of chapter for books, End of episode for episodes, neither for stations.
  Night sleep also starts for stations: the radio fragment carries the person's night settings.
- **K6 Show notes.** They open as a sheet inside the player, filled on open from the existing `/hx/player/episode-panel/{id}` fragment
  (same `loadEpisode` gate). Only the `.ts-podnotes` node is imported, so no id repeats. A failed fetch shows one plain line and the next
  open asks again. The `.ts-podnotes` rules moved from `pages-podcasts.css` to `now-panel.css`, which every page with a player loads.
  At 1280 px and up the bar is hidden and the Now playing panel carries the notes.
- **Episode speed.** A speed chosen in the player is saved to the show (`POST {showHref}/settings`, field `speed` only). "Make it my usual
  speed" is hidden for episodes and stations.

## Addendum to decision 0038 (fragment shape)

The episode player fragment may carry `data-has-notes="1"`, only when the cleaned show notes are not empty. The radio player fragment may
carry the night-sleep attributes (`data-night-sleep`, `data-night-from`, `data-night-to`, `data-night-minutes`) when the person's setting is on.

## Out of scope

Saving a station from the player, favouriting an episode, a now-playing history, share and download in the player, an episode "end of chapter"
sleep choice, Eyes-free for stations, a new mini bar or tablet layout, `android/`, translations.
