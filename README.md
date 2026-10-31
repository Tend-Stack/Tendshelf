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

Requires Go 1.27.

```sh
make build   # bin/tendshelf
make test
make run     # http://127.0.0.1:8095 and /healthz
```

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
