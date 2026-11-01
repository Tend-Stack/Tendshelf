Pill chips that filter the library and search results, plus static info chips for book facts.

## Markup

```html
<div class="ts-chip-set" role="group" aria-label="Filters">
  <button type="button" class="ts-chip" aria-pressed="true">
    <svg class="ts-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="m5 12.5 4.5 4.5L19 7.5"/></svg>
    Unfinished
  </button>
  <button type="button" class="ts-chip" aria-pressed="false">Downloaded</button>
</div>
<span class="ts-chip ts-chip--static">11 h 50 m</span>
```

## Rules

- A selected filter chip gets a check icon, `accent-soft` fill and an `accent` border, so it differs by shape and weight, not color alone.
- Filters apply at once (`hx-get` with the filter set, `hx-target="#library-results"`). Show the result count in the page, not on the chip.
- On phone, show the four most-used filters and a "More filters" chip that opens the filter sheet. On desktop the full filter bar fits.
- Static chips (`--static`) are not buttons. Use them for length, series position and year on Book detail. Keep them to three.
- Chips are 40px tall with an 8px gap, so each one's hit area reaches 44px.
