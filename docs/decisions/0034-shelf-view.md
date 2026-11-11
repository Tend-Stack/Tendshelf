# 0034 Shelf view: a book is as thick as it is long
Status: proposed (lead, 2026-11-08; operator asked for the view and for true thickness). Plan: `docs/plans/shelf-view.md`.

## Decision
The Library gains a third view, **Shelf** (`?view=shelf`), beside Grid and List. Home's "New on the shelf" and the Library draw the
same `components.Spine` in the same `.ts-shelf`. A spine's thickness shows the book's real length, through one function, everywhere.

**The rule.** `length = duration`; a text-only book uses `words / 9,300 per hour` (155 words a minute, a usual narration pace), or
300 words a page if an edition has no word count. Then:

    t    = (sqrt(clamp(hours, 1, 40)) - 1) / (sqrt(40) - 1)
    step = 1 + round(11 * t)        // ts-spine--w1 ... ts-spine--w12; an unknown length is w4

Width in CSS is linear in the step: `2.75rem + (step - 1) * inc`, with `inc` 0.3 / 0.35 / 0.4 rem at phone / tablet / desktop.

## Why this rule
- **Square root**: linear makes ordinary books (6 to 15 h) look alike and lets one epic swallow a row. Square root keeps the
  range people own apart, and a tome still looks like a tome. War and Peace (61 h) and a 40 h book both clamp to step 12.
- **Twelve steps, classes only**: the CSP forbids `style=""`, and the server already picks classes. No JavaScript sets a width.
  At phone width a step is 4.8 px, small enough to read as continuous and large enough to see.
- **A floor of 44 px**: the slot is the target. The 6 px between two books belongs to the links (3 px inset each side), so a
  1 h book shows 38 px of spine in a 44 px target, with no dead space and no uneven gaps. 38 px also holds two lines of 13 px title.
- **Words, not pages**: `editions.word_count` is always set and needs no new column. `page_count` depends on the source (a real page
  list, or characters / 1,800), so it is only the fallback. Newest edition, one correlated subquery in the existing card query.

The honest ratio is 38 px to 91 px on a phone and 38 px to 108 px on a desktop: about 2.4 to 2.8 times, where the hours differ by 40.
Text and the target set the floor; a row of 350 px sets the ceiling.

## Height, and what this replaces
Height is not length. It takes one of five steps from a hash of the book's first series id, or its own id with no series, so a
series stands as a matched set and no book changes height between renders. This replaces `spineWidth`'s three classes and
`spineHeight`'s three classes, and it overrides the sentence in the handoff's `BookCard/README.md` that heights vary with length
(the one physical axis that reads as length is thickness). Large text still turns spines into cards (BRAND).

## Rows
No JavaScript. Every slot's margin box is exactly one zone high, so each flex line is the same height, and the planks are one
repeating background on the list. The last row's plank runs the full width, however few books stand on it.

## Not chosen
Covers facing out (they hide the thickness this view exists to show, and add images to a view that has none); books that lean; grouping by series (there is no series sort).
Amended by the lead (2026-11-08): the view is kept per person as `library.view` in `user_settings`, set by `POST /me/library-view` (the control is a form of three buttons, so it works without JavaScript); a bare Library address opens in it, an explicit `view=` wins, and large text shows the grid whatever is saved.

## Addendum (2026-11-11): rows fill to the edge
The Library's wrapped shelf sits in a sideways scroller (`.ts-shelf-run`) and the list is one widest book wider than it, so a row that wraps is always filled past the visible right edge and its gap is off screen; a small scroll shows the last book whole. Pure CSS, the page never scrolls sideways, and "Show more" stays centred in the visible width. Home's single row is unchanged. A shelf with a single short row still scrolls sideways by up to one widest book. CSS budgets rose 1 KiB.
