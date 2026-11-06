# A1 Server for the app (plan, 2026-11-06)

For a builder. Parent: [android.md](android.md), decisions [0024](../decisions/0024-android-app.md), [0025](../decisions/0025-app-keys.md).
Order: Go first (steps 1-9), then templ (10-13), then docs (14). All routes additive to `/api/v1`.

**Reused**: `devices` kind `mobile`, `Devices.Issue`, `POST /api/v1/auth/token`, `keyLimiter`, `bearer`, `refuseMixed`, `/me/devices`, `shellFrom`,
`shellLink`, `NoOffline`, `popQueue`, `nextInSeries`, `playableBook`, `ContinueListening`, `LoadSettings`, `SetBookSpeed`. **Extended**: `tokenRequest`,
`sessions`, `Sessions.Lookup`, `Logout`, `SignOutOthers`, `deviceRows`. **New**: table `device_codes`, the `/app/connect` page. **Replaced**: nothing.

## 1. Invariants
1. The app never receives, stores or sends the login password. It is typed only into the server's own page.
2. A device token never reaches page script, a URL, a log or an audit row. Native code holds it; only its SHA-256 is on the server.
3. **A token never becomes a session.** No route takes a bearer token and answers with a cookie, so a token cannot reach any page, `/hx/`, `/admin` or `/me` route.
4. A token opens `/api/v1/` only, sees only what `share.Can` gives its person, and has no more power than today's `desktop` token.
5. A code is single-use, lives 2 minutes, is stored only as a hash, and is useless without the verifier held by the app that started.
6. Ending the phone ends both halves: device revoked, its WebView session dead on the next request. Sign-out from either side does both.
7. `ts_shell` is not a credential. It may change look and scripts, or narrow a page. It never widens access.
8. Browsers see no change: no new byte without `ts_shell=android`, CSP unchanged, old clients of `/api/v1` unchanged.

## 2. Threats
| # | Threat | Answer |
| --- | --- | --- |
| T1 | Code read by page script (XSS), history or a shoulder | PKCE: a code without the verifier burns and gives nothing |
| T2 | Attacker opens the page with their own challenge and phishes a signed-in person (device-code phishing) | The code goes only to the WebView that asked. Nothing is polled, so the attacker's phone never receives it |
| T3 | Attacker feeds their own code into a victim's app (sign-in fixation) | Code is bound to the challenge of the app that began; no scheme or intent accepts a code from outside |
| T4 | Another app registers a custom scheme, or listens on loopback | Neither exists; the handoff stays inside one WebView |
| T5 | Stolen token used to reach admin or pages | Invariant 3; `bearer` already refuses outside `/api/v1/` |
| T6 | Lost phone: token revoked, WebView session lives on | `sessions.device_id`, checked on every lookup |
| T7 | Cross-site POST to mint a code | `http.CrossOriginProtection`, CSRF token, `ts_shell` needed, a person's tap |
| T8 | Guessing codes | 256-bit code; 10 failures in 15 minutes per address (`keyLimiter`); hash only at rest |
| T9 | Forged `ts_shell` in a plain browser | Reaches one consent page and the same code rules; unlocks nothing |
| T10 | LAN sniffer or attacker on `http` | Not defended: a sniffer sees the session and the token. Warned, see D8 |
| T11 | Disabled person, password change, or sign-out everywhere while a code is live | Swap re-reads session and account; `device_codes` cascades from `sessions` |
| T12 | Stolen session mints a 180-day token | Accepted: a session already owns the account. Shown in `/me/devices`, audited, revocable |

## 3. Decisions
- **D1 Delivery of the code: a same-origin 303 the native side intercepts.** `POST /app/connect` answers `303 /app/connect/done?code=...`; the shell's
  `shouldOverrideUrlLoading` takes the code and cancels, so that request never reaches the server and no script runs on it. A custom scheme is out because
  the CSP has `form-action 'self'`, which Chromium applies to redirects, and it invites T4. The bridge is out because A2 may refuse it on `http`. A
  loopback listener is out (T4). Polling is out (T2). A Custom Tab is out: its cookies are not the WebView's.
- **D2 PKCE S256, RFC 7636 exact.** Verifier: 32 random bytes, base64url, no padding, 43 chars. Challenge: base64url(SHA-256(ASCII verifier)), 43 chars.
  No `plain`. No `state`: the verifier does that job.
- **D3 One tap of consent.** A GET that issues a code would be a CSRF-able GET, and the tap is the person's act. Passwords, and later OIDC or passkeys, need no second path.
- **D4 The swap extends `POST /api/v1/auth/token`**, same `201 {token,device,user}`. No second token route, as asked. The password form stays, for A0 and
  other clients; the app stops using it at A2.
- **D5 The WebView session is not seeded from the token; the order is reversed.** The person signs in on the page, so the session exists first, its
  HttpOnly cookie already in the WebView; the swap binds that session to the new device. A token-to-session route would be the way round `/admin` (T5),
  and a restricted session type would need a sensitive-route list that goes stale. Cost: after 30 days with no page opened the person signs in on the
  page once more, while playback and Auto keep working on the token. Operator to confirm.
- **D6 Reconnect rebinds, it does not add a device.** A swap with a live bearer of the same person binds the new session to the caller's device and answers
  `200` with no `token`. Another person's code answers `409`.
- **D7 `sessions.device_id`, enforced in `Sessions.Lookup`**, one place, so every revocation path counts, including paths written later. No foreign key: devices rows
  are pruned and rebuilt (0025). Bound sessions leave the session lists; the device row stands for the phone.
- **D8 Plain `http` stays allowed** (0024). The server cannot tell a home LAN from a café. `/app/connect` says so in words, the app warns once, and A2 warns harder on a
  non-private address. PKCE adds nothing on `http`; it is there for T1-T3.
- **D9 Bearer scope stays "all of `/api/v1/`"**, because every route there acts for the caller. No per-route scope list. `/api/v1/settings` carries player keys only: no
  theme, no account, no server setting.
- **D10 `/sw.js` answers 404 under `ts_shell=android`**, on top of `NoOffline`, so a page that loses its script choice still cannot register a worker. No change for `desktop` or browsers.
- **D11 Series next is opt-in** (`"series": true`), so an old client's 204 stays 204. **Correction to android.md**: `POST /hx/player/next` is queue only; `NextInSeries` is stored and shown
  in settings but no player reads it. A1 adds it for the app and leaves the web player alone.

## 4. Flow
1. App makes verifier and challenge, opens `/?shell=android&next=` + escaped `/app/connect?challenge=<c>`. The mark is set; the gate sends a signed-out person to `/login?next=...`.
2. Person signs in; `/app/connect` shows the consent page; the tap POSTs. Server stores the code hash and answers the 303.
3. Shell takes `code`, calls `POST /api/v1/auth/token`, keeps the token, loads `/`. The same session is now bound and carries on.

## 5. Data shapes
Migration `internal/db/migrations/0031_device_codes.sql` (renumber if another merge takes 0031):
```sql
ALTER TABLE sessions ADD COLUMN device_id TEXT;
CREATE TABLE device_codes (code_hash BLOB PRIMARY KEY CHECK (length(code_hash) = 32),
  session_hash BLOB NOT NULL REFERENCES sessions(token_hash) ON DELETE CASCADE,
  challenge BLOB NOT NULL CHECK (length(challenge) = 32),
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL) STRICT, WITHOUT ROWID;
CREATE INDEX device_codes_session ON device_codes(session_hash);
```
`POST /api/v1/auth/token`, code form: `{"code","verifier","name","kind":"mobile"}`, optional `Authorization: Bearer`.
| Case | Answer |
| --- | --- |
| Good, no bearer | `201 {token,device,user}` |
| Good, bearer of same person, kind `mobile` | `200 {device,user}` (`token` omitted) |
| `code` with `username` or `password` | `400 invalid`, field `code` |
| `kind` not `mobile` | `400 invalid`, field `kind` |
| Unknown, used, expired, wrong verifier, malformed, session gone, account disabled | `401 unauthorized`, one message: "This sign-in has expired. Start again from the app." Counts `keyLimiter.Fail(ip)` |
| Bearer of another person, or not `mobile` | `409 conflict`; code burned |
| Dead bearer | `401` from `bearer`, code not consumed |
| At the device limit | `409 conflict`, existing words; transaction rolls back |
| Limit reached | `429 rate_limited`, `Retry-After`, before any database read |

Other routes (both credentials work; cookies need CSRF as usual):
- `GET /api/v1/settings`, `PATCH` same keys, same answer: `{"speed","skipBackS","skipForwardS","autoRewind","nextInSeries"}`. PATCH overlays present keys on `LoadSettings`, then `SaveSettings`. Bad value: `400 invalid`, `field` = first bad key in that order. Empty patch: 200. Body limit 4 KiB.
- `GET /api/v1/books/{id}` gains `"speed"`: the book's own, else the usual speed.
- `PUT /api/v1/books/{id}/speed` `{"speed":1.5,"asUsual":false}`: 204. Uses `playableBook` + `followMerged`. `asUsual` calls `UseAsUsualSpeed`. Bad: `400`, field `speed`. Hidden: 404.
- `GET /api/v1/continue?limit=`: 1..24, default 12, otherwise `400`, field `limit`. `{"items":[{"book":BookCard,"positionMs","durationMs","updatedAt"}]}`, `no-store`. Audio only; rows failing `ListenBook` dropped.
- `POST /api/v1/queue/next` gains body `"series":true` and answer field `"source":"queue"|"series"`. Queue empty, `ended` set, `series` true and the person's `NextInSeries` on: `nextInSeries` of `ended`, then `ListenBook`, not refused, not missing, not `BookText`; else 204. The queue is never touched by this branch.
- `GET /app/connect?challenge=`, `POST /app/connect` (field `challenge`), `GET /app/connect/done`. Behind the gate. Bad challenge: 404 page. No `ts_shell=android`: 403 page with no form and, on POST, no code. Both `no-store`. `done` never reads its query.

Audit (never a code, verifier or token): `auth.device_code_issued`, `auth.device_code_failed` (`ip`, `reason`), `auth.device_issued` with `via: code`, `auth.device_rebound`.
Copy (not in the deck, listed for review): title "Connect this phone"; body "This phone will be able to play, download and keep your place."; button "Connect"; plain-`http` line "This address is not private. Others on this network could see what you do here."; outside the app "Open Tendshelf on your phone to connect it."; done "Go back to the Tendshelf app."; row title "This phone".

## 6. Steps
1. `internal/db/migrations/0031_device_codes.sql`, as above; `internal/db/migration_device_codes_test.go`, from the previous version and from empty; sessions survive with NULL `device_id`.
2. `internal/auth/errors.go`: add `ErrBadCode`, `ErrCodeOtherPerson`.
3. `internal/auth/sessions.go`: `Session.DeviceID`. `Lookup` also selects `COALESCE(s.device_id,'')` and whether a device with that id has `revoked_at IS NULL`. Bound and not live (or row gone): `Delete`, `ErrNoSession`. New `BindDevice(ctx, ex, hash, deviceID)`.
4. `internal/auth/devicecode.go` (new). `const DeviceCodeTTL = 2 * time.Minute`. `ParseChallenge(string) ([32]byte, bool)`. `(*Service).IssueDeviceCode(ctx, sess Session, challenge [32]byte, ip netip.Addr) (string, error)`: one transaction, deletes rows expired or of this session, inserts via `NewToken`, audits.
5. Same file: `(*Service).SwapDeviceCode(ctx, code, verifier, name string, caller *Device, ip netip.Addr) (DeviceLogin, error)`. Shape checks without the database (`HashToken`; verifier 43 base64url chars). In `WriteTx`: `DELETE ... RETURNING`; none, expired or `subtle.ConstantTimeCompare` false: commit the burn, `ErrBadCode`. Read the session (`expires_at >= now`), `users.ByIDTx`, refuse disabled: `ErrBadCode`. `caller` not nil: same person and `KindMobile`, else commit, `ErrCodeOtherPerson`; `BindDevice(caller.ID)`, empty `Token`. Else `devices.Issue(..., KindMobile)` (`ErrTooManyDevices` rolls back), `BindDevice`, touch last seen, audit.
6. `internal/auth/service.go`: `Logout` also revokes `s.DeviceID` when set, in its transaction. `internal/auth/manage.go`: `SignOutOthers` keeps `sess.DeviceID` (`revokeAllDevices` gains a keep id; `""` keeps none; `EndAllForUser` passes `""`), so the phone does not sign itself out. `list`: add `AND device_id IS NULL`.
7. `internal/web/routes_api.go`: `tokenRequest` gains `Code`, `Verifier`; `tokenResponse.Token` gets `omitempty`. `postAPIToken`, after decoding: `in.Code != ""` goes to new `postAPITokenCode`, implementing the table. Password path unchanged. Limit key: `s.proxies.ClientIP(r).String()`. Caller: `auth.DeviceFrom`.
8. `internal/web/routes_api_settings.go` (new): `GET`, `PATCH /api/v1/settings`, `PUT /api/v1/books/{id}/speed`, `GET /api/v1/continue`; `registerAPISettings` called from `registerAPI`. `internal/web/routes_api_read.go`: `speed` in the book answer.
9. `internal/web/routes_api_queue.go`: the series branch and `source`. `internal/web/routes_offline.go`: `/sw.js` 404 when `shellFrom(r) == "android"`.
10. `internal/web/pages/app_connect.templ` (new): `AppConnect(v AppConnectView)`, states Ask, NotInApp, Done. Plain form with hidden `_csrf` and `challenge`, `hx-boost="false"`. Rendered through `s.frame`. Words in `internal/web/app_connect_copy.go`.
11. `internal/web/routes_appconnect.go` (new): `registerAppConnect`, called in `NewHandler` beside `registerAppKeys`. Needs `auth.SessionFrom`. `Insecure` = `!s.proxies.Secure(r)`.
12. `internal/web/me_views.go`: `deviceRows` gains `currentDevice string`; that row gets `Current: true` and title "This phone". `ownDeviceRows` passes `sess.DeviceID`; admin callers pass `""`.
13. `internal/web/components/layout.templ`: `Shell == "android"` loads `js/native-audio.js`. `internal/web/static/js/native-audio.js` (new): a commented no-op until A4; `player.js` unchanged. `nav.templ`: `navFor(shell)` drops `downloads` for `android`; all three loops use it. Run `make generate`.
14. Docs: `docs/api.md` (all of the above; the 200 and 409 cases), `docs/decisions/0026-app-sign-in.md` (D1-D10), `AGENTS.md` Security and privacy: "A device token never becomes a session." and the `/sw.js` line, `docs/architecture.md` routes, `docs/roadmap.md` status.

## 7. Acceptance tests
Files: `internal/auth/devicecode_test.go`, `internal/web/appconnect_test.go`, `internal/web/api_settings_test.go`, `internal/web/shell_android_test.go`; extend `routes_matrix_api_test.go` and `routes_matrix_devices_test.go`. Prefix new names `TestA1`.
- Happy: anonymous `/app/connect` to `/login` keeps `next`; sign in, tap, swap: 201, `kind` `mobile`, device in `/me/devices`, session bound; the 303 `Location` is same-origin; page HTML holds no token.
- **Forbidden, swap**: second use; wrong verifier (burns; the right one then fails); after 2 min 1 ms; verifier of another code; `plain` challenge of wrong length; `code` with a password; `kind` `desktop`, `opds`, `koreader`; session signed out; person disabled; password changed; person deleted. Each 401 or 400, no device row added.
- **Forbidden, limit**: 10 failures then 429 with `Retry-After` and no database read; a good code from that address also 429; another address unaffected.
- **Forbidden, page**: POST with no CSRF; cross-site `Sec-Fetch-Site`; no `ts_shell`; bad challenge; anonymous; a bearer token on all three routes (401, "Device tokens work only for /api/v1").
- **Forbidden, token**: walk the sweep's page, `/hx/`, `/admin` and `/me` routes with an owner's mobile token: all 401. No answer under `/api/v1` sets `ts_session`. A member's token gets 404 on hidden books on every new route, and through `ended` in `/queue/next` (no title, no card). A bearer plus `ts_session` stays 400.
- **Ending**: each of `POST /me/devices/{did}/signout`, `DELETE /api/v1/auth/token`, admin revoke, `/logout` on the bound session, password change, disable, and pruned device row: token 401 and session gone on the next request. `SignOutOthers` from the phone keeps the phone and ends the rest. Session expiry leaves the token live.
- **Rebind**: same person 200, no `token`, device count unchanged, new session bound. Other person 409, no binding. Dead bearer 401, code still swappable without it. At 50 devices: new 409, rebind still 200.
- **Shell mark**: the matrix with and without `ts_shell=android` gives equal statuses, except `/app/connect` and `/sw.js`. `desktop`: `/sw.js` 200. `/?shell=android` with `Sec-Fetch-Site: cross-site` sets nothing. Android page: `data-shell`, `native-audio.js`, no `offline.js`, no Downloads link. Browser page: none of those, CSP byte-equal.
- **API**: PATCH each key, bad values name `field`, unknown keys ignored, theme not writable. Book speed own and usual. Continue limit bounds. Series branch: opt-out, setting off, last in series, text next, hidden next, queue wins.
- No log or audit row contains a code, verifier or token (capture `slog`, read `audit_log`).

Gates: `make check`, `make build`, `CGO_ENABLED=0 go build ./...`, `make generate` leaves the tree clean, `make visual-compare` shows browsers unchanged. A0's password path still works.

## 8. What A2 must do (recorded here so A1's contract holds)
- Intercept `/app/connect/done` only for the exact chosen origin, main frame, with a flow pending; otherwise ignore it. Clear the verifier after one swap.
- Native HTTP never shares the WebView cookie jar (`refuseMixed`), and never injects script into `/login`; form data saving off.
- WebView reaches `/login`: call `GET /api/v1/me`. 401: sign-out path. 200: open `/app/connect` with a new challenge and swap with the bearer. Also after `/setup` or an invite, when no token is held.
- 409 at the device limit: load `/me/devices`. Call `GET /api/v1/me` at launch. Passkeys in a WebView stay A2's check.

## 9. Out of scope, and why
- Token-to-session route, restricted sessions (D5). Per-route token scopes (D9). Custom scheme, loopback, Custom Tab (D1).
- Sound modes in the settings API: 0022 is proposed, no `playback.sound_*` key exists yet; the field is additive later.
- Series autoplay in the web player: separate fix, needs the operator's view (D11).
- Keeping the app signed in after changing the password on the phone: it ends every device, as `docs/api.md` promises. Offline sign-out: the device stays listed until it next reaches the server or is revoked. A session bound to a device past its 180-day idle limit is cut only when the token is next used, which `GET /api/v1/me` at launch does.
- iOS, device rename, HTTPS enforcement, token rotation.
