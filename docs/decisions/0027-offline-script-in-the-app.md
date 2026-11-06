# 0027 offline.js runs in the Android app
Status: accepted (operator, 2026-11-06, with the A9 plan). Plan: `docs/plans/android-a6-a10.md` A9; parents
[0012](0012-offline-pocket.md), [0019](0019-offline-reading.md), [0026](0026-app-sign-in.md).

## Context
0026 D10 keeps the service worker out of the app (`/sw.js` answers 404 there) and A1 left `offline.js` out of the Android window. The
app now stores a book's text itself (A9) and answers its addresses in `shouldInterceptRequest`, so the worker's part is played by
native code. The reading flush and the bookmark queue live in `offline.js`; a second flusher would be a parallel system.

## Decision
1. **`offline.js` loads in the Android shell** (`NoOffline` is true only for other shells). Its worker registration and download manager
   already stay off by `data-shell`. `/sw.js` still answers 404 in the app.
2. **`reader-offline.js` treats every book as held in the app**, so a sent place stays as the last local place and the stored shell
   reopens at the newest place. It does not call `refreshShell` there (no `caches`, and the app refreshes the shell itself).
3. **The manifest gains `assets`**, the worker's precache list. The app stores those files without credentials. No new route.
4. **Native reads the session cookie** (`CookieManager`) only to fetch the person's own text from the server's origin. It is never
   logged or stored. The device token stays under `/api/v1/` (0026).
5. **Offline answers only the exact stored origin, only GET, only when the server is unreachable**, with the stored `Content-Type` and
   `Content-Security-Policy`. Highlights, notes and bookmarks offline stay unhandled (0019).
