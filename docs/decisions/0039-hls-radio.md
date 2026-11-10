# 0039 HLS radio: the server turns an HLS station into one continuous stream
Status: proposed (2026-11-10). Plan: `docs/plans/hls-radio.md`. Parents: [0038](0038-podcasts.md) points 4, 5 and 10 (guarded
client, playback through this server, radio), AGENTS.md "Stack constraints", "Audio files are the user's" (never work around DRM) and
"Security and privacy". **Amends** the "Out of scope" line of `docs/plans/podcasts.md` that left HLS radio out ("needs a segment
proxy"). Changes no AGENTS.md rule.

## Context
R1 relays one continuous Icecast or Shoutcast stream (`GET /radio/live/{id}`, `GET /api/v1/radio/live/{id}`): one `podnet.OpenLive`
request per listener, ICY blocks cut out, the answer only ever a Content-Type from `radio.streamTypes`. A station whose only stream is
HLS (`.m3u8`, `application/vnd.apple.mpegurl`, or an `.m3u` with `#EXT-X-` tags) is refused at add time with `radio.ErrHLS`. Many
public broadcasters (BBC, Radio France, NPR member stations, iHeart) now publish HLS only, or HLS first. An HLS stream is a playlist
that is fetched again every few seconds, each time naming short audio files (segments): MPEG-TS, packed audio (`.aac`, `.mp3` with an
ID3 time tag in front) or fragmented MP4 (CMAF, with an `EXT-X-MAP` init file).

Hard limits: one pure-Go binary with CGO off; no new Go module without approval; ffmpeg may read only `file` and `pipe`; no client-side
framework; the browser and the Android app talk only to the Tendshelf server; every outbound connection goes through the guarded
client in `internal/metadata/online/podnet`, checked after DNS.

## Decision
1. **The server reads HLS and sends one continuous stream (option a).** A new package `internal/radio/hls` parses the playlists, pulls
   the audio frames out of each segment and writes them, in order, as one ADTS AAC stream (`audio/aac`) or one MPEG audio stream
   (`audio/mpeg`). The existing relay serves it with the same headers, limits and slots. Browsers play ADTS AAC and MP3 from a plain
   `<audio src>`, and Android's Media3 progressive player already plays the AAC Icecast stations the relay serves today, so **no client
   changes**: no hls.js, no `media3-exoplayer-hls`, no service-worker change (`/radio/...` stays uncached).
2. **Every fetch is a guarded fetch.** Two new `podnet` methods, `Playlist` (256 KiB, 10 s) and `Segment` (4 MiB, a deadline of twice
   the target duration, between 10 s and 30 s), go through the existing `do`: URL check, dial-time address check after DNS, at most 5
   redirects each re-checked, no cookies, no Referer, fixed User-Agent. Every variant, segment and init address is resolved against the
   final address of the playlist that named it (after redirects) and checked again on its own; a `data:`, `file:` or non-http(s)
   address is refused by `checkURL` as today. ffmpeg is not involved, so it never touches the network.
3. **Master playlist: one audio variant per session.** Among `EXT-X-STREAM-INF` variants (and `EXT-X-MEDIA TYPE=AUDIO` renditions with a
   `URI`), only audio ones count: `CODECS` naming only `mp4a.40.2`, `mp4a.40.5`, `mp4a.40.29`, `mp4a.40.34` or `mp4a.6b`/`mp3`, or no
   `CODECS`, no `RESOLUTION` and `BANDWIDTH` at most 512 000. Pick the **highest `BANDWIDTH` at or below 320 000**, otherwise the lowest
   audio one. Reason: radio audio variants are 32 to 320 kb/s, the server's own bandwidth is not the constraint, and the listener hears
   the difference; no adaptive switching (one variant for the whole session, re-chosen at the next connection). The stored address is
   the **master** address, so rotating variant tokens keep working. A master naming a master is refused (one hop).
4. **Video is refused, not stripped.** A variant whose `CODECS` names video or that has a `RESOLUTION` is never picked; a stream with
   only such variants is refused at add time. Reason: fetching 2 to 8 Mb/s of video per listener to throw it away is waste and a cheap
   amplification lever. A TS segment that still carries a video track has it ignored by the demuxer, and the 4 MiB segment cap ends a
   session that is really video.
5. **Encrypted streams are refused, all of them.** Any `EXT-X-KEY` or `EXT-X-SESSION-KEY` with a method other than `NONE`
   (`AES-128`, `SAMPLE-AES`, `SAMPLE-AES-CTR`, any `KEYFORMAT` such as FairPlay or Widevine) is refused at add time, and a live session
   whose playlist starts naming a key ends. SAMPLE-AES and key systems are DRM. Plain AES-128 is technically only a published key, but
   Tendshelf cannot tell a transport cipher from an access control (key servers behind tokens are common), and "never decoded, never
   worked around" is the house rule, so it is treated as copy-protected. Words: "This station's stream is locked, so Tendshelf will not
   play it." Guess, unverified: public free-to-air radio HLS is almost never encrypted.
6. **Live reload follows RFC 8216 6.3.4.** Start three segments from the live end. Reload after `EXT-X-TARGETDURATION` when the last
   reload brought new segments, after half of it when not. Segments are tracked by `EXT-X-MEDIA-SEQUENCE` + index: only numbers above
   the last one written are fetched. A sequence that went backwards, or a gap so large nothing known is left, restarts at three from the
   end. `EXT-X-DISCONTINUITY` is allowed while the codec, sample rate and channel count stay the same; a change of any of them ends the
   response (the players' existing live retry reconnects). `EXT-X-ENDLIST` ends the response after its last segment. Target duration
   must be 1 to 30 s. Low-latency parts (`EXT-X-PART`) are ignored; whole segments are played.
7. **Containers.** MPEG-TS (stream types 0x0F ADTS AAC, 0x03 and 0x04 MPEG audio, 0x15 timed ID3), packed audio (ID3 header skipped,
   then ADTS or MPEG audio frames), fragmented MP4 (`moov/trak/mdia/minf/stbl/stsd/mp4a/esds` for the AudioSpecificConfig,
   `moof/traf/tfhd/trun` sizes and `mdat`, each AAC access unit wrapped in a 7-byte ADTS header; HE-AAC signalled explicitly is written
   with its core profile and sample rate, implicit SBR). Only frames that pass a sync and length check are written, so no other byte
   the origin sent reaches the client. LATM AAC, AC-3, E-AC-3, Opus, FLAC and `EXT-X-BYTERANGE` playlists are refused: "That station
   uses a sound format Tendshelf can't play."
8. **One upstream per listener, as R1.** Each listener's session fetches its own playlists and segments. A shared fetch with fan-out
   was rejected for now: it needs a buffer, slow-reader handling and a lifecycle independent of any request, for a server with a
   handful of people. The existing caps hold: 1 live station per person, 16 upstream streams per server shared with podcast audio,
   12 h per connection, the stop within 3 s when radio is turned off. Three failed segment fetches in a row, or three failed playlist
   reloads in a row, end the response.
9. **Titles on air.** The `EXTINF` title (text after the comma; `title="..."` and `artist="..."` attributes when present) and ID3
   `TIT2` and `TPE1` frames in timed ID3 or packed-audio headers feed `Service.SetTitle`, cleaned and cut by `CleanTitle` as ICY
   titles are. Only text frames in Latin-1, UTF-16 with BOM and UTF-8 are read; every other frame is skipped.
10. **Detection at add time.** `Probe` takes the HLS path when the address ends in `.m3u8`, the answer's type is
    `application/vnd.apple.mpegurl` or `application/x-mpegurl`, or a `.m3u` playlist carries `#EXT-X-` tags; the body must start with
    `#EXTM3U`. It fetches the media playlist and its newest segment and demuxes one frame inside the existing 10 s probe. The station row
    gains `format` (`icy` or `hls`, migration 0042), so playing never sniffs. New plain-word refusals sit beside `ErrHLS` and follow its
    style; `ErrHLS` stays for HLS forms still not handled.

## Rejected
- **(b) Proxy the playlist and let clients play HLS.** Firefox has no native HLS; Chrome's native HLS is recent and not on every
  desktop (guess, unverified); so the web player would need hls.js (a vendored framework of hundreds of KiB on MSE, against the
  "no framework" rule) and Android would need `androidx.media3:media3-exoplayer-hls` (a new dependency). The server would still fetch
  every segment through a rewriting proxy, so it saves only the demux, and it opens many new `/radio/` addresses and states to guard.
- **(c) Pipe fetched segments into ffmpeg over stdin.** Allowed by the protocol rule, and it would cover every codec. Rejected: one
  ffmpeg process per listener, a stream that dies on each discontinuity, ffmpeg becomes required for radio, the Go side still needs the
  playlist parser and fetch loop, and ffmpeg's TS and MP4 demuxers would parse hostile internet bytes, a larger attack surface than a
  few hundred lines of bounded Go. Kept as the fallback if a needed container proves too hard in Go.
- **Decrypting AES-128.** See point 5.
- **Stripping video from muxed variants.** See point 4.

## Consequences
No new module, no rule change, no client change. One migration (0042). `internal/radio/hls` creates no `http.Client`; it takes a
`Fetcher` that `podnet.Client` satisfies, so the outbound rule still names only `podnet`. Opus-in-fMP4 and AC-3 stations stay refused.
