# Tendshelf

A self-hosted, open-source audiobook server and player. Your books, on your own machine, with the
listening comfort of a commercial app: pick up exactly where you left off on any device, at any speed,
with chapters, bookmarks, notes, a sleep timer and offline downloads. Share a library or a single book
with the people you choose, by link, for as long as you choose.

**Status: first release, 2026.1.** You can add your audiobook folders, listen on a phone or a computer,
keep your place on every device, and share with your family. Offline downloads, metadata lookup and
read-along are not built yet. See [CHANGELOG.md](CHANGELOG.md) for what is in this release and its known
limits, and [docs/roadmap.md](docs/roadmap.md) for what comes next.

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
  as an app (PWA) and with offline downloads, which are planned for the next milestone; desktop apps later through Wails.
- **Private.** No telemetry, no tracking, no third-party fonts or CDNs. The server talks to the
  internet only when you ask it to look up a book's details, and then it sends only the title,
  author, ASIN or ISBN.
- **Self-hosted.** Your files stay where they are and stay unchanged. Tendshelf changes an audio file
  only when you press "Write details into the file", and then only after it has verified the copy and
  kept a backup.

## Install and run

Tendshelf is one program. You need `ffmpeg` and `ffprobe` on the machine (or pass their paths with the
flags below); nothing else. To build it you need Go 1.27 (newest stable; see
[decision 0003](docs/decisions/0003-newest-versions.md)).

```sh
make build            # bin/tendshelf, stamped with the version from git
bin/tendshelf         # http://127.0.0.1:8095
bin/tendshelf -version
```

Tags are `vYEAR.RELEASE.BUGFIX` (for example `v2026.1.0`; [decision 0006](docs/decisions/0006-version-numbers.md)).
`tendshelf -version` and the start-up log line show the version.

### First-run setup

1. Start the program. Its log prints a setup link that carries a code (a new code at every start, until
   the owner exists).
2. Open the link. Create the owner account, name your server, and choose the folder that holds your
   audiobooks. The first scan starts at once and its progress streams onto the page.
3. Open Home. Your books appear as they are found. Add more folders later under Libraries.

If you forget the owner's password: `tendshelf reset-password [-data DIR] <username>` prints a new one
once and signs that account out everywhere.

### Using it from your phone

By default the server answers only on the computer it runs on. To listen from a phone on your home
network, start it with `-addr 0.0.0.0:8095` (or put it behind a reverse proxy you already run, with
`-trusted-proxy`), then open `http://<the computer's address>:8095` on the phone. Over plain `http` a
phone browser keeps the sound, the lock screen controls and the bookmarks, but not the shake-to-extend
sleep timer, which needs HTTPS. The one-handed phone check is in [docs/manual-checks.md](docs/manual-checks.md).
It has not been run on real phones yet (see the known limits in the changelog).

### The data folder and backups

Everything Tendshelf keeps lives in one data folder: the database (`tendshelf.db`), the `backups`
folder, cover thumbnails and caches. Your audio files are never moved or changed. The default is
`$XDG_DATA_HOME/tendshelf` or `~/.local/share/tendshelf` on Linux, `Library/Application Support/Tendshelf`
on macOS and `%LOCALAPPDATA%\Tendshelf` on Windows; `-data` chooses another. Before the program upgrades an
existing database it writes a verified copy to `backups/tendshelf-v<old>-<time>.db` and keeps the newest
three. To go back, stop the server and put a backup file back as `tendshelf.db`. A database made by a newer
version is refused untouched, with a message that says so.

To back up by hand, copy the data folder while the server is stopped.

## Flags

Every flag has an environment variable; `tendshelf -h` prints them all. The ones most people touch:

- `-addr` (`TENDSHELF_ADDR`): listen address, default `127.0.0.1:8095`.
- `-data` (`TENDSHELF_DATA`): data folder (database, backups, covers, caches).
- `-ffmpeg`, `-ffprobe` (`TENDSHELF_FFMPEG`, `TENDSHELF_FFPROBE`): program names or absolute paths; default searches `PATH`.
- `-trusted-proxy` (`TENDSHELF_TRUSTED_PROXY`): reverse proxies whose `X-Forwarded-*` headers are believed.
- `-compat-cache-gib` (`TENDSHELF_COMPAT_CACHE_GIB`): size cap of the compatibility-copy cache, default 5.
- `-waveforms` (`TENDSHELF_WAVEFORMS`): draw waveforms behind the scrubber, default on. Each audio file is
  decoded once in the background (about 4 seconds of CPU per hour of audio); `-waveforms=false` turns it
  off on weak hardware, and nothing is decoded.

`tendshelf -version` prints the version and exits.

## For developers

`make check` (gofmt, `go vet`, `go test -race ./...`), `make build`, `make generate` (templ), `make run`.
Browser checks (`make player`, `make visual`, `make visual-full`, `make visual-webkit`) need
`TENDSHELF_TEST_BROWSER` pointing at a Chromium; see the Makefile comments. The JSON API is in
[docs/api.md](docs/api.md).

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
- [docs/api.md](docs/api.md): the JSON API v1 (device tokens, library, progress and bookmark sync)
- [CHANGELOG.md](CHANGELOG.md): what each release adds, its known limits and how to upgrade
- [docs/copy-additions.md](docs/copy-additions.md): interface words that are not in the copy deck, for review
- [docs/design/README.md](docs/design/README.md): where the visual design lives
- [AGENTS.md](AGENTS.md): rules for contributors and coding agents

## Licence

Tendshelf is free software under the GNU Affero General Public License, version 3 or (at your option)
any later version. See [LICENSE](LICENSE). Credits for code and methods learned from other projects are
in [NOTICES.md](NOTICES.md).
