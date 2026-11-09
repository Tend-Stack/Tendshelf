# 0035 The next book is ready on the phone
Status: accepted (plan: `docs/plans/android-l8-auto-download.md`). Row L8 of `docs/plans/next-stage.md`. Parents: Android downloads (A7, [0024](0024-android-app.md)),
Up next and series autoplay ([0026](0026-app-sign-in.md)). Replaces A7's invariant 7 ("a foreground `dataSync` start happens only from the visible activity") by
one exception, and the D2 wording that no download starts without a person's tap.

## Decision
The Android app downloads the next book on its own, once, when the current one is nearly over and the phone is on Wi-Fi. It reuses `Offline`, the download
service, `downloadPlan`, the skip-silence setting path and `seriesNextBook`; it adds one read-only `GET /api/v1/queue/next`, one server setting
(`playback.auto_download`) and no dependency, table or app database column.

- **D1** Off by default, one switch, Wi-Fi always; no mobile-data option.
- **D2** The switch is a server setting, shown only in the app. Each phone applies its own quota.
- **D3** At most 5 GiB of automatic books kept, at least 2 GiB free after the book, and the normal 200 MiB spare still applies first. At the quota it stops and
  says so; it never deletes.
- **D4** Trigger: the last 2 hours of audio (book time, speed ignored); a book under 2 hours qualifies at once.
- **D5** Finished books are not removed; the quota makes that unnecessary. Removal stays a separate approval.
- **D6** It starts while the book plays or the app is open, on Wi-Fi. A started download finishes with the screen off. It does not start with the app closed.
- **D7** Start from the playback service while it is playing. No scheduler.
- **D8** WorkManager rejected: a worker that outlives its window needs the same foreground start and is refused the same way from the background.
- **D9** A per-download stop reason holds an automatic book (`AUTO_HOLD`); `setRequirements(NETWORK_UNMETERED)` is manager-wide and would stall a person's own download.
- **D10** The Wi-Fi check runs in the existing 1 s poll. Named limit: up to about a second of data after Wi-Fi drops.
- **D11** Marks live in the `offline` preferences, not a column; the mark is written before the row.
- **D12** Any loss of Wi-Fi holds; resuming waits for an allowed moment.

## Evidence for D7 (read 2026-11-08)
The platform lets an app start a foreground service from the background while it holds `PROCESS_CAPABILITY_BFSL`, which a running regular foreground service
sets. A6-A10 measured `procState=FGS,cap=---NFU-T` while playing and a denial with no foreground service. It is platform behaviour, not a documented promise;
Android 17 is unread. A refusal throws `IllegalStateException` (`ForegroundServiceStartNotAllowedException`), caught in `Offline.releaseHeld`, which starts
the service before it clears any hold, so a refusal changes nothing. Emulator check E2 in `docs/manual-checks.md` proves it.

## Limits
Nothing starts with the app closed and nothing playing. A failed automatic row is not retried. `sizeBytes` is the manifest size, not a browser-friendly
rendition's. A phone filled by other apps while a book downloads is caught by the 30 s room check, not reserved.
