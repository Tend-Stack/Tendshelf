# Notices

Tendshelf is licensed AGPL-3.0-or-later. This file credits code, methods and assets that come from
other projects. Every port, vendored file or third-party dependency adds a row here in the same commit.

## Methods and code ported from other projects

| Tendshelf file | Source project and file(s) | Licence | How |
| --- | --- | --- | --- |
| `internal/media/ffmpeg.go` | Audiobookshelf `server/utils/ffmpegHelpers.js` (embedded cover extraction arguments) | GPL-3.0 | Method port, idiomatic Go |
| `internal/media/accent.go` | Björn Ottosson's OKLab reference, https://bottosson.github.io/posts/oklab/ (matrices and conversions) | Public domain / MIT | Formulas re-implemented in Go |
| `internal/media/probe.go` | Audiobookshelf `server/libs/nodeFfprobe/index.js` (ffprobe argument set), `server/utils/prober.js` (stream, tag and chapter reading) | GPL-3.0 | Method port, idiomatic Go |
| `internal/scan/group.go` | Audiobookshelf `server/utils/scandir.js` (`groupFileItemsIntoLibraryItemDirs`); TendVox Extractor `extractor_details.py` (`book_layout`, `is_parts_set`) | GPL-3.0 | Method port, idiomatic Go |
| `internal/scan/order.go` | Audiobookshelf `server/scanner/AudioFileScanner.js` (`runSmartTrackOrder`, `getTrackAndDiscNumberFromFilename`); TendVox Extractor `extractor_details.py` (`track_numbers`, `order_parts`, `natural_key`) | GPL-3.0 | Method port, idiomatic Go |
| `internal/scan/chapters.go` | Audiobookshelf `server/scanner/AudioFileScanner.js` (`getBookChaptersFromAudioFiles`) | GPL-3.0 | Method port, idiomatic Go |
| `internal/metadata/names.go` | Audiobookshelf `server/utils/parsers/parseNameString.js`, `parseSeriesString.js`; TendVox Extractor `extractor_details.py` (`parse_names`, `parse_series_string`) | GPL-3.0 | Method port (the Extractor's simplified name rules), idiomatic Go |
| `internal/metadata/sidecar.go` | TendVox Extractor `extractor_details.py` (`sidecars`, `parse_nfo`, `parse_opf`, `parse_metadata_json`, `local_details`); Audiobookshelf `server/utils/parsers/parseOpfMetadata.js`, `parseNfoMetadata.js` | GPL-3.0 | Method port, idiomatic Go |
| `internal/metadata/folder.go` | Audiobookshelf `server/utils/scandir.js` (`getBookDataFromDir`, `getSequence`, `getNarrator`, `getASIN`, `getPublishedYear`); TendVox Extractor `extractor_details.py` (`parse_folder_name`, `parse_book_name`, `clean_sequence`) | GPL-3.0 | Method port, idiomatic Go |
| `internal/metadata/tags.go` | TendVox Extractor `extractor_details.py` (`meta_from_tags`, `clean_text`, `clean_asin`, `clean_isbn`, `clean_year`); Audiobookshelf `server/scanner/AudioFileScanner.js` (author tag order) | GPL-3.0 | Method port, idiomatic Go |
| `internal/metadata/production.go` | TendVox Extractor `extractor_details.py` (`find_production`, `_FULL_CAST`, `_DRAMATIZED`) | GPL-3.0 | Method port, idiomatic Go |
| `internal/metadata/online/*.go` | TendVox Extractor `extractor_online.py`; Audiobookshelf `server/providers/Audnexus.js`, `Audible.js`, `GoogleBooks.js`, `iTunes.js` | GPL-3.0 | Method port, idiomatic Go |
| `internal/textmatch/textmatch.go` (`Ratio`) | Python `difflib.SequenceMatcher` | PSF-2.0 | Algorithm re-implemented in Go |

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
| `internal/web/static/fonts/OpenDyslexic-Regular.woff2` (SHA-256 `f007004af3cda5d8076e57c943f8cc8d00a0da25988b1ae1048683d60e7cac1a`; 115,280 B) | OpenDyslexic (antijingoist), release `v0.91.12` (tag commit `449a41bbc25f7363c540ad112dfcef2f7b340c0a`), asset `opendyslexic-0.910.12-rc2-2019.10.17.zip` (SHA-256 `b92d7fcb9409f2bcfd23b65ac71647256eb49c429f4fbb1cc870381fc93eb486`) from `https://github.com/antijingoist/opendyslexic/releases/download/v0.91.12/opendyslexic-0.910.12-rc2-2019.10.17.zip`, file `OpenDyslexic-Regular.woff2`; fetched once by the maintainer, never by the app | 0.910.12 rc2 | OFL-1.1 (`fonts/OFL-OpenDyslexic.txt`, SHA-256 `caafcccfb70fc72458fcbda812ec8f0a06cb300cbabb87eabbb30b946124394b`, the `OFL.txt` of the same tag commit; the release zip carries no licence file) |
| `internal/web/static/fonts/OpenDyslexic-Bold.woff2` (SHA-256 `dd9fa9c7991113b0dddefe9506a30ad26b48e302b7a8fb91719a4726e8fde85b`; 120,356 B) | OpenDyslexic, same release and zip, `OpenDyslexic-Bold.woff2` | 0.910.12 rc2 | OFL-1.1 |
| `internal/web/static/fonts/OpenDyslexic-Italic.woff2` (SHA-256 `eb6a1bacf7e7c87a08116af4cd00a82064fbe61647b9a0d4d70a339890268f88`; 112,360 B) | OpenDyslexic, same release and zip, `OpenDyslexic-Italic.woff2` | 0.910.12 rc2 | OFL-1.1 |
| `internal/web/static/img/tendshelf-{mark-ember,mark-cream,app-icon}.svg` | Tendshelf design handoff (`docs/design/tendshelf-handoff/`), copied unchanged | n/a | AGPL-3.0-or-later (project asset) |
| `internal/web/static/css/tokens.css`, `css/bundle.css` | Tendshelf design handoff, copied; changes: one `--font-mono` line, the P0b app-shell section | n/a | AGPL-3.0-or-later (project asset) |

## Go module dependencies

| Module | Licence | Why |
| --- | --- | --- |
| github.com/a-h/templ v0.3.1100 | MIT | templ runtime (`internal/web`); the CLI runs through `go tool`, not linked into the binary. |
| modernc.org/sqlite v1.61.1 | BSD-3-Clause | Pure-Go SQLite driver (`internal/db` only). The SQLite C source it carries is public domain. |
| github.com/wailsapp/wails/v3 v3.0.0 | MIT | The desktop window (`desktop/` module only, decision 0020; needs CGO, GTK 4 and WebKitGTK 6). The root module and the server binary never import it. Its own dependencies (adrg/xdg, godbus/dbus, google/uuid and others) are indirect and desktop-only. |
| golang.org/x/crypto v0.58.0 | BSD-3-Clause | argon2id password hashing (`internal/users` only). |
| golang.org/x/net v0.61.0 | BSD-3-Clause | HTML5 tokenizer (`html`) and charset detection (`html/charset`) for reading EPUB, HTML and MOBI text (`internal/ebook` only). |
| github.com/ledongthuc/pdf v0.0.0-20261020125853-5e46805f718e | BSD-3-Clause (Copyright The Go Authors, from the Go PDF reader) | PDF text layer: pure-Go reader with per-glyph position and font size, used for reading PDF ebooks (`internal/ebook/pdf.go` only). Encrypted PDFs are refused before it reads any text. |
| golang.org/x/text v0.43.0 | BSD-3-Clause | Unicode NFC normalisation (`unicode/norm`) and legacy charset decoding (`encoding/...`: Windows-1251 FB2, Windows-1252 text, UTF-16) in `internal/ebook`. |
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
