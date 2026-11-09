# Plan: OpenID Connect sign-in (S7, decision 0037)
Three builder packages, in order: **P1** Go core (migration, `internal/auth/oidc`, `auth.Service` methods, fake provider, tests); **P2** web
(routes, gate, pages, copy, handler and visual tests, docs); **P3** Android (`android/` only). Read `docs/decisions/0037-openid-connect.md`
first; it is binding. A gap is `PLAN GAP: <one line>` after committing what is green. Reused: `auth.Service`/`Sessions.Create`/`ipLim`,
`Limiter`, `SafeNext`, `users.Repo.CreateInvited`, `db.Audit`, `db` KV (`internal/db/kv.go`), the SMTP secret pattern
(`internal/share/mailconfig.go`), `s.allow(ManageServer)`, `/welcome`, `frame`, `errorPage`. Nothing is replaced. No new Go module.

## Invariants
1. A callback signs in only when: address not limited; `state` query equals the `ts_oidc` cookie (constant-time); a row with
   `SHA-256(state)` exists, is unexpired and is deleted in the same statement (`DELETE ... RETURNING`); the ID token passes every check of
   0037 point 5. Any failure: row gone, cookie cleared, one `ipLim.Fail`, one `auth.oidc_failed` audit with a reason, generic message.
2. Only `SHA-256(state)` is stored. Nonce and verifier are stored raw (10-minute rows, not credentials to Tendshelf). No code, token,
   state, nonce, verifier or client secret in a log line, audit row, page or `Location` (except the authorize URL sent to the browser).
3. The owner and admins are never linked by email and never created. Created accounts are `member` or `guest`, no grants, no password.
4. Linking writes `oidc_identities` only from `POST /me/sso/confirm` with CSRF, by the session whose hash is on the row.
5. `(issuer, subject)` is unique; `(user_id, issuer)` is unique. A disabled account never signs in by provider.
6. The HTTP client in `internal/auth/oidc` reaches only the issuer origin (discovery, token, JWKS, userinfo); the origin is checked before
   every request; no redirects; caps and timeouts of 0037 point 11.
7. The password form on `/login` is always rendered and works whether the provider is up, down or misconfigured.
8. `http.Client` is constructed only in `internal/metadata/online`, `internal/desktopshell` and `internal/auth/oidc` (architecture test).

## Decisions
- Stdlib JOSE (0037 point 1). Algorithms map: RS* -> `rsa.VerifyPKCS1v15`, PS* -> `rsa.VerifyPSS` (salt = hash length), ES* -> raw r||s
  of 2x curve bytes -> `ecdsa.Verify`, EdDSA -> `ed25519.Verify` (crv Ed25519 only). JWK `alg`, when set, must equal the header `alg`.
- Keys `oidc.enabled` (bool), `oidc.name`, `oidc.issuer`, `oidc.client_id`, `oidc.client_secret`, `oidc.server_address`, `oidc.match_email`
  (bool), `oidc.create_accounts` (bool), `oidc.new_role` ("member"|"guest", default "member"). One provider.
- Client auth: secret set -> `client_secret_basic` (RFC 6749 2.3.1: form-urlencode id and secret, then base64) unless discovery's
  `token_endpoint_auth_methods_supported` lists `client_secret_post` and not `client_secret_basic`; no secret -> `client_id` in the body.
- Timeouts: 5 s per request, 15 s budget for the whole callback (`context.WithTimeout`). Synchronous by protocol (the callback must answer).
- Saving with "on" runs discovery once (5 s) and refuses to turn on if it fails or no allowed algorithm is listed.
- Changing the issuer deletes every `oidc_identities` row of the old issuer, in the save tx, audited with the count.
- Start on another origin than `oidc.server_address`: 303 to `<server address>/login/oidc?next=...`; in `ts_shell=android`, no redirect,
  show `ssoWrongAddress` instead (the app's origin must stay put for `/app/connect/done`).

## Data: `internal/db/migrations/0039_oidc.sql`; `internal/db/db.go` `SchemaVersion = 39`
```sql
-- oidc_identities: a provider account (issuer + sub) linked to one Tendshelf account (decision 0037).
CREATE TABLE oidc_identities (issuer TEXT NOT NULL CHECK (length(issuer) BETWEEN 1 AND 512),
  subject TEXT NOT NULL CHECK (length(subject) BETWEEN 1 AND 255), user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  email TEXT CHECK (email IS NULL OR length(email) <= 254), linked_via TEXT NOT NULL CHECK (linked_via IN ('account','email','new')),
  created_at INTEGER NOT NULL, last_used_at INTEGER, PRIMARY KEY (issuer, subject)) STRICT, WITHOUT ROWID;
CREATE UNIQUE INDEX oidc_identities_user ON oidc_identities(user_id, issuer);
-- oidc_logins: one sign-in under way, 10 minutes. Only SHA-256 of the state is kept; the raw state is the browser's ts_oidc cookie.
CREATE TABLE oidc_logins (id TEXT PRIMARY KEY, state_hash BLOB NOT NULL UNIQUE CHECK (length(state_hash) = 32),
  purpose TEXT NOT NULL CHECK (purpose IN ('signin','link')), issuer TEXT NOT NULL,
  nonce TEXT NOT NULL CHECK (length(nonce) = 43), verifier TEXT NOT NULL CHECK (length(verifier) = 43),
  next TEXT NOT NULL DEFAULT '/' CHECK (length(next) <= 2048),
  user_id TEXT REFERENCES users(id) ON DELETE CASCADE, session_hash BLOB REFERENCES sessions(token_hash) ON DELETE CASCADE,
  subject TEXT, email TEXT, email_verified INTEGER NOT NULL DEFAULT 0 CHECK (email_verified IN (0,1)),
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, used_at INTEGER,
  CHECK ((purpose = 'link') = (user_id IS NOT NULL AND session_hash IS NOT NULL))) STRICT;
CREATE INDEX oidc_logins_expires ON oidc_logins(expires_at);
```
A `link` row is not deleted by the callback: it gets `used_at`, `subject`, `email`, `email_verified` and `expires_at = now + 10 min`, and
`POST /me/sso/confirm` deletes it. A `signin` row is deleted by the callback. Prune `expires_at < now` at every start.

## P1 steps (Go core)
1. Migration above; pin its hash in `internal/db/open_test.go` `pinnedMigrations` (39); the table count at `open_test.go:216` goes `+29` -> `+31` (append
   `oidc_identities, oidc_logins (0039)` to its comment).
   `internal/db/migration_oidc_test.go`: from-previous (`openUpTo(t, 38, dir)`, seed a user + session, migrate, rows survive, both tables
   exist, the CHECKs reject a `link` row without session) and from-empty. Mirror `migration_sign_in_links_test.go`.
2. `internal/auth/oidc/config.go`: `Config{Enabled bool; Name, Issuer, ClientID, ClientSecret, ServerAddress string; MatchEmail,
   CreateAccounts bool; NewRole users.Role}`, `LogValue` (secret as `secret_set`), `LoadConfig(ctx, q db.RowQueryer)`,
   `SaveConfig(ctx, tx, q, by users.User, in ConfigInput, newSecret string, now time.Time) (Config, error)` (returns `users.FieldErrors`
   with codes `required`, `invalid`, `too_long`; name 1-40 runes; issuer and server address parsed by `ParseOrigin`-style rules below;
   client id 1-255), `ClearConfig` (sets `oidc.enabled` false only; keeps the rest), `RedirectURL(c) = c.ServerAddress+"/login/oidc/callback"`.
   Issuer: absolute URL, no query/fragment/userinfo, `https` or `http` with loopback host, kept byte-exact (trailing slash matters).
   Server address: scheme+host[+port] only, lower-cased, default port dropped, no path. Audit `oidc.update` (detail: changed key names,
   `secret_set`), `oidc.off`.
3. `internal/auth/oidc/client.go`: the one `http.Client` (comment names decision 0037): `Timeout: 5s`, `CheckRedirect` returns
   `http.ErrUseLastResponse` (a 3xx is `ErrProviderDown`), no `Jar`. `get/postForm(ctx, origin, url, cap)`: refuse if the URL's origin !=
   origin (`ErrProviderConfig`); `io.LimitReader(cap+1)`, over cap -> `ErrProviderConfig`; non-2xx -> `ErrProviderDown` (5xx, timeout,
   dial error) or `ErrProviderRefused` (4xx from token endpoint); `Content-Type` must start `application/json`.
4. `internal/auth/oidc/discovery.go`: `Discover(ctx, issuer) (Provider, error)` GET `strings.TrimSuffix(issuer,"/")+"/.well-known/openid-configuration"`;
   `issuer` must equal the configured value exactly (`*IssuerMismatchError{Got}`); endpoints rules of 0037 point 3; `response_types_supported`
   contains `code`; `code_challenge_methods_supported`, if present, contains `S256`; `id_token_signing_alg_values_supported` intersects the
   allowlist (`*AlgError{Offered}`). Cache by issuer for 1 h (`Now` injectable). `Origins(p) []string` = issuer and authorize origins, deduped.
5. `internal/auth/oidc/jose.go`: `parseJWKS` (kty RSA/EC/OKP, `use` absent or `sig`, RSA n >= 2048 bits, EC via
   `ecdsa.ParseUncompressedPublicKey`), `verifyJWS(raw string, keys, now)`: 3 parts, size <= 16 KiB, header `alg` in allowlist, `crit`
   refused, key by `kid` (no `kid`: the single key of matching type, else `kid` error); unknown `kid` -> one JWKS refetch if the last was
   >= 5 min ago. Returns the payload bytes.
6. `internal/auth/oidc/token.go`: `Exchange(ctx, p, cfg, code, verifier, nonce string) (Claims, error)`: POST token
   (`grant_type=authorization_code`, `code`, `redirect_uri`, `code_verifier`), require `id_token`, verify, check claims (0037 point 5,
   `Leeway = 2*time.Minute`, `MaxAge = 10*time.Minute`), then userinfo with `Authorization: Bearer` if listed; `sub` mismatch -> reason
   `userinfo_sub`. `Claims{Subject, Email string; EmailVerified bool; Name, PreferredUsername string}` (userinfo values win when present;
   `email_verified` accepted as JSON bool only). Errors: `*TokenError{Reason}`, reasons exactly `malformed alg kid signature issuer audience
   azp expired not_yet issued_at nonce sub userinfo_sub`.
7. `internal/auth/oidc/oidctest/provider.go` (non-test package, imported by tests only): `New(t) *Provider` on `httptest.NewServer`
   (loopback http), RSA-2048 and P-256 keys, endpoints `/.well-known/openid-configuration`, `/authorize` (records params, answers 302 to
   `redirect_uri?code=&state=`), `/token` (checks client auth, single-use code, S256 of verifier, redirect_uri), `/jwks`, `/userinfo`.
   Knobs (fields): `Sub, Email string; EmailVerified bool; Alg string ("RS256","ES256","HS256","none"); Kid string; IssClaim, Aud []string;
   Azp string; NonceOverride string; ExpIn, IatAgo time.Duration; WrongKey bool; UserinfoSub string; Down bool (503); Slow time.Duration;
   HugeBody bool; RedirectDiscovery bool; DiscoveryIssuer string`. `Authorize(authURL) (code, state)` drives `/authorize` like a browser.
8. `internal/auth/oidc_signin.go` (methods on `*Service`; `ServiceOptions` gains `OIDC *oidc.Client`, nil = off):
   - `OIDCStart(ctx, purpose, next string, ip netip.Addr, sess *Session) (authURL, state string, err error)`: limiter `oidcStartLim`
     (30/15 min/IP) -> `*RateLimitedError`; config off -> `ErrOIDCOff`; `link` needs `sess` -> else `ErrOIDCOff`; prune; open rows >= 500 ->
     `ErrOIDCBusy`; `Discover` (errors pass through as `oidc.ErrProviderDown`/`ErrProviderConfig`); insert row (`next` via `SafeNext`).
     Authorize URL params: `response_type=code client_id redirect_uri scope="openid email profile" state nonce code_challenge
     code_challenge_method=S256`.
   - `OIDCFinish(ctx, q OIDCCallback{State, Cookie, Code, Error string}, ip, ua string, prev *Session) (OIDCResult, error)`:
     `ipLim.Allow` first; state/cookie mismatch or no row -> `ErrOIDCFailed` (reason `state`); `Error` set -> `ErrOIDCCancelled` when
     `access_denied`, else `ErrOIDCFailed` (reason `provider_error`); `Exchange`; then for `signin`: identity -> user (disabled ->
     `ErrOIDCFailed`, reason `disabled`); else email match (0037 point 6) -> link `email`; else verified email used by any account ->
     `ErrOIDCEmailTaken`; else create (`CreateInvited` with role `cfg.NewRole`, display name = name, else preferred_username, else
     `"Reader"`, clamped 80 runes; email only if verified) -> link `new`; else `ErrOIDCNoAccount`. One WriteTx: delete `prev`, create session,
     `TouchLastSeen`, `last_used_at`, audits. For `link`: `prev.Hash` must equal the row's `session_hash` (else reason `session`); store the
     claims on the row; result `OIDCResult{LinkPending: rowID}`. `OIDCResult{Login Login; Next string; LinkPending string}`.
   - `OIDCLinkView(ctx, rowID string, sess Session) (email string, err)`; `OIDCConfirmLink(ctx, rowID string, sess Session) error`:
     row must be `link`, used, unexpired, same session -> else `ErrOIDCLinkGone`; identity owned by another user -> `ErrOIDCTaken`; user
     already linked for this issuer -> replace nothing, `ErrOIDCAlready`. Audit `auth.oidc_linked` `via: account`.
   - `OIDCUnlink(ctx, userID) error`: no password -> `ErrOIDCLastWayIn`; audit `auth.oidc_unlinked`. `OIDCIdentity(ctx, userID)
     (oidc.Identity, bool, error)`.
   Errors go in `internal/auth/errors.go`.
9. Wiring: `internal/web/server.go:235` passes `OIDC: oidc.NewClient(oidc.ClientOptions{Now: opts.Now})` in `auth.ServiceOptions`;
   `internal/app/resetpw.go:65` stays without it.
10. `internal/archtest/httpclient_test.go` (new): walk `internal/` and `cmd/` non-test `.go` files; `http.Client{` or `new(http.Client)` only
    under the three allowed paths (invariant 8).
11. `AGENTS.md` and `docs/architecture.md` edits of 0037 "Changes to AGENTS.md"; architecture: tables, routes, the client exception.

## P2 steps (web)
1. `internal/web/gate.go`: public `/login/oidc`, `/login/oidc/callback`, `/login/oidc/origins` (exact paths). `sw.js` PASS already covers
   `/login`; add a test row asserting `/login/oidc/callback` passes.
2. `internal/web/routes_oidc.go` `registerOIDC(mux)` next to `registerSignIn` (`server.go:297`):
   `GET /login/oidc` (start `signin`; set cookie `ts_oidc` Path `/login/oidc/callback`, HttpOnly, Lax, Secure per `proxies.Secure`, MaxAge
   600; 303 to authorize URL), `GET /login/oidc/callback` (clear cookie; signin -> `startSession`, 303 `Next`; link pending -> 303
   `/me/sso/confirm?id=`), `GET /login/oidc/origins` (JSON `{"origins":[...]}`, `Cache-Control: no-store`, `[]` when off or down),
   `GET /me/sso`, `GET /me/sso/link` (start `link`, `next=/me/sso`), `GET /me/sso/confirm`, `POST /me/sso/confirm`, `POST /me/sso/unlink`,
   `GET|POST /admin/sso`, `POST /admin/sso/off` (all three behind `s.allow(w, r, share.ManageServer, share.Target{Kind: share.TargetServer})`).
   Status codes: failed 403, no account 403, email taken 409, cancelled 200, down/config 503, busy 503, limited 429 (`tooMany`), off 404.
   Callback errors render the Login page (signin) or `/me/sso` (link) with `FormError`, never a redirect carrying the reason.
3. Pages: `pages/login.templ` `LoginView` gains `SSOName, SSOHref string`; when set, a secondary `ts-btn` link above the form and a
   divider caption `ssoOr`. New `pages/admin_sso.templ` (`AdminSSOView`), `pages/me_sso.templ` (`MeSSOView`: linked line or Link button,
   Unlink form, confirm card). `pages/prefs.templ`: `prefsServerItems` gains `{"sso","Sign-in","/admin/sso"}` when `n.Email` (same owner
   check). `profile.templ` Account menu: `profileMenuLink("/me/sso", ssoMenu, caption)` when the provider is on. `/welcome`: link
   `ssoWelcome` to `/me/sso/link` when on. Person page (`admin_user.templ`): caption `ssoPersonLinked` when linked. Reuse `ts-group`,
   `ts-form`, `caption`, `ts-btn--secondary`; new CSS only `ts-login__or` if no divider exists (grep first).
4. `internal/web/oidc_copy.go`: copy below, each `// NEW`.
5. Tests (section B), a11y rows, `visual_oidc_test.go` (seeded ids, fixed clock) at 390, 768, 1024, 1440: login with button, admin form,
   `/me/sso` linked and unlinked, confirm card. `docs/manual-checks.md` section "S7 sign-in with a provider" (rows below). Roadmap note.

## P3 steps (Android)
`android/shared/.../Navigation.kt`: `decide(...)` gains `signInOrigins: Set<Origin>`; a main-frame http(s) URL whose origin is in the set ->
`Nav.Stay` (sub-frames still `Block`); bundled-host and `/app/connect/done` rules unchanged and checked first. The app fetches
`GET <origin>/login/oidc/origins` with its existing native client at launch, at Connect and on every `onPageFinished` of `/login`; parse in
`android/shared` with `kotlinx-serialization-json` (`SignInOrigins.kt`, the only module allowed it); at most 4 origins, each `https` (or `http` only when equal to the server origin's scheme+host). The bridge's
allowed-origin rule is untouched. `NavigationTest.decideTable` rows: provider origin stays, provider sub-frame blocked, unknown origin
external, provider origin with userinfo trick blocked.

## Copy (exact, P2; `%s` is the provider name unless said)
`ssoButton="Sign in with %s"`, `ssoOr="or use your password"`, `ssoFailed="Tendshelf could not sign you in with %s. Try again, or use your password."`,
`ssoNoAccount="No account here uses this %s sign-in yet. Ask whoever runs this server for an invite, then link %s from your Account."`,
`ssoEmailTaken="An account here already uses this email. Sign in with your password, then link %s from your Account."`,
`ssoDown="%s is not answering right now. Sign in with your password, or try again in a few minutes."`, `ssoCancelled="Sign-in with %s was cancelled."`,
`ssoBusy="Too many sign-ins are under way. Try again in a few minutes."`, `ssoWrongAddress="To sign in with %s in the app, connect it to %s."` (name, address),
`ssoMenu="Sign in with %s"`, `ssoLinkedLine="Linked to %s since %s."` (email or "your %s account", date), `ssoLinkButton="Link %s"`,
`ssoUnlinkButton="Unlink"`, `ssoUnlinkNoPassword="Add a password first, so you can still sign in."`,
`ssoConfirmTitle="Link %s?"`, `ssoConfirmBody="Sign in to Tendshelf with the %s account %s from now on."` (name, email or subject),
`ssoConfirmButton="Link it"`, `ssoTaken="That %s account is already linked to someone else here."`, `ssoLinkGone="This link step ran out. Start again."`,
`ssoWelcome="Use %s instead of a password"`, `ssoPersonLinked="Signs in with %s"`, `ssoSignOutNote="Signing out of Tendshelf does not sign you out of %s."`.
Admin: title `"Sign-in"`; labels `"Provider name"`, `"Issuer address"`, `"Client ID"`, `"Client secret"`, `"This server's address"`,
`"Turn on sign-in with this provider"`, `"Link accounts by email address"`, `"Make an account for new people"`, `"New people start as"`;
hints `"The issuer your provider shows, like https://auth.example.com."`, `"Leave empty to keep the saved secret, or for a public client."`,
`"Give your provider this redirect address: %s"`, `"Only when the provider says the email is verified, and only for members and guests. Owners and admins link from their Account."`,
`"Anyone your provider lets in gets an account. They see nothing until you share a library with them."`,
`"Turning someone off at the provider stops new sign-ins, not the ones already open. Turn them off here too."`; buttons `"Save"`, `"Turn off"`;
errors `"Tendshelf could not read the provider's settings at %s. Check the address."`, `"The provider calls itself %s. Use that address."`,
`"The provider signs with %s, which Tendshelf does not accept. Choose an RSA or EC signing key in the provider."`.

## Acceptance tests
A. `internal/auth/oidc` (table-driven, fake provider, no network): happy path RS256, ES256, EdDSA, PS256; refused `none`, `HS256`, unknown
`kid` after refetch, wrong key, `crit` header, RSA 1024; `iss` wrong, discovery issuer differs (`IssuerMismatchError`), `aud` missing client,
multi-aud without `azp`, wrong `azp`; `exp` past by 3 min (fails) and by 1 min (passes, leeway), `nbf` future, `iat` 11 min old; nonce wrong;
userinfo `sub` differs; token endpoint 400, 503, slow (> 5 s with injected short timeout), body over cap, discovery redirect not followed,
endpoint on another origin refused before any request; JWKS cache (one fetch for two sign-ins; refetch once per 5 min).
B. `internal/auth/oidc_signin_test.go`: state mismatch; missing cookie; replayed callback (second use fails, row gone); expired row (11 min);
nonce from another row; disabled user; unverified email with match on -> no link; email match on -> links member, never owner or admin;
two members with the email -> no link; email taken with create on -> `ErrOIDCEmailTaken`; create on -> member/guest, no grants, no password;
create off -> `ErrOIDCNoAccount`; link from another session -> fails; confirm by another session -> `ErrOIDCLinkGone`; identity of someone
else -> `ErrOIDCTaken`; unlink without password -> `ErrOIDCLastWayIn`; limited address -> 429 before any query; 500 open rows -> busy;
issuer change deletes old identities; audit rows carry no code/state/token (scan `detail_json` for them).
C. `internal/web` httptest: `/login` shows the button only when on; password login still 303 with the provider `Down`; `/admin/sso` 404
for admin, member, guest, contributor, device bearer; secret never in any response body; `/me/sso*` redirect to `/login` signed out and
refuse bearer tokens; `/login/oidc/origins` lists only issuer and authorize origins; `/app/connect` round trip (next preserved through
the callback); `ts_shell=android` on a wrong origin shows `ssoWrongAddress`; archtest (P1 step 10).
D. Gates: `make check`, `make build`, `make generate` clean tree; P3: `./gradlew :shared:jvmTest` (scoped).

## Manual checks (`docs/manual-checks.md`, P2 adds the table)
S7-1 Authelia 4.39 (claims in userinfo): sign in, link, unlink. S7-2 Authentik with a signing key; then with none (HS256 refused in words).
S7-3 Keycloak realm, confidential client: sign in, wrong secret (failed message), provider stopped (password still works). S7-4 Android app
through each provider to `/app/connect` and back. S7-5 Desktop window: provider pages stay in the window, bridge silent on them.

## Out of scope
Several providers; turning passwords off; logout propagation (front/back-channel, RP-initiated); role or group mapping; refresh tokens;
SCIM; HS256; `form_post`; plain-http issuers off loopback; passkeys (S10); admin unlinking another person (disable covers it); emailing.
