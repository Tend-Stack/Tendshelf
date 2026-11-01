The phone's bottom navigation: Home, Library, Search, Downloads, Profile.

## Markup

```html
<nav aria-label="Main" class="ts-tabbar">
  <a href="/" class="ts-tabbar__item" aria-current="page" hx-boost="true">
    <svg class="ts-icon" …></svg>Home
  </a>
  …
</nav>
```

## Rules

- Five tabs, always in this order, always labelled. Labels use `meta` (13px).
- The current tab is `accent`, its icon filled, with a 3px ember bar at the top edge; the others are `ink-muted` outline icons.
- `size-tab-bar` (72px) tall, including the home-indicator inset. The mini-player docks 8px above it.
- Tabs are boosted links: the content region swaps, the tab bar and mini-player stay (`hx-preserve`).
- Tapping the current tab again scrolls its page to the top.
- Becomes the side rail at 768px and the sidebar at 1280px.
