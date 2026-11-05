# Tendshelf

A self-hosted, open-source server for your audiobooks and ebooks. Listen or read on a phone, a computer
or the desktop app, and pick up exactly where you left off on any device. It has chapters, bookmarks,
notes, a sleep timer, offline downloads, a book reader with highlights and search, a listening and
reading dashboard, and sharing with family by link or invitation.

**Current release: 2026.1; 2026.2 is being prepared.** See [CHANGELOG.md](CHANGELOG.md) for what each
release adds and its known limits, and [docs/roadmap.md](docs/roadmap.md) for what is built and which
checks are still open.

## What it is

- **Audiobooks and ebooks as books.** Point it at your folders. An audiobook and its ebook join into one
  book with one button for the format you used last. Copy-protected files are refused, never opened.
- **A player and a reader.** Speed, sleep timer, chapters, bookmarks, an Eyes-free mode for the car; for
  ebooks pages or scrolling, highlights, notes, search, look-up in dictionaries you add, and a word list.
- **Offline.** Download a book (audio and text) and use it with no connection.
- **Family.** Invite people, give each the libraries or books you choose, share a single book by an
  expiring link. Each person has their own place, shelves and statistics.
- **Desktop app.** A window around Tendshelf for Linux ([docs/desktop.md](docs/desktop.md)).
- **No Node.js anywhere.** One Go program (SQLite built in), templ templates, HTMX and a little plain
  JavaScript. `ffmpeg` and `ffprobe` are the only outside programs.
- **Private.** No telemetry, no tracking, no third-party fonts or CDNs. The server contacts the internet only
  when you ask it to look up a book's details or a cover, and then it sends only the title, author, ASIN or
  ISBN. Your files stay where they are; Tendshelf changes an audio file only when you press "Write details
  into the file", after it has verified a copy and kept a backup.

## Install and run

You need `ffmpeg` and `ffprobe` on the machine (on Debian or Ubuntu, `sudo apt install ffmpeg`), or pass their
paths with `-ffmpeg` and `-ffprobe`. Nothing else.

1. Get the program. Download `tendshelf-<version>-linux-amd64` or `-arm64` from the Releases page of the
   repository, check it against `SHA256SUMS`, make it executable (`chmod +x`) and put it somewhere on your
   `PATH`. Or build it: with Go 1.27 (see [decision 0003](docs/decisions/0003-newest-versions.md)) run
   `make build`, which writes `bin/tendshelf`.
2. Start it: `tendshelf`. It listens on `http://127.0.0.1:8095`. `tendshelf -version` prints the version.
3. To keep it running as a service, behind HTTPS, see [docs/deploy.md](docs/deploy.md).

Tags are `vYEAR.RELEASE.BUGFIX` (for example `v2026.1.0`; [decision 0006](docs/decisions/0006-version-numbers.md)).

### First-run setup

1. Start the program. Its log prints a setup link that carries a code (a new code at every start, until
   the owner exists).
2. Open the link. Create the owner account, name your server, and choose the folder that holds your
   audiobooks and ebooks. The first scan starts at once and its progress streams onto the page.
3. Open Home. Your books appear as they are found. Add more folders later under Libraries.

If you forget the owner's password: `tendshelf reset-password [-data DIR] <username>` prints a new one
once and signs that account out everywhere.

### Using it from your phone

By default the server answers only on the computer it runs on. To listen from a phone on your home
network, start it with `-addr 0.0.0.0:8095`, then open `http://<the computer's address>:8095` on the phone.
That is enough to listen. **Offline downloads and installing Tendshelf as an app need HTTPS**, because
browsers allow the service worker only there. A reverse proxy with a certificate, or
`tailscale serve --bg 8095`, gives you that; [docs/deploy.md](docs/deploy.md) has the steps and examples.

### The data folder and backups

Everything Tendshelf keeps lives in one data folder: the database (`tendshelf.db`), `backups`, `covers`,
`ebooks` (ebooks you upload), `dictionaries` (the files you add for look-up) and caches. Your own audio
files are never moved or changed. The default is `$XDG_DATA_HOME/tendshelf` or `~/.local/share/tendshelf` on
Linux, `Library/Application Support/Tendshelf` on macOS and `%LOCALAPPDATA%\Tendshelf` on Windows; `-data`
chooses another.

Before the program upgrades an existing database it writes a verified copy to
`backups/tendshelf-v<old>-<time>.db` and keeps the newest three. The owner can also schedule backups in
the admin pages. They hold the database only. To go back to an older version: stop the server, delete
`tendshelf.db-wal` and `tendshelf.db-shm` next to the database, and put a backup file back as `tendshelf.db`.
A database made by a newer version is refused untouched, with a message that says so.

To back up everything by hand, copy the whole data folder while the server is stopped.

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
checks, the visual capture helper and the desktop build. There is no Node in this project.

```sh
sudo apt install -y build-essential pkg-config sqlite3 ffmpeg woff2 libgtk-4-dev libwebkitgtk-6.0-dev
go install github.com/a-h/templ/cmd/templ@v0.3.1100      # also pinned in go.mod as a tool
go install golang.org/x/tools/gopls@latest
go install honnef.co/go/tools/cmd/staticcheck@latest
go install golang.org/x/vuln/cmd/govulncheck@latest
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0 # desktop; then: wails3 doctor
```

Add `$HOME/go/bin` to `PATH`. `gcc` is needed only for `go test -race`; the shipped binary builds
with `CGO_ENABLED=0`. Wails v3 uses GTK 4 and WebKitGTK 6.0 on Linux by default (the `gtk3` build tag
with `libgtk-3-dev libwebkit2gtk-4.1-dev` is its legacy fallback).

## Documents

- [docs/vision.md](docs/vision.md): who it is for and what "wow" means here
- [docs/feature-map.md](docs/feature-map.md): features against Audiobookshelf and Audible, by phase
- [docs/architecture.md](docs/architecture.md): packages, data model, routes, streaming, jobs
- [docs/roadmap.md](docs/roadmap.md): phases and their exit criteria
- [docs/deploy.md](docs/deploy.md): systemd, Caddy, nginx, HTTPS and Tailscale
- [docs/reader.md](docs/reader.md): the book reader
- [docs/desktop.md](docs/desktop.md): the desktop app (install, modes, limits)
- [docs/api.md](docs/api.md): the JSON API v1 (device tokens, library, progress and bookmark sync)
- [CHANGELOG.md](CHANGELOG.md): what each release adds, its known limits and how to upgrade
- [docs/copy-additions.md](docs/copy-additions.md): interface words that are not in the copy deck, for review
- [docs/design/README.md](docs/design/README.md): where the visual design lives
- [AGENTS.md](AGENTS.md): rules for contributors and coding agents

## Licence

Tendshelf is free software under the GNU Affero General Public License, version 3 or (at your option)
any later version. See [LICENSE](LICENSE). Credits for code and methods learned from other projects are
in [NOTICES.md](NOTICES.md).
