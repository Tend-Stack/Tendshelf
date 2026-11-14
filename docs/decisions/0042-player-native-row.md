# 0042 The full player looks like a native player

Date: 2026-11-14. Status: accepted by the operator (2026-11-14).

## Context

From phone screenshots the operator found two things in the full-screen player. The chapter bar
and the waveform under it were the same orange, so two progress bars read as one. And the bottom
row (Speed, Sleep, Chapters, Read) was a framed toolbar with dividers, "a web page bar menu, not a
native mobile player"; its Read label ("Read this book") was cut off at 390 px.

## Decision

The same day the operator sent a design handoff for the phone player (two artboards at 390 by 844, plan
`docs/plans/player-phone.md`) and three amendments: the header and the cover stay as they are, and both bars
keep what they do and only move and change look. This decision never shipped in its first form, so it is
written here as one decision in three parts.

### Phones (width under 768 px and height over 30 rem; `css/player-phone.css`)

- Under the cover: title, then the waveform, then a time row (elapsed in `--ink`, "left in chapter" in
  `--accent`), then "left in book" right-aligned over the book bar, then the transport, then the settings bar.
  The gaps between these are equal and flexible; only the cover shrinks on a short screen.
- The waveform is the bar you scrub: 3 px lines with round caps at a 5 px pitch, 8 to 46 px tall, one ink
  needle with a ground outline. Played lines use the cover's accent (`--ts-mark`, falling back to
  `--wave-played`); unplayed lines use `--line-strong`, because `--wave-rest` measures under 3:1 in dark, light
  and sepia (2.84, 2.02, 2.03) and no token is added.
- While dragging: a scrim over the cover shows "Jump to", the target time and the time left; the needle grows
  and gains a halo; the lines between the old place and the finger show at half strength; the right of the
  time row says "was 11:15"; the book bar, transport and settings bar dim to 45 %. Keys seek at once with no
  scrim. The large clock is not shown at rest.
- The book bar is the same control as before (tap, drag, keys, tip, slider semantics), drawn as one segment
  per track: finished in the accent, the current one taller and in `--ink`, upcoming in `--line-strong`.
  Segment widths follow chapter length, not equal widths, so a tap lands on the chapter under the finger. Very
  narrow segments lose their gaps and a position mark appears.
- The settings bar is one raised bar (`--raised`, 4 rem, `--radius-lg`), no border, no dividers, equal columns
  for the items that exist (four for a book with a text edition, three without). A running sleep timer shows
  its time left as the label. The fourth label is "Read along" (`msgOpenReaderShort`, one constant); its
  accessible name stays "Read this book". Note: nothing is synchronised with the audio (read-along was dropped
  on 2026-11-04); the label is the handoff's wording.
- Measured against the player background (dark / light / sepia / contrast): unplayed 5.06 / 3.54 / 3.63 / 4.89;
  played 10.53 / 5.72 / 4.66 / 10.07; current segment 15.49 / 14.95 / 10.14 / 21.00; "left in chapter" text
  5.92 / 5.38 / 5.32 / 10.07.

### From 768 px to 1279 px, and phones on their side

Points 1 and 2 below, as first decided.

### From 1280 px

Unchanged: the side panel hides the row and the desktop bar carries the tools.


1. The chapter bar in the full player (`.ts-player__ruler`, also the side panel's book ruler) is
   neutral: the played part is `--ink`, the rest `--line-strong`. The waveform keeps the accent as
   the one bar you scrub. The shared `Ruler` on the book page, Home and the reader is unchanged.
   Measured against the player background: played at least 10:1 and the rest at least 3.5:1 in
   all four themes (`TestPlayerRowContrast`).
2. The bottom row has no frame, no dividers and no background: separate equal-width buttons, at
   least 44 by 44 px, a 1.5 rem icon (Speed and a running sleep timer put their value in its place)
   over a 13 px sentence-case label in `--ink-muted`. A press shows a tonal `--raised-hover` layer
   with `--radius-control` at once (hover the same on pointer devices). A speed other than 1.0x or
   a running timer shows its value in the accent; the value text itself changes, so it is never
   colour alone. The Read label is the short `msgOpenReaderShort` ("Read along"); the full "Read this
   book" stays as its accessible name. The row clears the system gesture bar with `env(safe-area-inset-bottom)`. Large
   text keeps the two-column grid. The row is used only by the full player; from 1280 px the side
   panel hides it and the desktop bar carries the tools, as before.

## What stays from the handoff

`design-system/components/PlayerControls/README.md`: the order of the controls, the tool names, the
transport, the scrubber and its chapter ticks, the sleep and speed sheets.

## Consequences

The `.ts-player-bar` rules in `bundle.css` were rewritten (the class has one user). Handoff files are
not edited. Tests: `TestPlayerNativeRow`, `TestPlayerRulerColour`, `TestPlayerRulerColourTablet`, `TestPlayerRowContrast`,
and for phones `TestPlayerPhoneBarsBehaveAsBefore`, `TestPlayerPhoneClock`, `TestPlayerPhoneWave`,
`TestPlayerPhoneBookRow`, `TestPlayerPhoneSettingsBar`, `TestPlayerPhoneSpacing`, `TestPlayerPhoneContrast`,
`TestPlayerPhoneThemesLargeText`, `TestPlayerPhoneTabletUnchanged`; captures with `TS_PHONE_SHOTS` set
(`TestPlayerPhoneCaptures`).
