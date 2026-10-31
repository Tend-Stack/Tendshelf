# Feature map

Phases are defined in [roadmap.md](roadmap.md). "ABS" is Audiobookshelf 2.37.2 (server source read on
2026-10-31: models, controllers and providers listed there); "Audible" is the Audible mobile app from
general knowledge. "Kindle" is Amazon's Kindle apps with Whispersync for Voice and Immersion Reading,
from general knowledge. Cells marked `?` are unverified guesses; check before quoting them publicly.

| Feature | ABS | Audible | Tendshelf phase |
| --- | --- | --- | --- |
| **Library** | | | |
| Folder scanning (Author/Series/Book, single file, numbered MP3 folder, multi-disc) | Yes | n/a | P1 |
| Rescan on demand; periodic rescan | Yes (also file watcher) | n/a | P1 (watcher later, needs a dependency) |
| Move/rename detection keeping progress | Yes (ino-based) | n/a | P1 (size + quick hash) |
| Copy-protected files refused and shown as such | Partial? (plays only decrypted) | n/a | P1 |
| Multiple libraries and folders | Yes | n/a | P1 |
| Standalone ebook library, comics | Yes (EPUB, PDF, MOBI, AZW3, CBR, CBZ) | No (Kindle is a separate app) | Not planned (ebooks only as a book's text edition, see Reading along) |
| Podcasts (subscribe, download episodes) | Yes | Yes (Audible originals) | Later |
| **Details and covers** | | | |
| Details from folder names, file tags, sidecars (`.opf`, `.nfo`, `desc.txt`, `reader.txt`, `metadata.json`) | Yes | n/a | P1 (names, tags), P2 (sidecars) |
| Field-by-field source shown and kept | No? | n/a | P2 |
| Online lookup: Audnexus/Audible, Google Books, Open Library, Apple Books | Yes (plus FantLab, MusicBrainz, AudiobookCovers) | n/a | P2 |
| Field-by-field compare and merge with an online match | Partial (choose fields on apply) | n/a | P2 |
| Custom metadata provider (HTTP spec) | Yes | n/a | Later |
| Covers: embedded, folder image, online, upload | Yes | Yes | P1 (embedded, folder), P2 (online, upload) |
| Write details into the file (stream copy, verified, with backup and Undo) | Partial (embed tool; backup option?) | n/a | P2 |
| Merge MP3 folder into one M4B | Yes | n/a | Later |
| Authors and narrators pages, with photo and bio | Authors yes, narrators partial | Yes | P1 (pages), P2 (photo, bio) |
| Series pages with order | Yes | Yes | P1 |
| Genres and tags | Yes | Yes (categories) | P1 |
| Search (title, author, narrator, series, description) | Yes | Yes | P1 (FTS5) |
| **Listening** | | | |
| Web player with chapters | Yes | Web: yes | P1 |
| Playback speed with pitch kept | Yes (0.5-10x?) | Yes (0.5-3.5x) | P1 (0.5-3.0x, 0.05 steps) |
| Sleep timer: minutes, end of chapter | Yes (mobile) | Yes | P1 (fade-out), P4 (shake to extend) |
| Skip back/forward, user interval | Yes | Yes | P1 |
| Automatic short rewind after a pause | No? | Partial? | P1 |
| Lock-screen and headset controls (Media Session) | Yes (mobile app) | Yes | P1 (PWA) |
| Chapter-relative progress and time left at this speed | Partial | Yes | P1 |
| Progress sync across devices | Yes | Yes (Whispersync) | P1 |
| "Continue where you were on the other device?" prompt | No? | Partial? | P1 |
| Bookmarks | Yes | Yes (clips) | P1 |
| Notes on a position | No? (bookmark titles only) | Yes (note on a clip) | P1 |
| Listening history and statistics | Yes | Yes (plus badges) | P4 (stats); badges not planned |
| Mark finished / not started; reading status | Yes | Yes | P1 |
| Cast to Chromecast / AirPlay | Chromecast (web) | Yes | Later (AirPlay works from Safari's own audio element) |
| Car mode / big-button screen | No? | Removed? | P4 |
| **Reading along** ([read-along.md](read-along.md)) | | Audible / Kindle | |
| Add an ebook to an audiobook (text edition); several editions per book | Partial: one primary ebook per book (`Book.ebookFile`), other files supplementary | Partial: only a purchased Kindle edition matched to the Audible title | R1 (ingest), R2 (pairing, editions) |
| Ebook formats | EPUB, PDF, MOBI, AZW3 (`globals.js`) | Kindle formats; EPUB, PDF, DOCX converted by Send to Kindle? | R1: EPUB 2/3, PDF (text layer, reflow or page view), TXT, Markdown, DOCX, HTML, FB2, DRM-free MOBI/AZW3 |
| Copy-protected ebooks refused with a plain message | No? (no protection check found in 2.37.2 source) | n/a (store DRM) | R1 |
| Automatic ebook-audiobook matching (folder, ISBN/ASIN, title + author), manual pairing | Partial (files in the same folder form one item) | Yes (by purchase) | R2 |
| Read-along: sentence highlighted while listening, gentle auto-scroll | No (no code found in 2.37.2) | Yes (Immersion Reading; granularity ?) | R4 |
| Word-level highlight | No | ? | R4 |
| Tap a sentence to play from there | No | ? | R4 |
| "Lost the place?" button back to the voice | No | ? | R4 |
| Focus line (one sentence at a time, large type) | No | No? | R4 |
| Alignment of the user's own files, computed locally | No | No (Amazon-matched titles only) | R3 |
| Text not in the recording shown as such (abridged, other edition) | No | ? | R3, R4 |
| Reader typography: fonts, size, spacing, margins, themes | Partial: sans/serif, font scale, line spacing, text stroke, dark/sepia/light (`Reader.vue`) | Yes (dyslexia font ?) | R4 (incl. dyslexia-friendly font, night and high contrast) |
| Highlights and notes shared between text and audio (text range and time range) | No (no highlight model) | Partial? (Kindle highlights and Audible clips are separate) | R4 |
| Switch formats without losing the place (one position) | No: `ebookLocation`/`ebookProgress` stored beside the audio position (`MediaProgress.js`) | Yes (Whispersync for Voice) | R5 |
| Offline read-along | n/a | Yes? | R5 (needs P4) |
| Guests read along only if the ebook is shared too | No? | n/a | R5 (needs P3) |
| EPUB3 Media Overlays import (use existing SMIL timings) | No (no SMIL code found) | No? | R6 |
| EPUB3 Media Overlays export of the alignment | No | No | R6 |
| **Collections** | | | |
| Shelves / collections (user) | Collections (library-wide) + playlists (user) | Collections | P1 (personal shelves), P3 (shared shelves) |
| Wish list | No | Yes | Not planned |
| **Users and sharing** | | | |
| Multi-user | Yes (root, admin, user, guest) | Household (Amazon) | P3 (owner, admin, member, guest) |
| Per-library access | Yes | n/a | P3 |
| Per-book access for a person | No? (tag-based filters) | Partial (share a title?) | P3 |
| Invitations by link or email, expiring | No (admin creates accounts) | n/a | P3 |
| Public share link for one item | Yes (media item share) | No | P3 (always expiring, optional password) |
| Listen-only guests (no download) | Partial (download permission) | n/a | P3 |
| API keys / device tokens | Yes | n/a | P3 (device tokens) |
| Passkeys | No? | n/a (Amazon) | Later |
| SSO / OpenID Connect | Yes | n/a | Later |
| **Offline and apps** | | | |
| Installable PWA | Partial? | n/a | P4 |
| Offline downloads | Yes (mobile apps) | Yes | P4 (PWA) |
| Native desktop app | No | Windows (retired?) | P5 (Wails v3) |
| Native mobile app | Yes (stalled) | Yes | Later (evaluate Wails v3 mobile; PWA first) |
| **Feeds and integrations** | | | |
| RSS/podcast feed of a book or series | Yes | No | Later |
| OPDS catalogue | No (not in server source) | No | Later |
| Backups of the database | Yes | n/a | P1 (pre-migration copies), P4 (scheduled) |
| Notifications (Apprise) | Yes | Push | Not planned |
| **Research (Tendshelf only)** | | | |
| Clip extraction and calibration measurements for TendVox (off by default, admin-only, fair-practice rules) | No | No | P6 |
| Reference-numbers export (`tendvox-reference-styles` v1) | No | No | P6 |
