# Notices

Tendshelf is licensed AGPL-3.0-or-later. This file credits code, methods and assets that come from
other projects. Every port, vendored file or third-party dependency adds a row here in the same commit.

## Methods and code ported from other projects

| Tendshelf file | Source project and file(s) | Licence | How |
| --- | --- | --- | --- |
| (none yet) | | | |

Expected first entries (fill in when the port lands; do not pre-credit code that does not exist):

- **Audiobookshelf**, https://github.com/advplyr/audiobookshelf, GPL-3.0 (compatible with AGPL-3.0;
  the combined work is distributed under AGPL-3.0-or-later). Likely sources:
  `server/utils/parsers/parseNameString.js`, `parseSeriesString.js`, `parseNfoMetadata.js`,
  `parseOpfMetadata.js`, `server/utils/scandir.js`, `server/scanner/AudioFileScanner.js`,
  `server/scanner/BookScanner.js`, `server/providers/Audnexus.js`, `Audible.js`, `GoogleBooks.js`,
  `OpenLibrary.js`, `iTunes.js`.
- **TendVox Extractor** (internal, same author): `extractor_details.py`, `extractor_online.py`,
  `extractor_db.py`, `extractor_pitch.py`, `extractor_measure.py`, `categories.json`. Those files
  themselves follow Audiobookshelf; keep both credits.
- **TendVox** (internal, same author), for read-along (R3): `runtimes/alignment/worker.py` (basis of
  `runtimes/align/worker.py`), `internal/review/fidelity_normalize.go` and the English number forms
  (basis of the `tendshelf-normalize-v1` normaliser), the owned-server lifetime in
  `internal/providers/alignment_server_linux.go`.

## Vendored files

| File | Project | Version | Licence |
| --- | --- | --- | --- |
| `internal/web/static/vendor/htmx-4.0.0.min.js` (SHA-256 `e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f`; from `https://raw.githubusercontent.com/bigskysoftware/htmx/v4.0.0/dist/htmx.min.js`; licence text in `vendor/HTMX-LICENSE.txt`) | htmx (bigskysoftware) | 4.0.0 | 0BSD |
| `internal/web/static/fonts/Fraunces-Variable.woff2` (SHA-256 `e6638ea113d0027354a08f957a4068975c8066395a0d0f7bb7861f6409621be3`; 205,500 B) | Fraunces, commit `ea507ccb0a2a8a3f8644385c4de7fa3fa1078ffe`, `fonts/webfonts/variable/Fraunces[SOFT,WONK,opsz,wght].woff2` | commit | OFL-1.1 (`fonts/OFL-Fraunces.txt`, SHA-256 `bdf4c22802eaf804f998195871c6b8938aac2ac14b2d78a8bd66a6f1eced833b`) |
| `internal/web/static/fonts/Fraunces-Italic-Variable.woff2` (SHA-256 `a61bef9d19ae97bbb5c5f3613873ecfd4258ada3f10ed7e20ca3115534695cf2`; 246,260 B) | Fraunces, same commit, `fonts/webfonts/variable/Fraunces-Italic[SOFT,WONK,opsz,wght].woff2` | commit | OFL-1.1 |
| `internal/web/static/fonts/FiraSans-Regular.woff2` (SHA-256 `51000d3cc8a601427bdb88625275e0eefc00570f4f2ab7a926fa336abee7098f`) | Fira Sans (bBoxType), commit `f54eeb3124c63fe9b5bcd36d09d1cd46788cd15e`, `Fira_Sans_4_3/Fonts/Fira_Sans_WEB_4301/Normal/Roman/` | 4.301 | OFL-1.1 (`fonts/OFL-Fira.txt`, SHA-256 `5c29650250730778eccb5475b112d32a8e0c9dd1860d9509693329652bf8e9eb` is the repository `OFL.txt` at that commit) |
| `internal/web/static/fonts/FiraSans-Medium.woff2` (SHA-256 `14d421d4fb35d56b40e958cc9b64eb1528d1822bb75251623955c873a9a175d3`) | Fira Sans, same commit and folder | 4.301 | OFL-1.1 |
| `internal/web/static/fonts/FiraSans-SemiBold.woff2` (SHA-256 `01ca0c4a1f02dd4324721cf8766bcbb2edca4dee0fb8884a66e58e231ed62d55`) | Fira Sans, same commit and folder | 4.301 | OFL-1.1 |
| `internal/web/static/fonts/FiraSans-Italic.woff2` (SHA-256 `1d70c09acc08f385ff6874c81ba8fe296d9e393d8d64615f2724113735d127d8`) | Fira Sans, same commit, `.../Normal/Italic/` | 4.301 | OFL-1.1 |
| `internal/web/static/fonts/FiraMono-Regular.woff2` (SHA-256 `87c26294dfa3be4318160a1cbc76f07172f683ea889e837bdeff632021e17389`) | Fira Mono (bBoxType), same commit, `Fira_Mono_3_2/Fonts/FiraMono_WEB_32/` | 3.2 | OFL-1.1 |
| `internal/web/static/fonts/FiraMono-Medium.woff2` (SHA-256 `86f75c8c0ab63309694387ae4dc8fed869a705a6b01c2d4511ecb0e703e870b2`) | Fira Mono, same commit and folder | 3.2 | OFL-1.1 |
| `internal/web/static/img/tendshelf-{mark-ember,mark-cream,app-icon}.svg` | Tendshelf design handoff (`docs/design/tendshelf-handoff/`), copied unchanged | n/a | AGPL-3.0-or-later (project asset) |
| `internal/web/static/css/tokens.css`, `css/bundle.css` | Tendshelf design handoff, copied; changes: one `--font-mono` line, the P0b app-shell section | n/a | AGPL-3.0-or-later (project asset) |

## Go module dependencies

| Module | Licence | Why |
| --- | --- | --- |
| github.com/a-h/templ v0.3.1100 | MIT | templ runtime (`internal/web`); the CLI runs through `go tool`, not linked into the binary. |
| modernc.org/sqlite v1.61.1 | BSD-3-Clause | Pure-Go SQLite driver (`internal/db` only). The SQLite C source it carries is public domain. |
| modernc.org/libc v1.79.1 | BSD-3-Clause | C runtime for the transpiled SQLite (indirect). |
| modernc.org/mathutil v1.7.1 | BSD-3-Clause | Helper of modernc.org/libc (indirect). |
| modernc.org/memory v1.12.1 | BSD-3-Clause | Allocator of modernc.org/libc (indirect). |
| github.com/google/uuid v1.6.0 | BSD-3-Clause | Dependency of modernc.org/sqlite (indirect). |
| github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | Dependency of modernc.org/libc (indirect). |
| golang.org/x/sys v0.49.0 | BSD-3-Clause | Operating-system calls for modernc.org/sqlite and libc (indirect). |
| github.com/dustin/go-humanize v1.1.0 | MIT | Dependency of modernc.org/sqlite (indirect). |
| github.com/mattn/go-isatty v0.0.24 | MIT | Dependency of modernc.org/sqlite (indirect). |
| github.com/ncruces/go-strftime v1.1.0 | MIT | Dependency of modernc.org/sqlite (indirect). |

## Third-party services

Online metadata providers (Audnexus, Audible catalogue, Google Books, Open Library, Apple iTunes Search)
are third-party services. Tendshelf contacts them only on an explicit user action, sends only a title,
author, ASIN or ISBN, and follows their published rate limits.
