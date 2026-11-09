# Next stage: the best listening and reading (plan, 2026-11-05)

Operator's goal: "lets make the best audiobooks listening and best books readers experience available. improve on
server, desktop app, etc. and when ready, work on mobile app too, starting with android that can actually be built on
linux." The Android half is [android.md](android.md) and [decision 0024](../decisions/0024-android-app.md).

Everything below was checked against the code at `61fab59`: a gap is listed only after a search found nothing. Sizes: **S**
is a small package (under about 100 tool calls), **M** is one full package (about 250), **L** is two or more. The evidence is uneven. Store pages and
F-Droid listings are firm; what people "love" comes from forum threads and is anecdote. "Judgement" marks an item with
no source behind it.

## The rule for the split

Do before Android whatever changes a data shape or the API the app will consume (so the app is built once), and whatever
helps every person on every device. Do after Android whatever only a native program can do well, such as background work
and skip silence. The Android work starts when the operator's open checks in
[manual-checks.md](../manual-checks.md) are run, because the shell inherits every page.

## Already built, so not proposed

Checked in the code: smart rewind (fixed 5 s after 5 minutes, `player.js:437`), sleep timer with fade and shake
(`player-full.js`), lock-screen and headset chapter skip (`player.js:853`), "you were at ... continue there?"
(`copy.go:280`), queue and Play next (`routes_api_queue.go`), next in series, Eyes-free mode, stats, notes on bookmarks,
offline download with "Remove finished books", waveforms, per-person speed, and bookmark, highlight and word exports.

## Do before Android

| # | Package | Size | Why |
| --- | --- | --- | --- |
| 1 | **L1 Per-book speed.** A table for a person's settings on one book (speed first, sound mode next), falling back to the usual speed. | S | Speed today is one value per person (`playback.speed`). A slow narrator and a fast one fight each other. Prologue lists per-book settings. |
| 2 | **L2 Rewind that fits the pause, and "Jumped too far?"** Rewind grows with the length of the break; one tap undoes the last seek. | S | Both are small and felt daily. Voice lists both. Today's rewind is one fixed length. |
| 3 | **S1 Watch folders and a scheduled scan.** fsnotify (approved) with a debounce; a daily scan as the backstop for network drives, where events do not arrive. | M | Scans run only from setup and the admin buttons (`startScan` callers), so job 1 of the vision, "I drop files in and they appear", is not true yet. |
| 4 | **S2 Whole backups to a folder the owner chooses.** Add covers, uploaded ebooks and dictionaries, keep N copies, offer restore. A mounted NAS or rclone folder is the off-site path. | M | Backups hold the database only (`db.WriteCopy`), so uploaded ebooks live in the data folder with no copy. That is a real data-loss gap. No cloud clients: only `internal/metadata/online` may make requests. |
| 5 | **L3 Chapter editor.** Rename, add, delete and shift chapters per book, kept across rescans; **L3b** look up chapters from Audnexus; both feed "Write details into the file". | L (2) | There is no chapter editing and no chapter lookup. A folder of MP3s has file names as chapters, and every chapter feature rests on these rows. Judgement, backed by the Audiobookshelf chapter editor. |
| 6 | **L4 Sound modes.** Finish [decision 0022](../decisions/0022-sound-modes.md): Voice clarity, Even volume, Night, mono. Even volume is the volume boost people ask for. | M | Designed and approved; S0 is merged and the phone spike decides what ships. Voice, BookPlayer and Prologue all have boost or voice filters. Needs the operator's phones. |
| 7 | **S3 Library health.** One admin page: missing, refused, failed to read, no chapters, no cover, no lookup match, with a fix link. | M | The columns exist (`missing_since`, `refused_reason`, probe errors, `book_lookups`), but there is no single view. An owner cannot see what is wrong. |
| 8 | **S4 Duplicates.** Find the same audio (size and quick hash) and the same title, author and narrator; offer a merge that keeps progress. Never touches a file. | M | Only ebook merges exist (`internal/ebook/merge.go`). Library growth makes duplicates common (judgement). |
| 9 | **S5 App keys and OPDS.** A per-device key for clients that only speak Basic auth, then an OPDS 1.2 catalogue of ebooks through `share.Can`. | M | Readers on e-ink want to fetch books. Audiobookshelf users run community OPDS servers to get this. The key is the base for S6 and means never handing out the login password. |
| 10 | **S6 KOReader progress sync.** The kosync endpoints with an app key; store KOReader's partial MD5 per ebook file. | M | The Audiobookshelf bridge is actively developed, which shows the demand. Limit: positions convert only by percentage, so a sync lands near the place, not exactly on it. |
| 11 | **L5 Clips.** A bookmark may have an end time and a kind (favourite, quote, note, revisit), with "play this clip". No audio export yet. | M | Audible has clips and Voice has bookmark kinds. Do it before Android so `/bookmarks` changes once. |
| 12 | **S7 OpenID Connect.** Approved module. Sign-in on the existing page. | M | Self-hosters with Authelia or Authentik expect it (judgement), and the Android sign-in must support it from the start. |
| 13 | **L6 Listening timeline and night sleep.** "Where was I?" from `listening_sessions`; start the sleep timer on its own during chosen hours. | S | Vision job 3 ("where I really stopped hearing it"). The data exists. |
| 14 | **R1 All my highlights.** One page of highlights and notes across books, with search and export. | S | Exports exist per book only (`/books/{id}/read/marks.*`); there is no page across books. |
| 15 | **S8 Docker image and compose example.** Binary plus ffmpeg; nothing else changes. | S | `docs/deploy.md` covers systemd only. Most self-hosters start from compose (judgement). Optional, so the binary stays the product. |
| 16 | **S9 Activity page for the owner.** Read `audit_log` in plain words, filter by person. | S | The table is written and nothing shows it. |
| 17 | **A1 Server for the app** (see [android.md](android.md)). Settings API, continue-listening list, next-book answer, token handoff. | M | The first Android-facing change, last in this list so it carries the final shapes from L1, L3 and L5. |

## After Android

| # | Package | Size | Why |
| --- | --- | --- | --- |
| 18 | **A0, A2-A10 The Android app** (see [android.md](android.md)). | L (10) | Background audio with the screen off, Android Auto, real downloads. Nothing on the web can do these reliably. |
| 19 | **L7 Skip silence.** On Android through Media3's built-in `setSkipSilenceEnabled`; decide about the web afterwards. | S on Android | Smart AudioBook Player and Voice have it. On the web it needs custom sample code, which decision 0022 ruled out, and a dropped sound on a locked phone is the worst failure. |
| 20 | **L8 Download the next book on its own.** Next in queue or series, on Wi-Fi only (decision 0035; removing finished books is a separate, later approval). | M | ShelfPlayer calls these convenience downloads. On the web it runs only while a page is open and cannot see Wi-Fi, so it belongs in the app. |
| 21 | **L9 Home-screen widget.** Cover, play, skip, chapter. | M | Voice and Prologue have widgets. Native only. |
| 22 | **R2 Listen to a text book with the phone's voice.** Android's on-device TextToSpeech, reading from the reading place. | M | Gives every ebook a listen button, and it fits one book, two formats. On the web, only voices marked local may be used: a browser's online voices would send the book's text to a third party. |
| 23 | **R3 Reader feel on Android.** Volume keys turn pages, keep-awake, brightness swipe, offline reading in the app. | M | Reader work that needs the shell. |
| 24 | **S10 Passkeys.** Approved module. | M | After OIDC, because a WebView needs extra native setup for passkeys; check in A2. |
| 25 | **R4 Right-to-left and vertical text.** | L | Readers of Arabic, Hebrew and Japanese cannot use the reader well. Paginator work, later because of its size. |
| 26 | **L10 Download a series or a shelf in one tap.** | S | Small once the app's download queue exists. |
| 27 | **L11 Speed up to 3.5×.** | S | Audible's ceiling. Few people need it (judgement). |
| 28 | **S11 Podcasts, feeds of books, MP3-to-M4B merge, custom metadata providers.** On the roadmap's Later list. | L each | Each is its own project. A merge also needs a decision, because it writes audio files. |

## Not to do

- **Prometheus or `/metrics`.** Not built (no match in the code). It would be a pull endpoint, so it is not telemetry in the
  strict sense, but it is a surface nobody on the vision's list needs. `/healthz` stays, and S3 and S9 show the owner what
  matters. Keep the "no".
- **Chromecast.** Needs Google's sender library, proprietary on Android and a third-party script on the web, which breaks
  the rules. AirPlay works from Safari's own audio element.
- **CarPlay.** Needs an iOS app. Android Auto comes first; CarPlay waits for an iOS decision.
- **Cloud backup clients and AI features** (Audible's Character Guide and the like). They need third parties or models
  Tendshelf will not download.

## Desktop

Nothing here is desktop-only. The open items are the operator's checks in M6 and macOS and Windows, which are not verified.
Every package above reaches the desktop through the same pages. Wails stays at v3.0.0 until a package needs more.

## Sources

Listening: [Voice on F-Droid](https://f-droid.org/packages/de.ph1b.audiobook/), [Prologue](https://apps.apple.com/app/id1459223267),
[BookPlayer](https://apps.apple.com/hu/app/bookplayer/id1138219998), [Smart AudioBook Player notes](https://www.apkmirror.com/apk/alex-kravchenko/smart-audiobook-player/),
[Audible listing](https://apps.apple.com/tr/app/id379693831), [abs_flutter](https://github.com/Vito0912/abs_flutter),
[ShelfPlayer listing](https://apps.apple.com/app/id6475221163), [audiobookshelf-app #381](https://github.com/advplyr/audiobookshelf-app/issues/381).
Plappa and Libby and Libro.fm: no usable feature source was found, so nothing above rests on them.
Server: [Audiobookshelf community projects](https://audiobookshelf.org/docs/documentation/community/community-projects),
[ABS KoSync Bridge changelog](https://cporcellijr.github.io/abs-kosync-bridge/changelog/),
[koreader-sync-server](https://github.com/koreader/koreader-sync-server), [kosync-dotnet](https://github.com/jberlyn/kosync-dotnet),
[the partial MD5 note](https://www.mobileread.com/forums/showthread.php?p=3844602).
Reader: [docs/research/ebook-reader-features.md](../research/ebook-reader-features.md).
