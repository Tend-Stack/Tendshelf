# Design

Visual design for Tendshelf is produced with Claude Design and then implemented by hand as templ
components and plain CSS.

## Files

| Path | What | Owner |
| --- | --- | --- |
| `docs/design/claude-design-prompt.md` | The brief given to Claude Design (screens, tone, constraints). | The lead writes it; do not edit without the lead. |
| `docs/design/exports/<YYYY-MM-DD>-<topic>/` | What Claude Design returns: screenshots (PNG), HTML/CSS mock-ups, notes. One folder per round; never overwritten. | Added as received. |
| `docs/design/tokens.md` | The chosen design tokens (colours with contrast ratios, type scale, spacing, radii, motion) with the export they came from. | Written when a round is accepted. |
| `docs/design/screens.md` | Accepted screens per breakpoint (390, 768, 1440 px) and their states (empty, loading, error, offline). | Written when a round is accepted. |

## Rules for turning a design into code

- Exports are references, not code to paste. Rebuild them as templ components in
  `internal/web/components/` and CSS in `internal/web/static/css/`; tokens become custom properties in
  `tokens.css`.
- No external fonts, icon CDNs, Tailwind classes or framework markup from an export. Fonts are system
  fonts or self-hosted files with their licence in `NOTICES.md`; icons are inline SVG sprites.
- Every accepted colour pair is checked for WCAG AA contrast and recorded in `tokens.md`, light and dark.
- Mobile (390 px) is the primary frame; desktop layouts extend it, they do not replace it.
- Keep the persistent player bar and the `#main` content region from `docs/architecture.md`; a design
  that needs a full-page reload during playback is sent back.
