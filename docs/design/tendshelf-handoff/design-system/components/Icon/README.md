Tendshelf's own icon set: 24px outline icons with a 1.75px round stroke, inlined as SVG so they take `currentColor`.

## Markup

```html
<svg class="ts-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M6.5 4h11v16.5L12 16.8l-5.5 3.7z"/></svg>
```

`ts-icon` sets size, `fill: none`, `stroke: currentColor`, 1.75 stroke, round caps and joins. `ts-icon--fill` fills instead (play, pause, active tab, pressed bookmark).

## The set

| Name | Path data |
| --- | --- |
| home | `M3.5 10.5 12 3.5l8.5 7V20a1 1 0 0 1-1 1H15v-6H9v6H4.5a1 1 0 0 1-1-1z` |
| library | `M4 4h4v16H4zM10 4h4v16h-4zM15.8 5.2l3.4-.9 3.4 14.6-3.4.9z` |
| search | `<circle cx="11" cy="11" r="7"/>` + `m20 20-3.5-3.5` |
| download | `M12 4v11M7 10l5 5 5-5M5 20h14` |
| profile | `<circle cx="12" cy="8" r="4"/>` + `M4 21c1.5-4 4.5-6 8-6s6.5 2 8 6` |
| play (fill) | `M8 5.5v13a1 1 0 0 0 1.5.86l10.5-6.5a1 1 0 0 0 0-1.72L9.5 4.64A1 1 0 0 0 8 5.5z` |
| pause (fill) | two `rect`s: x 6 and 14, y 5, 4×14, rx 1 |
| skip-back | `M4.5 12a7.5 7.5 0 1 0 2.4-5.5` + `M4.5 3.5v4h4` (number overlaid) |
| skip-forward | `M19.5 12a7.5 7.5 0 1 1-2.4-5.5` + `M19.5 3.5v4h-4` |
| prev-chapter | `M6 5v14` + `M18.5 6.5v11L10 12z` |
| next-chapter | `M18 5v14` + `M5.5 6.5v11L14 12z` |
| sleep | `M19.5 14.5A8 8 0 0 1 9.5 4.5a8 8 0 1 0 10 10z` |
| bookmark | `M6.5 4h11v16.5L12 16.8l-5.5 3.7z` |
| chapters | `M9 6h11M9 12h11M9 18h11M4.5 6h.01M4.5 12h.01M4.5 18h.01` |
| output | `M4 9.5h3.5L12 5.5v13l-4.5-4H4z` + `M15.5 9a4.2 4.2 0 0 1 0 6M18 6.5a8 8 0 0 1 0 11` |
| share | `M12 3v12M7 8l5-5 5 5M5 13v6a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-6` |
| invite | `<circle cx="10" cy="8" r="4"/>` + `M3 21c1.4-4 4-6 7-6s5.6 2 7 6M19 8v6M16 11h6` |
| edit | `M4 20h4L19 9l-4-4L4 16z` |
| filter | `M4 6h16M7 12h10M10 18h4` |
| grid | `M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z` |
| check | `m5 12.5 4.5 4.5L19 7.5` |
| close | `M6 6l12 12M18 6 6 18` |
| alert | `M12 4 2.5 20h19z` + `M12 10v4M12 17h.01` |
| folder | `M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z` |

## Rules

- One set only; new icons follow the same 24px grid, 1.75px stroke and rounded ends.
- Active and pressed states fill the shape (home, bookmark) or switch to `ts-icon--fill`.
- Icons are `aria-hidden`; their button or link carries the label. No emoji anywhere in the product.
- In a templ project, keep these as one `icons.templ` component keyed by name.
