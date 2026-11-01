Labelled text inputs, the pill search field, and the select button that opens a picker sheet.

## Markup

```html
<div class="ts-field">
  <label class="ts-field__label" for="server-name">Name your server</label>
  <input id="server-name" class="ts-input" type="text">
  <span class="ts-field__hint">People you invite will see this name.</span>
</div>

<div class="ts-search" role="search">
  <svg class="ts-icon" …></svg>
  <label class="ts-sr" for="q">Search books, authors, narrators and notes</label>
  <input id="q" class="ts-input" type="search" name="q"
         hx-get="/search" hx-trigger="input changed delay:150ms, search" hx-target="#results">
</div>
```

## Rules

- Every input has a visible label above it, never a placeholder in place of a label. The search field is the one exception: its label is visually hidden and its icon and placeholder say what it searches.
- Inputs are 48px tall, `surface` with a `line-strong` border (3:1). Hover darkens the border to `ink-muted`; focus turns it `accent` and adds `focus-ring`.
- Errors: `aria-invalid="true"`, a `danger` border, and a message below with the alert icon, linked with `aria-describedby`. Say what to do, not what went wrong: "That email looks incomplete."
- Search is instant: debounce 150ms, swap only the results region, keep the query in the URL. On desktop show the `/` shortcut hint; replace it with a clear button once there is text.
- Selects are buttons that open a bottom sheet (phone) or a popover list (desktop) of `ts-option` rows. Never a native `<select>` for sort and filter, so the options can carry durations and checks.
- Disabled inputs keep their value readable (`ink-subtle` on `raised`) and a hint says why.
