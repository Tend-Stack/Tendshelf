A book in four sizes: the Continue listening hero, a grid card, a list row, and a spine on the shelf.

## Hero

The one large card on Home: cover with paper page edges (`ts-book-block`), "Continue listening" overline in `accent`, serif title, byline, the chapter ruler, the where-you-are line and one 56px Resume button. Exactly one per Home. While it is on screen the mini-player is hidden.

Where-you-are copy, always in this order: `Chapter 12 · 18 min left in chapter · 6 h 40 m left in book`.

## Grid card

```html
<a class="ts-card" href="/books/{id}">
  <span class="ts-card__cover"><span class="ts-cover"><img src="…" alt=""></span>
    <span class="ts-card__ring ts-ring-badge">…ring…</span></span>
  <span class="ts-card__title">Salt and Ember</span>
  <span class="ts-card__author">Ruth Okafor</span>
</a>
```

Covers are square with `radius-cover` (4px; books have crisp corners). Phone grid: 3 columns at 390 wide (≈106px covers, 16px gaps). Tablet 5–6, desktop 7–8 at 140–160px. Titles wrap to two lines, then ellipsis.

## List row

Cover 56px, title, `author · length · progress`, and a separate play button. **Tapping the row opens the book; only the play button plays.** The row and the button are separate targets so a scroll-tap never starts playback.

## Spine

`ts-spine` on a `ts-shelf` with a `ts-shelf__plank` under it: for "New on the shelf" and the onboarding celebration. Spine color comes from the cover's dominant color, text from its readable contrast color; heights vary with book length (150–180px on phone). Each spine is a link with an `aria-label` of "Title by Author". When large text is on, spines fall back to grid cards (vertical text is harder to read).

## Accessibility

Cover images are decorative (`alt=""`) because the title is right there. Progress rings carry their own `aria-label`.
