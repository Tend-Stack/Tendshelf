# 0008 Library filter header

Date: 2026-11-03. Status: accepted (the operator's request, 2026-11-03: "All categories in one drop
down. Sorting will go under show ... If the user choose Author on the filter, then that is when the
author drop down will shows up").

## Decision

This overrides `design-system/components/Chip/README.md` and the Library boards where they differ.

- The Library header has one "Filter" chip menu listing the kinds (Author, Narrator, Series,
  Category, Length). Choosing a kind adds that kind's chip menu, open; only kinds in use show. Each
  active chip shows its value and an "Any ..." item clears it.
- Genres are one "Category" menu (multi-select, at most five), not a chip per genre.
- "Show" holds the show states and "Sort by"; there is no separate sort control.
- The "Filter and sort" sheet and "More filters" are retired; the same chip menus serve phone,
  tablet and desktop, without JavaScript.
- `add=<kind>` opens a kind for one page view and is never carried forward.

Plan: [docs/plans/library-filters.md](../plans/library-filters.md).
