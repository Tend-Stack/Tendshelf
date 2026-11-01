The reading surface for Read along and Read. Server-rendered; one chapter per partial.

- `.ts-reader` carries `--read-size` (18, 20 or 22px, from `read-sm`/`read-md`/`read-lg`) and `--read-leading` (`leading-compact`, `leading-normal`, `leading-airy`). `.ts-reader--sans` switches the text to Fira Sans.
- `.ts-reader__col` stops at `size-read-measure` (68ch). Phone padding is 24px each side. On tablet and desktop the column is centred and never narrower than `size-read-measure-min` (45ch).
- Paragraphs after the first indent 1.4em; no gaps between paragraphs. Hyphenation and `text-wrap: pretty` are on. Text is left-aligned, never justified.
- Your highlights use `.ts-hl` (gold wash plus a 2px gold underline). Notes add a `.ts-notedot` button after the sentence, labelled with who wrote it.
- Selection is the system selection, recoloured with `selection` so it never looks like a highlight.
- Use real `<p>` and headings so screen readers can move by paragraph and heading.
