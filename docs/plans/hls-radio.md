# Plan: HLS radio (decision 0039)
Builder's contract. Reused: `podnet.Client.do` (all guards), `radio.Service.Acquire`/`SetTitle`/`CleanTitle`/`SetChecked`/`NotePlay`,
`getRadioLive` headers and `relayCopy`, `stopWhenRadioOff`, the `radio_test.go` fake publisher (`f.pub.URL(host, path)`), `openUpTo`
migration tests. Extended: `radio_stations` (one column), `Probe`, `getRadioLive`. Replaced: the three `ErrHLS` refusals in `Probe`.
No client, Android, service-worker, template or CSS change.

## 1. Invariants
1. Every playlist, init and segment request goes through `podnet` (`do`: URL check, dial-time address check, 5 re-checked redirects,
   no cookies, fixed User-Agent). `internal/radio/hls` never imports `net/http` client code and never creates an `http.Client`.
2. Relative URIs resolve against the **final** URL of the playlist that named them; each resolved URL is checked on its own request.
3. Only bytes of validated audio frames (ADTS sync + length, MPEG audio sync + length) reach the client; the answer type is
   `audio/aac` or `audio/mpeg` only, with the existing nosniff, sandbox CSP and `no-store` headers.
4. Any `EXT-X-KEY`/`EXT-X-SESSION-KEY` with `METHOD` other than `NONE` is refused at add and ends a live session. Nothing is decrypted.
5. Video variants are never picked; ffmpeg is never run for radio.
6. All bounds hold: playlist 256 KiB, segment and init 4 MiB (init also 1 MiB in `hls`), 2000 segments per playlist, line 8 KiB,
   target duration 1-30 s, 3 consecutive failures end a session, 12 h session, existing slots (1 per person, 16 per server).
7. One codec, sample rate and channel count per response; a change ends the response, never mixes.
8. Off means off: `stopWhenRadioOff` cancels the HLS session's context like the ICY one.

## 2. Decisions (reasons in 0039)
- Server demux to one ADTS/MPEG stream: clients unchanged, no new dependency.
- Store the master URL and a `format` column: tokens rotate; the relay never sniffs.
- Variant: highest audio `BANDWIDTH` <= 320000, else lowest audio one; no switching inside a session.
- Per-listener upstream: simplest, existing caps bound it.
- All encryption refused: house DRM rule; cannot tell transport cipher from access control.

## 3. Names
Package `internal/radio/hls` (files below). Exported:
```go
type Variant struct{ URI string; Bandwidth int; Codecs []string; Resolution string }
type Master struct{ Variants []Variant }            // EXT-X-MEDIA TYPE=AUDIO with URI is appended as a Variant (Codecs from its group's STREAM-INF)
type Segment struct{ Seq int64; URI string; Duration time.Duration; Title string; Discontinuity bool; Map string }
type Media struct{ Target time.Duration; Sequence int64; Segments []Segment; EndList bool }
func Parse(body []byte, base *url.URL) (*Master, *Media, error) // exactly one non-nil; URIs absolute
func ChooseVariant(m *Master) (Variant, error)                   // ErrVideo when no audio variant
type Format struct{ Codec string; SampleRate, Channels int }      // Codec "aac" or "mp3"
func (f Format) ContentType() string                             // "audio/aac" | "audio/mpeg"
func Demux(seg, init []byte) (frames []byte, f Format, titles []string, err error) // sniffs TS / packed / fMP4
type Fetcher interface {
    Playlist(ctx context.Context, rawURL string) (body []byte, finalURL string, err error)
    Segment(ctx context.Context, rawURL string, timeout time.Duration) ([]byte, error)
}
type Options struct{ OnTitle func(string); Sleep func(context.Context, time.Duration) error } // nil Sleep = timer
func Open(ctx context.Context, f Fetcher, playlistURL string, o Options) (*Session, error) // resolves variant, fetches + demuxes first segment
func (s *Session) Format() Format
func (s *Session) Read(p []byte) (int, error) // io.EOF after ENDLIST; ErrChanged on format change; ctx error on cancel
func (s *Session) Close() error
var ErrNotPlaylist, ErrLocked, ErrVideo, ErrCodec, ErrUnsupported, ErrChanged, ErrTooLarge error // errors.New("hls: ...")
const MaxSegment = 4 << 20; MaxInit = 1 << 20; MaxSegments = 2000; StartBack = 3; MaxFailures = 3
```
Parse errors: no `#EXTM3U` first line (BOM allowed) -> `ErrNotPlaylist`; key method not NONE -> `ErrLocked`; `EXT-X-BYTERANGE`,
master naming master, target outside 1-30 s, missing `EXT-X-TARGETDURATION` -> `ErrUnsupported`; > 2000 segments or line > 8 KiB ->
`ErrTooLarge`. Unknown tags ignored. `EXT-X-PART`/`EXT-X-PRELOAD-HINT` ignored. Audio codecs: `mp4a.40.2`, `mp4a.40.5`, `mp4a.40.29`,
`mp4a.40.34`, `mp4a.6b`, `mp3` (case-insensitive); any other entry (`avc1`, `hvc1`, `ac-3`, `ec-3`, `opus`, `flac`) makes the variant
non-audio when it is video, `ErrCodec` when all audio variants are only unsupported audio.

`podnet` (new, `internal/metadata/online/podnet/hls.go`): `Playlist` (Accept `application/vnd.apple.mpegurl, application/x-mpegurl,
audio/mpegurl;q=0.9, */*;q=0.5`, `statusErr`, `readCapped` 256 KiB, 10 s, `finalURL = resp.Request.URL.String()`), `Segment`
(Accept `*/*`, `Accept-Encoding: identity`, `readCapped` 4 MiB, the given timeout clamped 10-30 s). Constants `playlistMax`,
`playlistTimeout`, `segmentMax`. Errors are `*podnet.Error` as elsewhere.

`radio` (in `service.go`): `Probed.Format string`; new refusals, exact words:
- `ErrHLSLocked`  "This station's stream is locked, so Tendshelf will not play it."
- `ErrHLSVideo`   "That stream sends video, and Tendshelf plays radio as sound only."
- `ErrHLSCodec`   "That station uses a sound format Tendshelf can't play."
- `ErrHLSRecording` "That address is a recording, not a live station."
- `ErrHLS` keeps its text; used for `hls.ErrUnsupported`, `ErrNotPlaylist` after an HLS signal, `ErrTooLarge`.
`func (s *Service) probeHLS(ctx context.Context, raw string) (Probed, error)`; `func hlsRefusal(err error) error` maps hls errors.

Web (`routes_radio.go`): `func (s *server) relayHLS(ctx context.Context, w http.ResponseWriter, r *http.Request, st radio.Station)`;
`func setLiveHeaders(h http.Header, mt string)` shared by both paths.

## 4. Data
Migration `internal/db/migrations/0042_radio_hls.sql`:
`ALTER TABLE radio_stations ADD COLUMN format TEXT NOT NULL DEFAULT 'icy' CHECK (format IN ('icy','hls'));`
`internal/db/db.go` `SchemaVersion = 42`. `radio.Station.Format`, `radio.Input.Format` (empty means `icy`), `stationCols` and
`scanStation` add `s.format`, `Save` inserts it. No endpoint or API JSON change (`apiRadioStation` unchanged).

## 5. Steps
### H1 Go parsers, `internal/radio/hls` only (~200 calls)
1. `playlist.go`: `Parse`, `ChooseVariant`, attribute-list reader (quoted strings, commas inside quotes), EXTINF title rule: text after
   the first comma; if it holds `title="..."`, take it, prefixed by `artist="..."` + " - " when present.
2. `adts.go`: `scanADTS(b) (frames []byte, f Format, err)` (sync 0xFFF, layer 0, length 7-8191, drops a trailing partial frame,
   resyncs over junk at most 4 KiB once, else `ErrCodec`); `adtsHeader(profile, srIndex, chCfg, payloadLen int) [7]byte`.
3. `mpeg.go`: `scanMPEG(b)` for MPEG-1/2 Layer III frames (frame length from bitrate, sample rate, padding; free format refused).
4. `id3.go`: `id3Len(b) int` (syncsafe size, footer flag), `id3Title(tag []byte) string` (v2.3/v2.4 `TIT2`, `TPE1`; encodings 0, 1, 3;
   joins "artist - title"; stops at tag end; never panics).
5. `ts.go`: `demuxTS(seg)`: 188-byte packets, sync 0x47 or `ErrUnsupported`; PAT -> PMT -> first audio PID (0x0F -> scanADTS,
   0x03/0x04 -> scanMPEG, 0x11 -> `ErrCodec`), 0x15 PID -> ID3 titles; PES reassembly with adaptation fields; other PIDs ignored.
6. `fmp4.go`: box reader (size 0/1/64-bit, nesting depth 8, bounds-checked); `parseInit(init) (ascCfg, error)` from `esds`
   DecoderSpecificInfo (AOT 5/29: core AOT and core sample rate; objectTypeIndication 0x6B -> mp3); `demuxFMP4(seg, cfg)` from
   `tfhd` default size, `trun` sample sizes, `mdat` (data offset honoured), each AAC sample gets `adtsHeader`.
7. `packed.go` + `demux.go`: `Demux` sniff order: TS (0x47 at 0 and 188), ID3 or ADTS/MPEG sync -> packed, else fMP4 (needs init or
   `ErrUnsupported`). Packed: skip ID3 (collect its title), then scanADTS or scanMPEG.
8. Tests (`*_test.go`, synthetic bytes built by helpers in `synth_test.go`: `synthADTS`, `synthMPEG`, `synthTS`, `synthID3`,
   `synthInit`, `synthFragment`): table-driven `TestParse` (master, media, relative URIs, BOM, every error above, attribute quoting),
   `TestChooseVariant` (64k/128k/320k/1M audio -> 320k; only 512k audio -> 512k; video only -> ErrVideo; ac-3 only -> ErrCodec; no
   CODECS + RESOLUTION -> skipped), `TestDemuxTS` (AAC, MP3, with a video PID, ID3 PID title, split PES, bad sync), `TestDemuxPacked`,
   `TestDemuxFMP4` (LC and HE-AAC v1 ASC give the right ADTS header bytes), `TestID3Title` (encodings, truncated tag), and a
   `FuzzDemux` + `FuzzParse` seed corpus that must never panic (run 30 s locally, not in CI).
### H2 Go fetch and session (~180 calls)
9. `podnet/hls.go` + `hls_test.go`: `Playlist`, `Segment`; tests reuse the podnet test dialler: private, loopback, link-local, CGNAT and
   NAT64 targets refused; a redirect to `127.0.0.1` refused; over-cap playlist and segment give `too_large`; `finalURL` after a redirect.
10. `hls/live.go`: `Open` (playlist -> `ChooseVariant` -> media playlist; `ErrLocked` etc. pass through; start index
    `max(0, len-StartBack)`; init fetched once per distinct `Map`, cap `MaxInit`), `Read` (drains buffered frames; on empty: next
    segment with `Seq > last`; none -> `Sleep` Target (half when the last reload brought nothing) then reload; newest seq < last ->
    restart at `len-StartBack`; Format change -> `ErrChanged`; `EndList` exhausted -> `io.EOF`; titles deduped into `OnTitle`;
    3 consecutive segment or reload failures -> last error). Segment timeout = 2 x Target.
11. `hls/live_test.go` with a fake `Fetcher` (map of URL -> responses per call): live window advance, no-new-segment half wait (fake
    Sleep records durations), sequence reset, gap, discontinuity same format passes, format change ends, ENDLIST EOF, key appearing
    mid-session -> `ErrLocked`, 3 failures end, ctx cancel stops a sleeping Read within the fake.
### H3 Go wiring and docs (~150 calls)
12. Migration step 4 + `internal/db/migration_radio_hls_test.go` (`FromPrevious`: v41 row survives with `format='icy'`;
    `FromEmpty`: `'dash'` rejected by CHECK, `'hls'` accepted).
13. `radio/store.go`: Format through Save, Get, Favourites, Recent, Kept.
14. `radio/service.go`: `Probe` calls `probeHLS(ctx, norm)` where it returned `ErrHLS` (m3u8 path, HLS type, HLS inside `.m3u`, using
    that `.m3u`'s address); `probeHLS` = `hls.Open` + `Close` under the same 10 s context, refuses `EndList` with `ErrHLSRecording`,
    returns `Probed{StreamURL: norm, Codec, Format: "hls"}`. `routes_radio.go addStation` copies `probed.Format` into `in.Format`.
15. `routes_radio.go`: `getRadioLive` branches on `st.Format == "hls"` after `Acquire`, the session context and `stopWhenRadioOff`;
    `relayHLS`: `hls.Open(ctx, s.radio.Net, st.StreamURL, {OnTitle: SetTitle})`; error -> `SetChecked(id, "", code)` (podnet code, else
    `bad_answer`) and 502 `radioLiveUnavailable`; success -> `SetChecked(codec)`, `NotePlay` (not for HEAD), `setLiveHeaders`, 200,
    `relayCopy(w, sess, true)`, `Close`.
16. Tests in `radio_test.go` and `internal/web/radio_test.go` (sibling pattern; forbidden roles in `routes_matrix_radio_test.go`) with a fake HLS origin on the fake publisher:
    the three old `ErrHLS` rows now add a station; refusals: locked (AES-128, SAMPLE-AES), video only, ac-3 only, ENDLIST, byterange;
    segment URL on a private address after a public playlist -> add refused with podnet "refused" words and relay ends; relay answers
    `audio/aac` with only ADTS frames (body parsed back), title from EXTINF reaches `/hx/radio/now/{id}`; `/api/v1/radio/live/{id}`
    same; second live per person 503; radio turned off stops the HLS relay; guest forbidden as for ICY.
17. Docs: `docs/plans/podcasts.md` "Out of scope" drops "HLS radio (needs a segment proxy)" and points to 0039;
    `docs/manual-checks.md` rows RH-1 (web phone 390: add an HLS public broadcaster by address, plays, title updates), RH-2 (Android:
    the same station from Radio, notification title); `docs/roadmap.md` status note.

## 6. Gates
While working: `go test -race ./internal/radio/... ./internal/metadata/online/podnet/...` (H3 adds `./internal/db/... ./internal/web/...`).
Before each commit (AGENTS.md): `make check` and `make build`; `CGO_ENABLED=0 go build ./...` passes; no `go.mod` change.

## 7. Out of scope (reason)
Decrypting any key method (DRM rule); video and muxed-video stripping (bandwidth); shared upstream fan-out (not needed at this size);
adaptive variant switching (one variant per session is enough for audio); LL-HLS parts (whole segments suffice); `EXT-X-BYTERANGE`,
LATM, AC-3/E-AC-3, Opus and FLAC in fMP4 (rare for radio; would need new output types); DASH; hls.js or `media3-exoplayer-hls`
(rejected in 0039); showing `format` in the UI or API; timeshift or seeking in a live station; ffmpeg for radio.
