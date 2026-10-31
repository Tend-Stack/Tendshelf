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
| (none yet; `internal/web/static/vendor/htmx.min.js` arrives with P0, licence to be read from the release) | | | |

## Go module dependencies

| Module | Licence | Why |
| --- | --- | --- |
| (none yet; standard library only) | | |

## Third-party services

Online metadata providers (Audnexus, Audible catalogue, Google Books, Open Library, Apple iTunes Search)
are third-party services. Tendshelf contacts them only on an explicit user action, sends only a title,
author, ASIN or ISBN, and follows their published rate limits.
