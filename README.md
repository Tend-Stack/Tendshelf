# Tendshelf

A self-hosted, open-source audiobook server and player. Your books, on your own machine, with the
listening comfort of a commercial app: pick up exactly where you left off on any device, at any speed,
with chapters, bookmarks, notes, a sleep timer and offline downloads. Share a library or a single book
with the people you choose, by link, for as long as you choose.

**Status: early.** This repository holds the design documents and a compiling skeleton. Nothing here
is ready for your library yet. See [docs/roadmap.md](docs/roadmap.md).

## Why another audiobook server

Audiobookshelf proved that people want to own their audiobook libraries. Tendshelf takes a different
route to the same goal:

- **Fast.** Server-rendered pages that appear at once, even on a phone on a weak connection and on a
  small home server. No large JavaScript bundle to download and start.
- **No Node.js anywhere.** Not in the app, not in the build. Go, templ templates, HTMX and a small
  amount of plain JavaScript for the player.
- **One binary.** Download it, run it, open the page. SQLite is built in. `ffmpeg`/`ffprobe` are the
  only outside programs.
- **Mobile-first.** Designed for a phone in one hand first, and beautiful on a large screen. Installable
  as an app (PWA) with offline downloads; desktop apps later through Wails.
- **Private.** No telemetry, no tracking, no third-party fonts or CDNs. The server talks to the
  internet only when you ask it to look up a book's details, and then it sends only the title,
  author, ASIN or ISBN.
- **Self-hosted.** Your files stay where they are and stay unchanged. Tendshelf changes an audio file
  only when you press "Write details into the file", and then only after it has verified the copy and
  kept a backup.

## Running the skeleton

Requires Go 1.27 (newest stable; see [decision 0003](docs/decisions/0003-newest-versions.md)).

```sh
make build   # bin/tendshelf
make test
make run     # http://127.0.0.1:8095 and /healthz
```

## Developer setup (Ubuntu 26.04)

The server needs only Go, plus `ffmpeg`/`ffprobe` at run time. The extra packages below are for
checks, the visual capture helper and the later desktop build. There is no Node in this project.

```sh
sudo apt install -y build-essential pkg-config sqlite3 ffmpeg woff2 libgtk-4-dev libwebkitgtk-6.0-dev
go install github.com/a-h/templ/cmd/templ@v0.3.1100      # also pinned in go.mod as a tool
go install golang.org/x/tools/gopls@latest
go install honnef.co/go/tools/cmd/staticcheck@latest
go install golang.org/x/vuln/cmd/govulncheck@latest
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0 # desktop (P5); then: wails3 doctor
```

Add `$HOME/go/bin` to `PATH`. `gcc` is needed only for `go test -race`; the shipped binary builds
with `CGO_ENABLED=0`. Wails v3 uses GTK 4 and WebKitGTK 6.0 on Linux by default (the `gtk3` build tag
with `libgtk-3-dev libwebkit2gtk-4.1-dev` is its legacy fallback).

## Documents

- [docs/vision.md](docs/vision.md): who it is for and what "wow" means here
- [docs/feature-map.md](docs/feature-map.md): features against Audiobookshelf and Audible, by phase
- [docs/architecture.md](docs/architecture.md): packages, data model, routes, streaming, jobs
- [docs/roadmap.md](docs/roadmap.md): phases and their exit criteria
- [docs/design/README.md](docs/design/README.md): where the visual design lives
- [AGENTS.md](AGENTS.md): rules for contributors and coding agents

## Licence

Tendshelf is free software under the GNU Affero General Public License, version 3 or (at your option)
any later version. See [LICENSE](LICENSE). Credits for code and methods learned from other projects are
in [NOTICES.md](NOTICES.md).
