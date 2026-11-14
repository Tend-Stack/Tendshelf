# 0042 The full player looks like a native player

Date: 2026-11-14. Status: accepted by the operator (2026-11-14).

## Context

From phone screenshots the operator found two things in the full-screen player. The chapter bar
and the waveform under it were the same orange, so two progress bars read as one. And the bottom
row (Speed, Sleep, Chapters, Read) was a framed toolbar with dividers, "a web page bar menu, not a
native mobile player"; its Read label ("Read this book") was cut off at 390 px.

## Decision

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
   colour alone. The Read label is the short "Read"; the full "Read this book" stays as its
   accessible name. The row clears the system gesture bar with `env(safe-area-inset-bottom)`. Large
   text keeps the two-column grid. The row is used only by the full player; from 1280 px the side
   panel hides it and the desktop bar carries the tools, as before.

## What stays from the handoff

`design-system/components/PlayerControls/README.md`: the order of the controls, the tool names, the
transport, the scrubber and its chapter ticks, the sleep and speed sheets.

## Consequences

The `.ts-player-bar` rules in `bundle.css` were rewritten (the class has one user). Handoff files are
not edited. Tests: `TestPlayerNativeRow`, `TestPlayerRulerColour`, `TestPlayerRowContrast`.
