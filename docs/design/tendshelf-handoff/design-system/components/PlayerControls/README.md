The full player's transport row (previous chapter, back, play/pause, forward, next chapter) and the settings bar beneath it (speed, sleep timer, chapters, output).

## Markup

```html
<section class="ts-player" style="--cover-accent:#9ccbc4; --cover-tint:#1f4a52">
  <div class="ts-transport">
    <button class="ts-iconbtn ts-iconbtn--md" aria-label="Previous chapter">…</button>
    <button class="ts-iconbtn ts-iconbtn--md ts-skip" aria-label="Back 30 seconds">…<span class="ts-skip__num">30</span></button>
    <button class="ts-iconbtn ts-iconbtn--cover ts-iconbtn--xl" aria-label="Pause">…</button>
    <button class="ts-iconbtn ts-iconbtn--md ts-skip" aria-label="Forward 30 seconds">…</button>
    <button class="ts-iconbtn ts-iconbtn--md" aria-label="Next chapter">…</button>
  </div>
  <div class="ts-player-bar">
    <button class="ts-player-bar__item"><span class="ts-player-bar__value">1.2×</span><span class="ts-player-bar__label">Speed</span></button>
    …
  </div>
</section>
```

## The cover accent

The player root sets `--cover-accent` and `--cover-tint` from the current cover (computed server-side when the cover is scanned and stored with the book). Only these use it: the play/pause button, the scrubber fill, needle and current-chapter line, the "left in chapter" label, and the background fade (`cover-tint` at 70% at the top, 30% at a third, `ground` by 60%). Everything else stays ink and ember.

Sampling rule: take the cover's most common saturated color and cap its chroma at 0.10 (OKLCH). Dark theme: lightness 0.75–0.85, with `on-cover-accent` charcoal (#14110E). Light and sepia: lightness 0.38–0.46, with `on-cover-accent` white, so the scrubber fill keeps 3:1 on paper. Contrast theme: white. Store both tones with the book at scan time. Covers with no usable color fall back to ember.

Examples: a teal cover gives #9CCBC4 in dark and #2C6660 in light; a moss cover gives #B8CB9A and #4B6338.

## Rules

- Skip buttons show the configured interval (15, 30, 45 or 60 s; back and forward can differ).
- The settings bar shows live values: "1.2×", "24:00" while a sleep timer runs (label turns `accent`), the output device's short name.
- Speed opens a sheet: presets 0.8, 1.0, 1.2, 1.5, 2.0 and a fine-tune slider from 0.5× to 3.0× in 0.05 steps.
- Sleep opens a sheet: End of chapter, 15 min, 30 min, 60 min, Custom. The last 10 s fade out; on phones, shaking adds 10 minutes.
- Keyboard on desktop: Space play/pause, ←/→ skip, B bookmark.
