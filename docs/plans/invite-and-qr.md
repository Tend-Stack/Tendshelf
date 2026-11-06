# Invitations in the app, and QR codes (plan, 2026-11-06)

For a builder. Decision [0028](../decisions/0028-qr-sign-in.md); parents [0026](../decisions/0026-app-sign-in.md), [android-a1.md](android-a1.md). Two packages, in order: **Server**
(S1-S15, Go, templ, CSS, docs), then **Android** (A1-A9). Copy goes in English here; there is no `values-*` to translate.

**Reused**: `/invite/{token}`, `inviteURL`, `InviteLinkPanel`, `lookupInvite` limits, `device_codes`, `SwapDeviceCode`, `POST /api/v1/auth/token`, `/app/connect/done`, `BindDevice`, `sessions.Create`,
`NewToken`, `ParseChallenge`, `SetCookie`, `/me/devices`, `Origin`, `decide`, `ConnectFlow.accept/proceed/confirmLeave`, `onPageFinished`'s invite and setup rule, `ServerGate`, `data-copy`, 2 s polling.
**Extended**: `IssueDeviceCode` (one helper split out), `gate`, `InviteView`, `InviteLink`, `StartPages`, `Nav`, `ConnectFlow`, `MainActivity`, desktop More menu. **New**: table `device_pairings`, `internal/auth/pairing.go`,
`/me/devices/add`, `/pair` routes, QR component, `rsc.io/qr`, `Handoff`. **Replaced**: nothing.

## 1. Threats
| # | Threat | Answer |
| --- | --- | --- |
| T1 | QR seen over a shoulder, in a screenshot or a screen share | The token only claims. The number shows on the claimer's phone; the owner must type it. D3 |
| T2 | Token holder waits for the real phone's approval, then finishes first | Only the first claimer holds `ts_pair`. No cookie, no finish. PKCE alone would not protect the session |
| T3 | QR from an attacker, pointing at a fake server | Native dialog with the exact origin before any request; no credential sent; ASCII hosts only. D6 |
| T4 | QR from an attacker on the same server: the victim's phone becomes the attacker's account | Attacker cannot read the number; the phone's button names the person; a connected phone refuses |
| T5 | Another app claims `tendshelf:` | The server page's `intent:` names the package; no QR, mail or page gives a bare scheme. A bare link holds no code. D2 |
| T6 | Page text or a book link drives the app | `decide` obeys `/link` only from a bundled page; the claim is a CSRF-protected POST with a tap; a GET changes nothing |
| T7 | Forced approval (the person taps yes out of habit) | No yes button: the number must be typed. One wrong number ends it |
| T8 | Guessing tokens or numbers | 256-bit token, hash only; number has one try; 20 failures in 15 minutes per address; 10 starts an hour per person |
| T9 | Replay of a used QR | One claim; row deleted at finish, wrong number, decline; dead after 3 minutes |
| T10 | Owner signs out, password change, disable, sign-out everywhere during a pairing | FK cascade from `sessions`; finish re-reads session and person |
| T11 | Connected app joins an invite or pairs: WebView and token become different people | App refuses; server refuses a bound session (409) on `/pair` and on `POST /invite/{token}` |
| T12 | A device token or a bearer reaches any of this | `bearer` refuses outside `/api/v1/`; `/me/devices/add` needs a session |
| T13 | Plain `http` | Warned on both devices; passive sniffer cannot finish; active attacker not defended (0026 D8) |
| T14 | Real-time phone call talks the person through it | Not stopped. Words on the page; phone listed, audited, revocable. Accepted |
| T15 | Public page leaks who invited or who is pairing | `/pair/{token}` shows no person until approved; the number page shows the server's name only |

## 2. Invariants
1. A QR or link holds only an address and a one-time token. The token alone gives no session, code, token or name.
2. A session and code reach only the first claimer, and only after the number on that phone was typed on the same session that showed the QR.
3. The password never reaches the app, and a device token never becomes a session.
4. The last leg is the existing one: session, `device_codes`, verifier swap, `BindDevice`. No new token route, no second kind of device.
5. Each person only for themselves. No route takes a user id; `share.Can` is not involved because nothing here touches another person.
6. The phone shows the exact origin natively before any request to it, for pasted and opened links alike. A connected phone never changes server or person silently.
7. Hash only at rest for token, claim and code. None of them, nor the number, in a log or audit row. All tokens stay out of `Location` except the existing 303 and app's own `next`.
8. `ts_shell` is not a credential: it may refuse, never allow. Existing pages for browsers gain only what is named here; `/api/v1` is unchanged.

## 3. Decisions
- **D1 QR content is a plain server address**: `/invite/{token}` as today, `/pair/{token}` new. A camera without the app lands on a working page.
- **D2 No scanner; system camera plus page hand-over.** `intent://open?link=<q>#Intent;scheme=tendshelf;package=app.tendshelf.android;S.browser_fallback_url=<q2>;end`, where `q` is the query-escaped address and `q2` that address plus `?noapp=1`. The fallback stops Chrome going to the Play Store. App Links are impossible per host. Scanner can come later with no other change.
- **D3 Typed 4-digit number, one try, on the device showing the QR**, shown with the phone's name (self-reported, so it proves nothing) and address. Number: `crypto/rand`, `%04d`, column `match_code` (`MATCH` is an SQL word).
- **D4 Lifetime 3 minutes, absolute, `PairTTL`.** One claim. One live pairing per showing session; a new one deletes the old. Starting: 10 an hour per person.
- **D5 Final tap, not a GET.** The phone's Connect is a POST with CSRF that names the person. A GET that makes a session breaks 0026 D3. This is the phone-side consent.
- **D6 Dialog always, before health check.** Title "Connect to this server?", the origin as text, buttons Connect and Cancel; on `http` the warning is in the same dialog and marks it warned. Positive button sets `filterTouchesWhenObscured`.
- **D7 Show on signed-in device, scan with phone.** Reverse is QRLjacking and needs a camera on a laptop (0028 table).
- **D8 Separate table, not `device_codes` columns.** A pairing has no code yet and needs no session on the phone; the row ends by creating an ordinary `device_codes` row.
- **D9 Loopback address: no QR.** There is no public-address setting and `inviteURL` uses `r.Host`, so on `localhost`, `*.localhost`, `127.0.0.0/8`, `[::1]`, `0.0.0.0` show words instead.
- **D10 Invite QR appears with the link panel only.** Only the hash is stored, so a QR exists when the invite is made or on "Copy link again", which replaces the link as it does today. The `/invite/{token}` page shows one of itself.
- **D11 `rsc.io/qr` v0.2.0, imported in `internal/web/qr.go` only**, level M. Probe: a 75-byte address is version 5, 37 modules, about 4 KB of path.
- **D12 Polling answers 204 while nothing changed**, so the QR stays (it cannot be drawn again) and a screen reader is not interrupted. `hx-get` on self, `hx-select="#pair-status"`, `hx-swap="outerHTML"`, `hx-trigger="every 2s"`, only in the states `waiting` (owner) and `claimed` (phone). Every other state, and the 404 ended page, renders `#pair-status` without those attributes, so polling stops and htmx has something to swap in.

## 4. Flows
**Invite in the app**: paste link, or browser page "Open in the app"; dialog; health check; `/?shell=android&next=/invite/TOK`; Join; `/welcome`; existing rule in `onPageFinished` opens `/app/connect`; tap; swap.
**Add a phone**: owner `POST /me/devices/add`, QR; camera; `GET /pair/TOK` in browser; Open in the app; dialog; `/?shell=android&next=/pair/TOK?challenge=C&name=N`; tap Add this phone; `POST /pair/TOK` (claim); `/pair/wait` shows number; owner page shows phone, types number; phone's poll shows "Connect as Ana"; tap; `POST /pair/finish`; 303 to `/app/connect/done?code=`; swap.

## 5. Data and routes
`internal/db/migrations/0033_device_pairings.sql`:
```sql
-- device_pairings: a signed-in session asking to add a phone by QR (decision 0028). Hash only. Ends with that session.
CREATE TABLE device_pairings (id TEXT PRIMARY KEY,
  token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  owner_session BLOB NOT NULL REFERENCES sessions(token_hash) ON DELETE CASCADE,
  state TEXT NOT NULL DEFAULT 'waiting' CHECK (state IN ('waiting','claimed','approved')),
  claim_hash BLOB UNIQUE CHECK (claim_hash IS NULL OR length(claim_hash) = 32),
  challenge BLOB CHECK (challenge IS NULL OR length(challenge) = 32),
  match_code TEXT CHECK (match_code IS NULL OR length(match_code) = 4),
  phone_name TEXT, phone_ip TEXT,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL) STRICT;
CREATE INDEX device_pairings_owner ON device_pairings(owner_session);
```
| Route | Who | Answer |
| --- | --- | --- |
| `GET /me/devices/add` | session | Intro, form "Show a code". Loopback: words, no form. No session (proxy sign-in): 403 page |
| `POST /me/devices/add` | session, CSRF | 200 page with QR and copyable link, never a redirect. Limit: 429 page, `Retry-After`. Loopback: 404 |
| `GET /me/devices/add/{id}` | that session | Status page. htmx and `waiting`: 204. Claimed: phone, address, number field. Expired, row gone, or another session's id, even the same person's: one 404 page, "Show a new code" |
| `POST /me/devices/add/{id}/approve` | that session, CSRF | Field `match`. Right: "Now tap Connect on your phone". Wrong: row gone, ended words. Not `claimed`: 404 |
| `POST /me/devices/add/{id}/cancel` | that session, CSRF | Row gone, 303 `/me/devices`, audited |
| `GET /pair/{token}` | public, no state change | Dead token: 404 page, one answer for every reason, counts a failure. No `ts_shell=android`: hand-over page. In app: bad `challenge` 404; else server name, address, plain-`http` line, button "Add this phone" |
| `POST /pair/{token}` | public, CSRF, `ts_shell=android` | Fields `challenge`, `name`. Sets `ts_pair`, 303 `/pair/wait`. No mark: 403 page. Session with a `DeviceID`: 409 page. Dead or already claimed: 404 page. Limited: 429 page |
| `GET /pair/wait` | `ts_pair` | Number page. htmx and `claimed`: 204. Approved: form "Connect as {name}". No or bad cookie, ended: 404 page |
| `POST /pair/finish` | `ts_pair`, CSRF, `ts_shell=android` | Session and code in one transaction, cookie set, `ts_pair` cleared, 303 `/app/connect/done?code=`. Otherwise 404 page, nothing made |

`ts_pair`: raw `NewToken`, HttpOnly, `SameSite=Strict`, `Path=/pair`, `MaxAge` 180, `Secure` by `s.proxies.Secure`. All pair pages use `inviteHeaders`. Name: trim, drop control characters, 64 runes, empty gives "Phone".

`internal/auth/pairing.go` (errors in `errors.go`: `ErrPairEnded`, `ErrPairMismatch`, `ErrPairBound`; each transition is one `WriteTx`, a guarded `UPDATE`/`DELETE ... WHERE state = ? AND expires_at >= ?`):
`StartPairing(ctx, sess Session, ip) (Pairing, tok string, err)` deletes expired rows and this session's rows. `PairingPending(ctx, tok) error`. `ClaimPairing(ctx, tok string, challenge [32]byte, name string, ip) (claim string, err)`. `OwnerPairing(ctx, id string, sess Session) (Pairing, error)`. `ApprovePairing(ctx, id string, sess Session, number string, ip) error` compares with `subtle.ConstantTimeCompare`; wrong deletes and answers `ErrPairMismatch`. `CancelPairing(ctx, id string, sess Session, ip) error`. `ClaimedPairing(ctx, claim string) (Pairing, error)`, with `PersonName` filled only when approved. `FinishPairing(ctx, claim, ua string, ip netip.Addr, prev *Session) (Login, code string, err)`: `DELETE ... state='approved' ... RETURNING owner_session, challenge`; owner session live; `users.ByIDTx`, not disabled, else `ErrPairEnded`; `prev` with `DeviceID` is `ErrPairBound`, otherwise deleted; `sessions.Create`; `insertDeviceCode`; `TouchLastSeen`.
`Pairing{ID, State, Number, PhoneName, PhoneIP, PersonName string; ExpiresAt int64}`. `PairFailed(ctx, ip, reason)` uses `ipLim` like `InviteFailed`.
Audit: `auth.pair_started`, `auth.pair_claimed` (`ip`, `name`), `auth.pair_approved`, `auth.pair_refused` (`reason`: `number` or `declined`), `auth.pair_failed` (`ip`, `reason`), `auth.login` with `via: pair`, then the existing `auth.device_code_issued` and `auth.device_issued`.

Copy, not in the deck, for review. Owner: title "Add a phone"; "Scan this with your phone's camera. It works once, for 3 minutes."; link label "Or open this link on the phone"; waiting "Waiting for your phone…"; claimed "{name} wants to sign in as you, from {ip}. Type the number on that phone. Only do this if the phone is in your hand."; button "Add phone", quiet "Not my phone"; wrong "That number did not match, so this code has ended. If the phone is yours, show a new code."; loopback "This address works only on this computer. Open Tendshelf at an address your phone can reach, then come back here." Phone: "Add this phone to {server}?", "Only go on if you are doing this yourself, with your other device in front of you."; button "Add this phone"; "Type this number on your other device."; "Connect as {name}"; ended "This code has ended. Show a new one on your other device."; link "Sign in with a password instead" to `/login` (the app's existing `onPageFinished` rule then opens `/app/connect`). Hand-over: "Add this phone", "Open in the app"; other devices "Open this link on the phone you want to add."; `noapp` "The Tendshelf app isn't on this phone yet." Invite page: "Open in the app"; `noapp` "The Tendshelf app isn't on this phone yet. You can join here in your browser."; summary "Continue on your phone"; bound "This phone is signed in. Sign out first to use this invitation."

## 6. Steps: Server package
S1. `go get rsc.io/qr@v0.2.0`. `NOTICES.md` Go module row: `rsc.io/qr v0.2.0 | BSD-3-Clause | QR codes as SVG (internal/web/qr.go only)`. `AGENTS.md` approved list. `docs/architecture.md` "Dependencies and approvals" row.
S2. `internal/db/migrations/0033_device_pairings.sql`; `internal/db/migration_device_pairings_test.go`, from the previous version's fixture and from empty; deleting a session deletes its pairing. Bump the supported version wherever `0032` is named.
S3. `internal/auth/devicecode.go`: split `insertDeviceCode(ctx, tx, sessHash [32]byte, userID string, challenge [32]byte, ip) (string, error)` from `IssueDeviceCode`, behaviour and audit unchanged.
S4. `internal/auth/errors.go`, `internal/auth/pairing.go`, as section 5.
S5. `internal/web/qr.go`: `qrView(text, label string) (components.QRView, bool)`; path is one `M{x} {y}h{w}v1h-{w}z` per horizontal run, offset by the quiet zone 4; `viewBox` is `0 0 {n+8} {n+8}`. Encode error: false, the link still shows. `reachableHost(host string) bool` for D9.
S6. `internal/web/components/qr.templ`: `QRView{ViewBox, Path, Label string}`; `<svg class="ts-qr" viewBox role="img" aria-label><rect class="ts-qr__paper" width="100%" height="100%"/><path class="ts-qr__ink" d/></svg>`.
S7. `internal/web/static/css/tokens.css`: in the `:root` block, `--qr-ink: #000; --qr-paper: #fff;` with a comment that they stay fixed in every theme. `bundle.css`: `.ts-qr` (block, `width: min(100%, 280px)`, `height: auto`, `shape-rendering: crispEdges`), `.ts-qr__paper`, `.ts-qr__ink` fill from those tokens. No `ts-` class reuse for colour.
S8. `internal/web/gate.go`: `isPairPath` beside `isInvitePath` (`/pair/` plus one or more characters), allowed in `gate`.
S9. `internal/web/pair_copy.go`; `internal/web/pages/pair.templ`: `AddPhone(v)`, `PairHandover(v)`, `PairConfirm(v)`, `PairWait(v)`, `PairEnded(v)`. Forms plain, hidden `_csrf`, `hx-boost="false"`; `autocomplete="one-time-code"`, `inputmode="numeric"`, `maxlength="4"` on `match`, with `<label>`. Status inside `<div id="pair-status" role="status">`, which also holds the QR while waiting. Link "Check again" for no script.
S10. `internal/web/routes_pair.go`: `registerPair(mux)`, called in `NewHandler` beside `registerInvites`; the table of section 5. `s.pairLim` in `server.go`, `auth.NewLimiter(10, time.Hour, 1024, opts.Now)`, `Allow(user.ID)` then `Fail(user.ID)` on start, as `mailLim`. `ipLim` via `InviteAllowed` and `PairFailed` on every public route. `Insecure: !s.proxies.Secure(r)` on owner page and `PairConfirm`. Owner pages render the way `getDevices` does; `/pair` pages through `s.frame`.
S11. `internal/web/app_link.go`: `appOpenURL(link string) string` (D2), and `androidBrowser(r)`: `shellFrom(r) == ""` and the User-Agent holds `Android`; cosmetic only. Rendered with `templ.SafeURL` and `hx-boost="false"`.
S12. `internal/web/pages/admin_users.templ`: `InviteLink.QR *components.QRView`, shown in `InviteLinkPanel` under the field. Set in `postInviteLink` and in `routes_share.go:398` when `reachableHost(r.Host)`. Share links stay without one (`LinkCreated`).
S13. `routes_invite.go`, `pages/invite.templ`: `InviteView.QR`, in `<details>` "Continue on your phone", only when `shellFrom(r) == ""`, not `androidBrowser`, and reachable. `AppLink` when `androidBrowser` and no `noapp`; `NoApp` from `noapp=1`, which only hides the button. `Bound` when the session has a `DeviceID`: no form, and `postInvite` answers 409 `InviteState` before `AcceptInvite`.
S14. `internal/web/pages/devices.templ`: `<a class="ts-btn ts-btn--secondary" href="/me/devices/add">Add a phone</a>` under the list. `components/desktop_bar.templ`: `<a role="menuitem" class="ts-wmenu__item" href="/me/devices/add">Add a phone…</a>` after "Change mode or server…"; check `desktop-shell.js` walks it by keyboard. `desktop/menu.go` unchanged: that row is gone on Linux. Run `make generate`.
S15. Docs: `docs/api.md` (`/pair`, unchanged token routes), `docs/architecture.md` routes, `AGENTS.md`: `/pair/...` in the service worker's never list, and under Security "`tendshelf:` carries only a link, never a code", a one-line Addendum in `0026`, `docs/roadmap.md`, status of 0028 to accepted when the operator approves.

## 7. Steps: Android package
A1. `android/shared/.../Handoff.kt` (new): `data class Handoff(val origin: Origin, val kind: Kind, val token: String)`, `enum class Kind { Invite, Pair }`, `fun parseHandoff(text: String): Handoff?`. At most 2048 chars. Take the first whitespace-separated word starting `https://`, `http://` or `tendshelf://`; strip trailing `.,;:)>`. `tendshelf://open?link=` decodes once, one level only, and must give http(s). `parseUrl` must accept; origin through `Origin.parse`. Path exactly `/invite/` or `/pair/` plus 43 base64url characters. Query and fragment are ignored.
A2. `Origin.kt`: `inviteUrl(token)` is `$text/?shell=android&next=%2Finvite%2F$token`; `pairUrl(token, challenge, name)` is the same with an escaped `/pair/$token?challenge=...&name=...`.
A3. `Navigation.kt`: `Nav.Link(val text: String)`; in `decide`, bundled path `/link` gives it from query `link`, inside the existing main-frame-and-`fromBundled` check. `ShellWeb.kt`: `is Nav.Link -> f.onLink(nav.text)`.
A4. `ConnectFlow.kt`: `onLink(text)`: parse, else `showJoin(R.string.link_only)`. Token held and same origin: dialog `link_connected`, stay. A pairing with the same token already pending: ignore, keeping the verifier. Then the D6 dialog; Connect runs `health` and `identity` on that exact origin, failures go to `showJoin(words)`; then a `pending: Handoff?` field and `proceed(o, id)`. `proceed` and `confirmLeave` call new `begin(o)`: `pending` Invite gives `beginInvite`, Pair gives `beginPair`, null gives `beginConnect`; `pending` is cleared. `beginPair` makes the verifier as `beginConnect` does, `connectLoading = true`, `suppressAutoConnect = false`, `state = NeedsSignIn`, loads `pairUrl(token, challenge, Build.MODEL)`. `beginInvite`: same without a verifier. `signInPath` adds `/pair/`. Cancel returns to the page behind.
A5. `MainActivity.kt`: `onCreate` after `launch`, and `onNewIntent`: `ACTION_VIEW`, scheme `tendshelf`, host `open`, then `flow.onLink(data.toString())`, then clear the intent's data so `recreate()` cannot repeat it.
A6. `AndroidManifest.xml` on `.MainActivity`: filter with `VIEW`, `DEFAULT`, `BROWSABLE`, `<data android:scheme="tendshelf" android:host="open"/>`. No camera permission, no `https` filter.
A7. `StartPages.kt`, assets: `choose.html` (new, two `ts-btn` links, `/server` "I have a server", `/join` "I have an invitation", and "Have a code on another screen? Point your phone's camera at it."), `join.html` (new; form `GET /link`, field `link`, label "Invitation link", hint "Paste the link you were sent", button "Continue"). `/start` gives `choose.html` when address and note are empty, else `start.html` as today; `/server` gives `start.html`; `/join` gives `join.html` with `{{NOTE}}` and `{{LINK}}`. `start.html` gains a quiet link to `/join`. No script, values escaped. New `@Volatile var joinState = StartState()` and `joinUrl`; `ConnectFlow.showJoin(words)` sets it and loads that page. `showStart` is unchanged.
A8. `res/values/strings.xml`: `start_server`, `start_invited`, `start_scan_hint`, `join_label`, `join_hint`, `join_button`, `link_only` "That doesn't look like an invitation link.", `link_connected` "This phone is already signed in to that server. Sign out first to use this link.", `link_title`, `link_body` "Tendshelf will connect to %1$s. Only go on if you know this address.", `link_connect`.
A9. `android/app/src/main/assets/start.css`: chooser spacing only. No Gradle change, no new dependency. Record the manual checks in `docs/roadmap.md` (section 9).

## 8. Acceptance tests (prefix `TestPair`, `TestInviteApp`, `TestQR`)
Files: `internal/auth/pairing_test.go`, `internal/web/pair_test.go`, `internal/web/routes_matrix_pair_test.go`, `internal/web/qr_test.go`, extend `invite_test.go`, `internal/web/visual_pair_test.go`; `HandoffTest.kt`, extend `NavigationTest.kt`, `OriginTest` in `SharedTest.kt`.
- **Happy**: full flow with two clients: device in `/me/devices`, kind `mobile`, session bound, no password used; then `POST /me/devices/{did}/signout` kills token and phone session. Invite with `ts_shell=android`: join, `/app/connect`, swap, no password.
- **Forbidden, claim**: second claim; expired by 1 ms; unknown; malformed; used; no `ts_shell`; no CSRF; cross-site `Sec-Fetch-Site`; bad challenge, `plain`; bound session (409); limit (429, no database read). A GET leaves the row `waiting`. Dead tokens give byte-equal pages.
- **Forbidden, approve**: anonymous; another person; same person's other session (404); bearer token (401); no CSRF; before claim; second approve; wrong number deletes the row and the right one then fails; expired. Each leaves no session and no code.
- **Forbidden, finish**: before approval; no `ts_pair`; another claim's cookie; right token but no cookie (T2); no `ts_shell`; no CSRF; twice; owner signed out, password changed, person disabled or deleted after approval; expired; bound `prev`. No `sessions` or `device_codes` row added.
- **Forbidden, swap**: the code with a wrong verifier burns; the claim's challenge is the only one that works.
- **Forbidden, who**: no route takes a person; owner's page for another session's id is the 404 page; an admin gets the same; a member and a guest each add their own phone; a disabled person cannot start.
- **Limits**: 11th start in an hour 429; 20 public failures 429, another address unaffected.
- **Leaks**: `/pair/{token}` and `/pair/wait` before approval hold no person's name; `slog` and `audit_log` hold no token, claim, code, or number; `Set-Cookie` flags exact; every pair page `no-store`, `no-referrer`, `noindex`; CSP byte-equal; no `style=` and no `<script` in the new markup.
- **QR**: size 37 for the probe address; three finder patterns; quiet zone 4 in `viewBox`; path covers exactly the black modules; each of four themes keeps `--qr-paper` and `--qr-ink`; loopback list gives no QR and no form, and `POST` 404; a real-phone scan is manual (no decoder in tests).
- **Hand-over**: exact `intent:` address, `package`, fallback, `hx-boost="false"`; present only on an Android browser; absent with `noapp=1`, inside the app and on desktop. **Invite**: the matrix for browsers is unchanged apart from `<details>`; bound session: no form, POST 409, no account made, invite unused.
- **Handoff**: table with spaces, words around the link, trailing punctuation, `tendshelf://open`, double-wrapped (refused), userinfo, backslash, non-ASCII host, `ftp:`, wrong path, 42 and 44 characters, 2049 characters. **Nav**: `/link` from a server page or subframe is `Block`.
- **Visual**: `TestPair` captures at 390, 768, 1024, 1440 in four themes, seeded ids, fixed token.
Gates: `make check`, `make build`, `CGO_ENABLED=0 go build ./...`, `make generate` clean, `make visual-compare` (existing pages identical except named ones), bundle budget; Android: `make apk-debug`, shared unit tests, lint.

## 9. Manual checks (need a real phone)
System camera opens the address on a Pixel and a Samsung (guess: both do). Chrome, Firefox and Samsung Internet honour `intent:` with `package`, and the fallback (guess). App running and not running: `onNewIntent` fires and only one window opens. Rotation repeats no dialog. Scan on a dark-themed screen.

## 10. Out of scope
In-app scanner (D2). Signing a browser in by QR. QR on share links. A public-address setting (D9). Mail or push on a new device. iOS. Changes to the invite email: its `https` link already lands on the page with the button. Fixing an invited person with no password whose session lapsed (reported to the operator).
