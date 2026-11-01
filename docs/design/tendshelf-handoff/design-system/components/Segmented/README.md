A pill of two to four mutually exclusive options that apply immediately: grid or list, skip interval, theme.

## Markup

```html
<div role="radiogroup" aria-label="Library view" class="ts-seg">
  <button type="button" role="radio" aria-checked="true" class="ts-seg__item">Grid</button>
  <button type="button" role="radio" aria-checked="false" class="ts-seg__item">List</button>
</div>
```

## Rules

- Use it when the choice changes the view right away. For navigation between pages use Tabs; for more than four options use a select sheet.
- Labels are one word or a value ("30 s"). An icon may sit before the word; never an icon alone.
- The selected item is `raised` with `ink` text at weight 600; unselected items are `ink-muted`. The container border is `line-strong` so the control reads as a control at 3:1.
- Items are 44px tall. With HTMX, each item can `hx-get` the region it changes (`hx-target="#library-grid"`) and the server returns the item group with the new `aria-checked`.

## Keyboard

Tab into the group, arrow keys move and select, Space and Enter also select.
