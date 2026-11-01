An on/off switch inside a full-width settings row, with a plain label and an optional one-line hint.

## Markup

```html
<div class="ts-switch-row">
  <span class="ts-switch-row__text">
    <span class="ts-switch-row__label" id="wifi">Download on Wi-Fi only</span>
    <span class="ts-switch-row__hint">Saves your mobile data.</span>
  </span>
  <button type="button" role="switch" aria-checked="true" aria-labelledby="wifi" class="ts-switch"></button>
</div>
```

## Rules

- The label says what is true when the switch is on ("Download on Wi-Fi only"), never a question.
- Changes save immediately; no Save button. With HTMX, `hx-post` the new value and swap the row.
- Off: `surface` track with a `line-strong` border and an `ink-muted` knob. On: `accent-fill` track with an `on-accent` knob. On and off also differ by knob position, not color alone.
- The whole row is 56px; the switch itself is 52×32 inside a 44px hit area.
- Disabled rows keep the hint, which says why ("Only on phones with motion sensors").
