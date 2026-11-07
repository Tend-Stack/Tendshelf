# Plan: Sign-in links (decision 0030)
One builder package (Go, templ, copy). Read `docs/decisions/0030-sign-in-links.md` first; it is binding. A gap is reported as `PLAN GAP: <one line>`
after committing what is green. Reused: invite accept (`internal/auth/invite.go`), `InviteAllowed` limiter, `recheckManage`/`changePerson`,
`InviteLinkPanel`, `qrView`/`reachableHost`, `inviteHeaders`, `InviteState`, `/welcome`, `Sessions.EndAllForUser`. Nothing is replaced.

## Invariants
1. Only `SHA-256(token)` is stored; the raw token appears only in the one POST response that made it. Never in a log, audit row or `Location`.
2. A link works once, within 24 h, for an enabled non-owner account, while not revoked. Spending is an `UPDATE ... WHERE used_at IS NULL AND
   revoked_at IS NULL` that must affect exactly 1 row (race-safe).
3. Making a link: `personForm` + `changePerson` (fresh `recheckManage` in the tx); self, owner-for-admin, and people who manage nobody get 404.
4. Every failure on `/signin/{token}` (GET or POST) returns the same 404 page and costs the address one failure; a limited address gets 429
   before the database is read.
5. `/me/new-password` works only for a session whose hash is on a `sign_in_links` row with `revoked_at IS NULL` and `used_at >= now - 1h`;
   otherwise 404. Ordinary sessions, device-bound sessions and bearer tokens never reach it.
6. Saving there: one WriteTx sets the hash, `EndAllForUser` (sessions + device tokens), creates one new session, audits.
7. New link, disable, and role change revoke all of the person's links (`revoked_at = now, session_hash = NULL WHERE revoked_at IS NULL`).
8. GET never changes a row except the limiter and the failure audit.

## Decisions
- TTL `SignInLinkTTL = 24 * time.Hour`: long enough to hand over by message the same day, short for a credential.
- Reset window `SignInResetWindow = time.Hour` after spending: bound to the link-started session via `session_hash` FK `ON DELETE SET NULL`, so
  ending sessions clears it with no extra code.
- No email (decision 0030 point 3). No new CSS unless a check needs it (use `caption`, existing `ts-users__*`, `ts-invite*` classes).
- One failure page for all reasons (simplest; never tells whether an account exists). Audit keeps the reason.
- Prune: on create, `DELETE FROM sign_in_links WHERE created_at < now - 48h` (all users; 24 h + 1 h window fits).

## Data: migration `internal/db/migrations/0035_sign_in_links.sql`
```sql
-- sign_in_links: a one-time link an admin makes so a person can sign in and choose a password (decision 0030). Only the SHA-256
-- of the token is stored. session_hash is the session the link started; it unlocks /me/new-password for one hour.
CREATE TABLE sign_in_links (id TEXT PRIMARY KEY,
  token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, used_at INTEGER, revoked_at INTEGER,
  session_hash BLOB REFERENCES sessions(token_hash) ON DELETE SET NULL CHECK (session_hash IS NULL OR length(session_hash) = 32)) STRICT;
CREATE UNIQUE INDEX sign_in_links_open ON sign_in_links(user_id) WHERE used_at IS NULL AND revoked_at IS NULL;
CREATE INDEX sign_in_links_session ON sign_in_links(session_hash) WHERE session_hash IS NOT NULL;
```
`internal/db/db.go`: `SchemaVersion = 35`. IDs via `internal/db/id.go`; token via `token.New()`/`token.Hash()`.

Audit actions (exact): `user.signin_link` (UserID actor, Target `"user:"+id`, Detail `{"expires_at": ms}`); `auth.login` with Detail
`{"ip", "via": "sign_in_link"}` (UserID/Target the person); `auth.signin_link_failed` (Detail `{"ip", "reason"}`, reasons `malformed`, `unknown`,
`used`, `expired`, `revoked`, `disabled`, `owner`); `auth.password_reset` with Detail `{"by": "sign_in_link", "ip"}`; `user.signin_link_revoked`
only when a revoke touched rows (Detail `{"why": "new_link"|"disabled"|"role"}`).

## Steps
1. `internal/db/migrations/0035_sign_in_links.sql` as above; `internal/db/db.go` version 35; `internal/db/open_test.go:216` count `+28` and append
   `sign_in_links (0035)` to the comment.
2. `internal/db/migration_sign_in_links_test.go`: `TestMigrationSignInLinksFromPrevious` (`openUpTo(t, 34, dir)`, `seed7`, users and sessions
   counts survive, table empty, both indexes via `indexCount`) and `TestMigrationSignInLinksFromEmpty` (indexes exist; inserting two open rows for
   one user fails; deleting a session sets `session_hash` NULL).
3. `internal/auth/signin_links.go` (new), methods on `*Service`:
   - consts `SignInLinkTTL`, `SignInResetWindow`; `var ErrSignInLinkBroken = errors.New("auth: sign-in link does not work")`,
     `ErrNotLinkSession`, `ErrNoPassword`.
   - `MakeSignInLink(ctx, tx *sql.Tx, byID, userID string) (tok string, expiresAt int64, err error)`: prune, `RevokeSignInLinks(ctx, tx,
     userID, "new_link")`, insert, audit `user.signin_link`. Caller has already re-checked rights.
   - `RevokeSignInLinks(ctx, ex db.Execer, userID, why string) (int64, error)`: the invariant-7 UPDATE; audits `user.signin_link_revoked` when n > 0.
   - `PendingSignInLink(ctx, userID string) (expiresAt int64, ok bool, err error)`: the open, unexpired link of a person, for the person page.
   - `CheckSignInLink(ctx, tok string, ip netip.Addr) (name string, err error)`: read-only, for GET. `InviteAllowed` first; any failure ->
     `signInFailed(ctx, ip, reason)` + `ErrSignInLinkBroken`. `name` = display name, else username.
   - `SpendSignInLink(ctx, tok string, ip netip.Addr, ua string, prev *Session) (Login, bool /*hasPassword*/, error)`: mirrors `AcceptInvite`
     (lines 33-84): limiter, hash, WriteTx {`DeleteExpired`; select link JOIN users; reason checks in order malformed, unknown, revoked, used,
     expired, owner, disabled; spend UPDATE (0 rows -> `used`); delete prev; `sessions.Create`; `UPDATE sign_in_links SET session_hash = ?`;
     `TouchLastSeen`; audit `auth.login`}. Failure reasons (not limiter, not db errors) call `signInFailed`. hasPassword read via
     `users.HasPassword` after the tx.
   - `SignInBound(ctx, sess Session) (bool, error)`: invariant 5 query with `user_id = sess.UserID`.
   - `SetPasswordFromLink(ctx, sess Session, next string, ip netip.Addr) (string, error)`: validate like `ChangePassword` (manage.go, the
     `users.Validate` line) -> `users.FieldErrors{"password": code}`; hash before the tx; in WriteTx re-run the bound check (else
     `ErrNotLinkSession`), `HasPassword` false -> `ErrNoPassword`; `SetPassword`, `EndAllForUser`, `sessions.Create(sess.UserAgent)`, audit
     `auth.password_reset`. Return the new token.
   - `signInFailed(ctx, ip, reason)`: `a.ipLim.Fail(IPKey(ip))` + audit `auth.signin_link_failed`.
4. `internal/auth/signin_links_test.go` (tests listed below, section A).
5. `internal/web/gate.go`: `const signInPrefix = "/signin/"`, `isSignInPath(p)` = prefix plus one non-empty segment with no `/` (copy
   `isPairPath`); add to the public list in `gate`; update the doc comment.
6. `internal/web/signin_copy.go` (new): the strings in "Copy" below, each `// NEW`.
7. `internal/web/pages/signin.templ` (new): `SignInConfirmView{CSRF, Token, ServerName, Title, Body, Button string}` + `SignInConfirm` (mirror
   `PairConfirm`/`Invite` structure: `ts-invite ts-invite__state`, POST form to `/signin/{token}`, `hx-boost="false"`, hidden `_csrf`);
   `NewPasswordView{CSRF, Username, Error string}` + `NewPassword` (mirror `welcome.templ` password branch: hidden-from-view
   `autocomplete="username"` field with the username, `components.TextField` "New password", `Type: "password"`, `Autocomplete: "new-password"`,
   primary button, quiet link "Not now" to `/`). Run `make generate`.
8. `internal/web/routes_signin.go` (new) `registerSignIn(mux)`, called next to `s.registerPair(mux)` in `server.go:294`:
   - `GET /signin/{token}`: `inviteHeaders`; device-bound session (`sess.DeviceID != ""`) -> `inviteState` 409 `signInBoundNotice`;
     `CheckSignInLink` -> 429 `inviteLimited`, broken -> `signInBroken` (404, `InviteState`), ok -> `SignInConfirm` 200 in `s.frame`, `InviteStyles`.
   - `POST /signin/{token}`: `parseForm` (CSRF); same device check (409, link untouched); `SpendSignInLink`; `startSession`; 303 to `/welcome`
     when no password, else `/me/new-password`.
   - `GET /me/new-password`: session required; `SignInBound` false -> `notFound`; no password -> redirect `/welcome`; render `NewPassword` in
     `s.frame` titled `signInNewTitle`.
   - `POST /me/new-password`: `parseForm`; `SetPasswordFromLink`; ok -> `startSession(tok)`, 303 `/me?changed=password`; FieldErrors -> 422
     with `fieldMessage("password", code)`; `ErrNotLinkSession` -> 404; `ErrNoPassword` -> 303 `/welcome`.
   - `POST /admin/users/{id}/signin-link`: `personForm`; `changePerson(w, r, id, "", fn)` where fn returns `errRefused` if `t.Disabled`, else
     `MakeSignInLink(ctx, tx, actor.ID, id)`; on success `inviteHeaders` and `renderPerson` 200 with `v.SignInLink` set (no redirect). Register it
     in `registerUsers` (`routes_users.go:26-40`) next to the disable routes.
9. `internal/web/pages/admin_user.templ`: `AdminUserView` gains `Username string`, `NoPassword bool`, `SignInOpen string` (the "open until"
   line or ""), `SignInLink *InviteLink`. In the header, after `v.Meta`, a `<p class="caption ts-users__sub">` with `personUsername` (or
   `personUsernameNoPassword`). In the Account section, before the on/off control and only when `!v.Off`: the panel `@InviteLinkPanel(*v.SignInLink)`
   when set, else `SignInOpen` caption when set; then a POST form (`_csrf`) with secondary button `signInMake` and caption `signInMakeHelp`.
10. `internal/web/routes_users.go` `personView`: fill `Username`, `NoPassword` (`s.users.HasPassword`), `SignInOpen` (`PendingSignInLink`,
    formatted `Mon 2 Jan, 15:04` in `s.now().Location()`). The panel: `&pages.InviteLink{Name: name, URL: s.signInURL(r, tok), Title:
    fmt.Sprintf(signInPanelTitle, name), Note: fmt.Sprintf(signInPanelNote, when, name), Label: signInFieldLabel, FieldID: "signin-link",
    QR: s.signInQR(r, tok)}`; `signInURL`/`signInQR` copy `inviteURL` (routes_share.go:352) and `inviteQR` (routes_invite.go:461) with
    `/signin/` and `signInQRLabel`.
11. `routes_users.go` `postPersonDisable` (:422) and the `postPersonRole` changePerson fn (:382): call `s.auth.RevokeSignInLinks(ctx, tx, id,
    "disabled"|"role")` inside the same tx.
12. `internal/web/pages/login.templ` (after the submit button): `<p class="caption ts-frame__note">` with `loginNoPassword`; if
    `ts-frame__note` does not exist use the caption class already used under login forms (grep `pages/setup.templ`), no new CSS.
13. `internal/web/static/js/sw.js:13`: add `"/signin/"` to `PASS` and to the comment above it. Grep `*_test.go` for a test pinning PASS; extend.
14. `AGENTS.md`: the two edits in decision 0030 "Changes to AGENTS.md". `docs/architecture.md`: add the four routes wherever `/invite/{token}`
    is listed (grep). `docs/roadmap.md`: status note under the line ending "(reported, not designed here)": built, checks, limits.
15. Web tests (section B), matrix rows, a11y rows, visual test; then gates.

## Copy (exact; all NEW, follow BRAND.md voice)
- `personUsername = "Username: %s"`; `personUsernameNoPassword = "Username: %s · No password yet"`
- `signInMake = "Make a sign-in link"`; `signInMakeHelp = "For when %s can't sign in. It works once, for 24 hours, then they choose a new password. A new link ends the old one."`
- `signInOpenLine = "A sign-in link is open until %s."`
- `signInPanelTitle = "Sign-in link for %s"`; `signInPanelNote = "Works once, until %s. Send it only to %s."`; `signInFieldLabel = "Sign-in link"`;
  `signInQRLabel = "QR code for this sign-in link"`
- `signInConfirmTitle = "Sign in to %s"` (server name, `s.serverName`); `signInConfirmBody = "You'll be signed in as %s on this device."`;
  `signInConfirmButton = "Sign in"`
- `signInBrokenTitle = "This sign-in link doesn't work"`; `signInBrokenBody = "It may be used already or too old. Ask whoever runs this server for a new one."`
- `signInBoundNotice = "This phone is signed in. Sign out first to use this sign-in link."`
- `signInNewTitle = "Choose a new password"`; `signInNewLead = "Your username is %s. You'll need both to sign in."`; `signInNewLabel = "New password"`;
  `signInNewButton = "Save password"`; `signInNewSkip = "Not now"`
- `loginNoPassword = "No password yet, or forgot it? Ask whoever runs this server for a sign-in link."`

## Acceptance tests
A. `internal/auth/signin_links_test.go` (fake clock as in `invite_test.go`): `TestSignInLinkSpendOnce` (second spend broken; reason `used`
audited); `TestSignInLinkExpired` (24h+1ms); `TestSignInLinkNewRevokesOld` (old token broken, old binding cleared); `TestSignInLinkRevokedOnDisable`;
`TestSignInLinkDisabledAccount` (row open, account disabled -> broken); `TestSignInLinkOwnerRowRefused` (row inserted by SQL for the owner);
`TestSignInLinkMalformedNoRead`; `TestSignInLinkLimiter` (20 failures -> `*RateLimitedError`, then a good token also limited);
`TestSignInLinkCheckDoesNotSpend`; `TestSignInLinkSpendRace` (two goroutines, exactly one `Login`); `TestSignInLinkEndsPrevSession`;
`TestSignInBound` (link session true; ordinary session false; after 1h+1ms false; after revoke false); `TestSetPasswordFromLink` (old sessions
gone, devices revoked as in `devices_test.go:336`, new token valid, second call with new session `ErrNotLinkSession`, login with new password ok);
`TestSetPasswordFromLinkNoPassword` (`ErrNoPassword`); `TestSignInLinkAudit` (the six action names; no row contains the raw token).
B. `internal/web/signin_test.go`: `TestSignInLinkMakeByRole` (owner -> admin, member, guest, contributor: 200 with panel and `signin-link`
field; admin -> member, guest, contributor: 200; admin -> owner, admin: 404; member, guest, contributor -> anyone: 404; self: 404; disabled target:
404; anonymous: redirect `/login`; missing CSRF: 403; response has `Cache-Control: no-store`, no `Location`); `TestSignInLinkOneAnswer` (malformed,
unknown, used, expired, revoked, disabled, owner: same body, 404, GET and POST); `TestSignInLinkGetDoesNotSpend`; `TestSignInLinkNoPasswordToWelcome`;
`TestSignInLinkResetFlow` (POST -> `/me/new-password`, 422 on a short password, save -> other browser's cookie dead, `/me?changed=password`);
`TestNewPasswordOrdinarySession404` (GET and POST, incl. a session of the same person not started by the link); `TestSignInLinkAppSession409`
(device-bound session, link still open after); `TestSignInLinkBearer` (bearer token on `/me/new-password` and `POST /signin/{tok}` never spends or
answers 200); `TestSignInLinkRoleChangeRevokes`; `TestLoginNoPasswordLine`; `TestPersonPageUsername`; `TestGateSignInPaths` (add `/signin/x`
public, `/signin/`, `/signin/x/y` gated to `TestGateAnonymous`'s table or beside it); sw.js PASS contains `/signin/`.
C. `routes_matrix_users_test.go`: rows for `POST /admin/users/{id}/signin-link` per role; `/me/new-password` rows (anonymous redirect, bearer 401/redirect).
D. `a11y_pages_test.go` rows: `signin-confirm`, `signin-broken`, `me-new-password`, `admin-user-signin-panel`. `visual_signin_test.go` (dev, mirror
`visual_pair_test.go`, seeded ids, fixed clock) at 390, 768, 1024, 1440.
Gates: `make generate` (tree clean), `go test -race ./internal/auth/... ./internal/db/... ./internal/web/...`, `make check`, `make build`,
`internal/web/static/motion_test.go` budget only if CSS or JS changed beyond sw.js.

## Out of scope
Emailing links; a self-service forgot-password form; owner recovery (CLI stays); sign-in links inside the Android app; changing `/welcome`
(it still does not end other sessions); re-checking the link's maker at spend time (revocation on disable and role change covers the target;
a maker who loses admin later does not void links already made); translations (separate package).
