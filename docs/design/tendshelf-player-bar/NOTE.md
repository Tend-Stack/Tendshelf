# Note (lead, 2026-11-02)

The operator's design for the desktop player bar, kept as a reference like `../tendshelf-handoff/`.
`templ/player_bar.templ` is stored as `player_bar.templ.txt` so `make generate` (which walks the
whole tree) does not turn it into Go code; `go/` is its own module and is not built with the app.
How it maps onto the app's existing code, and the declared differences, are in `docs/design/screens.md`.
