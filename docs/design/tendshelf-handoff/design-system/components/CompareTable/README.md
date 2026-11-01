The side-by-side sheet behind "Look up details online": each field's current value and the value found online, with source badges, and a tick to choose the online value.

## Markup

```html
<table class="ts-compare">
  <caption class="ts-sr">Compare current details with details found online</caption>
  <thead><tr><th>Field</th><th>Now</th><th>Found online</th><th><span class="ts-sr">Use online value</span></th></tr></thead>
  <tbody>
    <tr class="is-diff">
      <th scope="row" class="ts-compare__field">Narrator</th>
      <td><div class="ts-compare__val is-other">T. Marsh<span class="ts-source">From file</span></div></td>
      <td><div class="ts-compare__val is-chosen">Theo Marsh<span class="ts-source">Online · 31 Oct</span></div></td>
      <td><input class="ts-check" type="checkbox" checked aria-label="Use online narrator"></td>
    </tr>
  </tbody>
</table>
```

## Rules

- Rows that differ get `is-diff` (a `surface` fill); identical rows show a disabled tick.
- The value that will be kept is `ink` (`is-chosen`); the other is `ink-muted` (`is-other`). Ticking swaps them.
- Sources, as badges: "From file" (tags in the audio), "Online · {date fetched}", "Edited by you" (accent). Values the person edited are never pre-ticked for replacement.
- Ratings from each source are listed separately with their fetch date; never averaged.
- The primary button counts what will change: "Use 2 changes". With HTMX, the form posts the ticked fields and the Details tab re-renders.
- On phone the table becomes stacked cards: field name, then Now and Found online one above the other, with the tick on the right.
