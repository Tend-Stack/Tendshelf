The player's scrubber: a ruler across the whole book with a tick per chapter, a needle for the playhead, and three times beneath (elapsed, left in chapter, left in book).

## Markup

```html
<div class="ts-scrub" style="--cover-accent: #9ccbc4">
  <div class="ts-scrub__line"></div>
  <span class="ts-scrub__tick" style="left: 57.8%"></span> <!-- one per chapter boundary -->
  <div class="ts-scrub__fill" style="width: 62%"></div>
  <div class="ts-scrub__chapter" style="left: 57.8%; width: 5.9%"></div> <!-- current chapter -->
  <div class="ts-scrub__needle" style="left: 62%"></div>
  <div class="ts-scrub__bubble">Chapter 13 · The Keeper's Log · 4:10</div>
  <input class="ts-scrub__input" type="range" min="0" max="63132" value="39108"
         aria-label="Position in book" aria-valuetext="10 hours 51 minutes elapsed, chapter 12">
</div>
<div class="ts-scrub__times"><span>10:51:48</span><span>18:12 left in chapter</span><span>−6:40:12</span></div>
```

The fill, needle and current-chapter underline use `--scrub-fill`, which defaults to `cover-accent`. Outside the player (read-along bar, desktop panel) it is still the cover accent; in the hero card use the plain `ts-ruler` from Progress.

## Behaviour

- **Dragging** adds `is-dragging`: the bubble shows the chapter name and the time inside that chapter; the needle grows.
- **Fine scrub:** the further the finger moves up from the bar, the slower the scrub (1×, ½×, ¼×). The middle label changes to "Drag up for fine control" while dragging.
- **Chapter ticks snap lightly:** within 1.5% of a tick, the value settles on the chapter start, with a light haptic tick on phones.
- The value is in seconds. Keyboard: ←/→ move 30 s (the skip setting), Page Up/Down move a chapter, Home/End go to the start/end.
- A small client script updates the needle and times every second; the server never re-renders the scrubber while playing.
