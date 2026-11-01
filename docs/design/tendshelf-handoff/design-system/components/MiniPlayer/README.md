The persistent mini-player: cover, title, chapter and time left, back 30 s and play/pause, with a 2px progress line along its top edge.

## Markup

```html
<div class="ts-mini" id="mini-player" hx-preserve>
  <div class="ts-mini__progress" style="width:62%"></div>
  <a class="ts-mini__open" href="/play" aria-label="Open player: The Lighthouse Keeper's Daughter, chapter 12, 18 minutes left">
    <span class="ts-cover" style="width:48px;height:48px"><img src="…" alt=""></span>
    <span class="ts-mini__text">
      <span class="ts-mini__title">The Lighthouse Keeper's Daughter</span>
      <span class="ts-mini__meta">Chapter 12 · 18 min left</span>
    </span>
  </a>
  <button type="button" class="ts-iconbtn" aria-label="Back 30 seconds">…</button>
  <button type="button" class="ts-iconbtn ts-iconbtn--filled" aria-label="Pause">…</button>
</div>
```

## Where it lives

- **Phone:** 8px above the tab bar, 8px side insets, `size-mini-player` tall, `radius-md`, `shadow-float`.
- **Tablet:** docked at the bottom of the content area, beside the rail.
- **Desktop:** a full-width bar at the bottom of the window; it can expand into the right-side player panel instead of a full screen.
- **Hidden** while the Home "Continue listening" card is on screen (one Resume, never two); it slides in once that card scrolls out of view. Also hidden on the full player and when nothing has ever been played.

## Behaviour

- Tap the text or cover to open the full player; swipe up does the same; swipe down on the full player collapses back. The cover is a shared element in that transition (see the motion spec in the brand book).
- Long-press play/pause opens the speed sheet.
- It sits outside the swapped region (`hx-preserve`) so audio and its state survive every HTMX page swap.
- In the mini-player the play button and progress line are ember, not the cover accent: it lives in the chrome.
