A calm, centered message for when a region has nothing to show: a small line drawing, a serif headline, one plain sentence and one action.

## Markup

```html
<div class="ts-empty">
  <svg class="ts-empty__art" viewBox="0 0 120 72" aria-hidden="true">…</svg>
  <h2 class="ts-empty__title">Your shelf is empty</h2>
  <p class="ts-empty__body">Point Tendshelf at a folder of audiobooks and they'll appear here, covers and all.</p>
  <button class="ts-btn ts-btn--primary">Add a folder</button>
</div>
```

## Copy

| Where | Title | Body | Action |
| --- | --- | --- | --- |
| Empty library (owner) | Your shelf is empty | Point Tendshelf at a folder of audiobooks and they'll appear here, covers and all. | Add a folder |
| Empty library (guest) | Nothing shared yet | When someone shares books with you, they'll appear here. | — |
| No search results | Nothing for “{query}” | Check the spelling, or try an author or narrator. | Clear search |
| No downloads | No downloads yet | Download a book to listen without a connection. | Browse library |
| No bookmarks | No bookmarks yet | Tap the bookmark while listening to save a moment. | — |
| Load error | Couldn't load your library | Your server didn't answer. It may be restarting. Downloaded books still play. | Try again |
| Offline | (banner) You're offline. Downloaded books still play. | | |

## Rules

- The drawing is line work in `line-strong` with one `accent-fill` element, built from the shelf-and-spine motif. No illustrations of people, no mascots.
- One action at most. Errors always offer "Try again" and say what still works.
- Never blame the person; never show error codes in the title (put them in a "Details" disclosure for admins).
