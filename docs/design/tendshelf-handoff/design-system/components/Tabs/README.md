Editorial underline tabs that switch between sections of one page (About, Chapters, Bookmarks, Details) or between the Listen, Read along and Read modes.

## Markup

```html
<div role="tablist" aria-label="Book" class="ts-tabs">
  <button type="button" role="tab" aria-selected="true" aria-controls="panel-about" class="ts-tabs__item">About</button>
  <button type="button" role="tab" aria-selected="false" aria-controls="panel-chapters" class="ts-tabs__item">Chapters</button>
</div>
<section id="panel-about" role="tabpanel">…</section>
```

## Rules

- The active tab is `ink`, weight 600, with a 2px `accent` underline; the rest are `ink-muted`.
- Each panel is a server-rendered partial: `hx-get="/books/{id}/chapters" hx-target="#book-panel" hx-push-url="true"`, so the URL works as a deep link.
- The mode switcher (Listen · Read along · Read) uses the same look with `aria-pressed` buttons, because it changes the player's mode rather than showing a panel.
- Keep tab labels to one or two words. Four tabs at most on phone.

## Keyboard

Arrow keys move between tabs, Home and End jump to the first and last tab, Enter activates.
