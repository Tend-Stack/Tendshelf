One row in a chapter list: number, title, duration and a state mark (played check, now-playing bars, partial ring, or nothing).

## Markup

```html
<a class="ts-chapter" href="/books/{id}/play?chapter=12" aria-current="true"
   aria-label="Chapter 12, The Second Lamp, now playing, 18 minutes left">
  <span class="ts-chapter__num">12</span>
  <span class="ts-chapter__title">The Second Lamp</span>
  <span class="ts-chapter__dur">18:12 left</span>
  <span class="ts-chapter__state"><span class="ts-eq"><span></span><span></span><span></span></span></span>
</a>
```

## States

- **Played:** `is-played`; title in `ink-muted`, a `success` check.
- **Current:** `aria-current="true"`; `accent-soft` fill, title at 600, animated bars in `accent` (static under reduced motion), duration becomes time left.
- **Partly listened:** a small ring with the chapter's progress.
- **Not started:** no mark.
- **Read-along gap** (reader feature): a small speaker icon means the text could not be matched to the audio here; the chapter still plays and the text still reads.

## Rules

Rows are 56px. Tapping a row jumps playback to that chapter's start, which is the one place a list row plays: the list is inside the player or the chapter sheet, where that is what people expect. On Book detail, the same rows open the chapter only after an explicit Play.
