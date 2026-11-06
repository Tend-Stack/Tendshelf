# 0026 App sign-in for the phone app
Status: accepted (operator confirmed the open points, 2026-11-06). Plan: `docs/plans/android-a1.md`; parent
[0024](0024-android-app.md), keys [0025](0025-app-keys.md). This decision adds one rule to `AGENTS.md`: a device token never becomes a session.

## Context
The Android app shows the server's pages in a WebView and plays in native code, so it needs two credentials at once: a session
cookie for the pages and a device token for the native half. The login password must reach only the server's own sign-in page, never
the app, and the app must be able to reconnect after the cookie is gone. The server cannot tell a home LAN from a cafe, and plain
`http` stays allowed (0024).

## Decision
1. **The code reaches the app as a same-origin 303 the app's window intercepts.** `POST /app/connect` answers
   `303 /app/connect/done?code=...`; the shell's `shouldOverrideUrlLoading` takes the code and cancels the load, so the request never
   reaches the server and no script runs on it. A custom scheme is out (the CSP's `form-action 'self'` applies to redirects, and another
   app could register the scheme). The JavaScript bridge is out (a WebView may refuse it on `http`). A loopback listener is out (any app
   can connect). Polling is out (the attacker's phone would receive the code). A Custom Tab is out (its cookies are not the WebView's).
2. **PKCE S256, RFC 7636 exact.** The verifier is 32 random bytes in base64url without padding (43 characters); the challenge is
   base64url of its SHA-256 (43 characters). No `plain`. No `state`: the verifier does that job. A code is bound to the challenge of the
   app that began, so a code fed in from outside is useless.
3. **One tap of consent.** A GET that issues a code would be a CSRF-able GET, and the tap is the person's own act. The same page will
   serve passwords, and later OIDC or passkeys, with no second path.
4. **The swap extends `POST /api/v1/auth/token`.** Body `{"code","verifier","name","kind":"mobile"}`; answer `201 {token,device,user}`.
   No second token route. The password form stays for A0 and other clients.
5. **The WebView session is not seeded from the token; the order is reversed.** The person signs in on the page, so the session exists
   first; the swap binds that session to the new device. A token-to-session route would be a way round `/admin`, and a restricted session
   type needs a sensitive-route list that goes stale. Cost: a person who opens no page for 30 days signs in on the page once more, while
   playback and Android Auto keep working on the token. A password change on the phone signs the app out, as it ends every device.
6. **Reconnect rebinds, it does not add a device.** A swap with a live bearer of the same person (kind `mobile`) binds the new session to
   the caller's device and answers `200` with no `token`. A bearer of another person, or of another kind, answers `409`.
7. **`sessions.device_id`, enforced in `Sessions.Lookup`**, one place, so every way of ending a device counts, including ones written
   later. No foreign key: device rows are pruned and rebuilt (0025). Bound sessions leave the session lists; the device row stands for
   the phone. `/logout` on a bound session revokes the device; "sign out everywhere else" from the phone keeps the phone.
8. **Plain `http` stays allowed.** `/app/connect` says in words when the address is not private; PKCE adds nothing on `http` and is there
   for code theft, phishing and fixation. A LAN sniffer sees the session and the token; this is warned, not defended.
9. **A bearer token still opens all of `/api/v1/`**, because every route there acts for the caller through `share.Can`. No per-route scope
   list. `/api/v1/settings` carries player keys only: no theme, no account, no server setting, and `sound` stays out until 0022 lands.
10. **`/sw.js` answers 404 under `ts_shell=android`**, on top of `NoOffline`, so a page that loses its script choice still cannot
    register a worker. Series next is opt-in (`"series": true` on `POST /api/v1/queue/next`), so an old client's 204 stays 204; it is
    the app's, and the web player is left alone (its autoplay reads only the queue).

## Threats and answers
Code read by script, history or a shoulder: PKCE. Device-code phishing: nothing is polled; the code goes only to the window that asked.
Sign-in fixation: no scheme or intent takes a code from outside. Stolen token on pages or `/admin`: `bearer` refuses everything outside
`/api/v1/`, and no route turns a token into a cookie. Lost phone: the device is revoked and the bound session dies on its next request.
Cross-site POST: `http.CrossOriginProtection`, the CSRF token, the window mark and a person's tap. Guessing: 256-bit code, 10 failures in
15 minutes per address, hash only at rest, 2 minutes to live, single use, burned by any wrong verifier. A forged `ts_shell` in a browser
reaches one page that makes a code only the forger's own session could swap. A stolen session can mint a 180-day token: accepted, since
a session already owns the account; it is listed in `/me/devices`, audited and revocable.

## Consequences
Migration `0031_device_codes`: `sessions.device_id` and the `device_codes` table. Browsers see no change: no byte differs without
`ts_shell=android`, the CSP is unchanged, and older clients of `/api/v1` are unchanged. A2 must intercept `/app/connect/done` only
for the exact chosen origin and main frame, clear the verifier after one swap, never share the WebView cookie jar with native HTTP, and
keep `GET /api/v1/me` at launch (see the plan, section 8).
