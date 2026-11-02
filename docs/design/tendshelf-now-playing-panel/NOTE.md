# Note (lead, 2026-11-02)

The operator's design for the desktop "Now playing" panel, kept as a reference. The reference
`templ/now_panel.templ` and `now_panel_test.go` are stored with a `.txt` suffix so `make generate`
and `go test ./...` do not pick them up (this folder is inside the app's module).

Operator's adjustment when handing it over: keep the panel to **About and Bookmarks**; the Chapters
tab is left out on desktop, because the player bar already opens its own chapters list (it may make
sense on mobile). How the design maps onto the app, and the declared differences, are in
`docs/design/screens.md`.
