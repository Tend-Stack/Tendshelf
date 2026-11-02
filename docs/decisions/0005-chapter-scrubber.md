# 0005 The scrubber covers the current chapter

Date: 2026-11-02. Status: accepted by the operator (2026-11-02).

## Context

The handoff's Scrubber (`docs/design/tendshelf-handoff/design-system/components/Scrubber/README.md`)
is one ruler across the whole book that settles on a chapter start when the finger is within 1.5 % of
it. In a 13 hour book with 27 chapters, 1.5 % is about 12 minutes and chapters are about 30 minutes
apart, so most of the line snapped to a chapter start, and the current chapter was about 14 px wide
on a phone. The operator's words: the timeline is compacted rather than using the full length;
jumping within the same chapter is almost impossible, it just jumps to the next chapter; a click on
the timeline or a grab of the indicator should move to a different timestamp within the same
chapter.

## Decision

The operator, on the proposal below: "Yes that is a better approach and more natural for the user.
For chapters, the user can click the next and back buttons."

1. The main scrubber of the full player, and the compact scrubber in the desktop mini-player bar,
   cover the current chapter: the line's left end is the chapter's start and its right end the
   chapter's end. A click anywhere on the line jumps there; dragging the needle scrubs; dragging up
   still slows the scrub (fine control). There is no snapping to chapter starts inside a chapter.
2. The scrubber moves on to the next chapter as playback crosses the end. A drag stays inside the
   chapter it began in. The previous and next chapter buttons (and Page Up and Page Down on the
   scrubber) move between chapters.
3. Keys on the scrubber: left and right by the skip setting, Page Up and Page Down by a chapter, Home
   and End to the chapter's start and end. The label is "Position in chapter" and `aria-valuetext`
   names the chapter and the time elapsed in it.
4. The whole book stays visible as a slim ruler above the scrubber (the hero card's `ts-ruler`
   style): chapter ticks and the book fill, not interactive, hidden from a screen reader (the text
   under the line carries the same facts). The times are the elapsed time in the chapter, "{m:ss} left
   in chapter" and "{h} h {m} m left in book".

## Consequences

- A declared difference from the Scrubber board and README in `docs/design/screens.md`.
- A book of one long chapter (no chapter marks) gets one chapter that spans the book, so the
  scrubber behaves as the handoff's did.
- The handoff copy is not edited; this decision overrides its Scrubber description.
