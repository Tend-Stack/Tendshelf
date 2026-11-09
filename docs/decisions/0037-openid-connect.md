# 0037 Sign in with an OpenID Connect provider (Authelia, Authentik, Keycloak)
Date: 2026-11-09. Status: proposed (row 12, S7, of `docs/plans/next-stage.md`). Plan: `docs/plans/oidc.md`. Parents: app sign-in
[0026](0026-app-sign-in.md), contributors [0029](0029-contributors.md), sign-in links [0030](0030-sign-in-links.md), and "Security and
privacy" in AGENTS.md. The operator approved "an OpenID Connect client" on 2026-11-03 (architecture.md, "Dependencies and approvals").

## Context
Self-hosters who run Authelia, Authentik or Keycloak expect to sign in with it. Sign-in today is the password form on `/login`, an invite
(`/invite/{token}`), a sign-in link (`/signin/{token}`) and, for the phone app, the page flow of 0026 that ends in `/app/connect`. What exists
and is reused: `auth.Service` sessions, the shared sign-in IP limiter, `SafeNext`, `users.Repo.CreateInvited` (inserts an account without a
password), the `settings` key-value table and the SMTP secret pattern (`internal/share/mailconfig.go`), `share.Can(ManageServer)`, `db.Audit`,
`/welcome`. Nothing is replaced and no second session system is added: a provider sign-in ends in the same `sessions` row as a password.

## Decision
1. **No new module: the client is the standard library**, in one new package `internal/auth/oidc`. The flow needs discovery (one JSON GET),
   a token POST, a JWKS GET, an optional userinfo GET and a compact-JWS check with an allowlist of asymmetric algorithms (RS256/384/512,
   PS256/384/512, ES256/384/512, EdDSA) over `crypto/rsa`, `crypto/ecdsa`, `crypto/ed25519`. `go-oidc/v3` would add three modules
   (`go-oidc`, `go-jose/v4`, `x/oauth2`) for about 500 lines we can test exhaustively against a fake provider; stdlib-first and "one binary,
   nothing to keep up to date" decide it. HS256 and `none` are refused (Authentik's "no signing key" default is HS256: the admin page says to
   choose a signing key). No `NOTICES.md` row is needed; the approval stays open for a later need.
2. **One provider, set up by the owner in the settings UI** (`/admin/sso`, `share.ManageServer`, owner only), not flags or environment:
   name on the button, issuer, client id, client secret, this server's address, and three switches. Keys `oidc.*` in `settings`. The client
   secret follows the SMTP password rule: stored in the settings table, never rendered, logged, audited or put in a job; a blank field keeps
   it. A public client (no secret) is allowed; PKCE protects it.
3. **The redirect address is `<server address>/login/oidc/callback`**, where the server address is an `oidc.server_address` setting
   (scheme, host, port; prefilled from the request). Tendshelf has no public-address setting, and a `Host`-derived URI would change with
   every way in. A browser that starts on another address is first sent to the configured one, so the state cookie and the session land
   on the same origin. The issuer and the token, JWKS and userinfo endpoints must be `https` (loopback excepted) and share the issuer's
   origin; the authorization endpoint must be `https` and may be elsewhere (the server never fetches it).
4. **Code flow with state, nonce and PKCE S256**, scopes `openid email profile`, no `response_mode`. State, nonce and verifier are 32
   random bytes each (base64url, 43 characters). Only SHA-256 of the state is stored; the raw state rides in an `HttpOnly`, `SameSite=Lax`
   cookie `ts_oidc` scoped to the callback path, so a callback URL fed to another browser fails (login CSRF). The row is deleted when the
   callback reads it (single use) and lives 10 minutes. The start is a GET link, not a form: `form-action 'self'` blocks a form whose
   redirect leaves the origin (0026 point 1), and starting a sign-in changes nothing that matters.
5. **ID token checks (OIDC Core 3.1.3.7)**: `iss` equals the discovered issuer exactly, `aud` contains the client id, `azp` equals it when
   present or when `aud` has several values, `exp` in the future, `nbf` (if present) past, `iat` within the last 10 minutes, `nonce` equals
   the row's, signature by a JWKS key whose `kid` matches (or the only key of the right type), RSA keys at least 2048 bits. Clock skew: 2
   minutes on every time check. Claims come from the ID token, then the userinfo endpoint if listed (Authelia 4.39 puts email and name
   there), whose `sub` must equal the token's or the sign-in fails. Access and refresh tokens are dropped after that call.
6. **Matching an account**: by `(issuer, sub)` in `oidc_identities`, never by name. A first sign-in links only by (a) **explicit linking**
   from the person's Account page (`/me/sso`), where the callback shows "Link {provider} account {email} to your Tendshelf account?" and a
   POST confirms it, bound to the session that started it; or (b) **verified email**, only when the owner turns on "Link accounts by
   email", only when the provider says `email_verified: true`, only to exactly one enabled member or guest with that email and no
   identity yet. The owner and admins always link explicitly. An identity belongs to one account and an account has one identity per issuer.
7. **New accounts** only through an invite (accept it, then link on `/welcome` or the Account page) or the owner's switch "Make an account
   for new people" with a default role of member or guest (never admin, never contributor). A new account gets no grants and no password.
   If a verified email already belongs to an account here, no second account is made; the person is told to link from that account.
8. **No group or claim mapping to roles.** Roles stay in Tendshelf, so a provider group change cannot make an admin.
9. **Sessions are Tendshelf's.** A provider sign-in starts the usual 30-day session; it does not follow the provider's session. Sign-out
   ends the Tendshelf session only. Disabling an account in Tendshelf ends its sessions and refuses its provider sign-in; disabling it only at
   the provider stops new sign-ins, not open sessions (the admin page says so).
10. **The password form always stays.** There is no switch to turn passwords off in S7, so the owner can never be locked out by a provider
    that is down or misconfigured. When the provider does not answer, `/login` says so and the password form works as before.
11. **The outbound client is a second, narrow exception** to "only `internal/metadata/online` creates an `http.Client`": `internal/auth/oidc`
    makes one, used only for the configured issuer's discovery document and its discovered token, JWKS and userinfo endpoints (origin
    checked before each request), with a 5-second timeout per request, no redirects followed, no cookies, response caps (64 KiB for
    discovery, JWKS and token, 32 KiB for userinfo), only `application/json` answers, and discovery and JWKS cached for one hour (an unknown
    `kid` refetches JWKS at most once per 5 minutes). Every call is caused by an owner saving the settings or a person pressing the button.
12. **Rate limits and audit.** Starting costs a per-address budget (30 in 15 minutes) and at most 500 open sign-ins exist. Every failed
    callback costs the address one try on the shared sign-in limiter, checked before the database is read. Audit actions: `auth.login`
    (detail `via: oidc`), `auth.oidc_failed` (detail `reason`), `auth.oidc_linked` (`via: account|email|new`), `auth.oidc_unlinked`,
    `oidc.update`, `oidc.off`. No code, token, state, nonce or verifier is ever logged or audited.
13. **The phone app and the desktop window.** Both sign in on the server's page, so 0026 needs no second path: `/app/connect` sends a
    signed-out person to `/login?next=/app/connect?...`, the provider button carries `next`, and the callback lands back on `/app/connect`.
    The Android window keeps other origins out, so it learns the provider's origins from `GET /login/oidc/origins` (public JSON, the issuer
    and authorization endpoint origins, empty when off) and keeps those in the WebView; the message bridge stays bound to the server's origin
    only. The desktop window navigates in place and its bridge already answers only the session's origin (`fromSession`); a manual check
    confirms it.

## Security invariants
- A provider sign-in signs in only the account linked to that `(issuer, sub)`, or one made or matched under points 6 and 7.
- A callback needs the cookie of the browser that started it, a fresh unused row, and an ID token that passes every check in point 5.
- Linking needs the session that started it, a confirming POST with the CSRF token, and an identity that belongs to nobody else.
- The owner and admins are never linked or created by email or by a claim; nobody becomes an admin through the provider.
- The server talks only to the configured issuer's origin, within the limits of point 11.

## Not handled, and why
- Several providers at once: one covers the self-hosting case; the tables already carry the issuer.
- Turning password sign-in off, and "provider only" accounts for the owner: a lockout risk; later, behind its own decision.
- Front-channel, back-channel or RP-initiated logout: sessions are Tendshelf's (point 9).
- Role or group mapping (point 8), SCIM or provisioning, refresh tokens.
- A plain-`http` issuer on a LAN address: the ID token's trust rests on TLS to the token endpoint; loopback only, for tests.
- Providers whose sign-in pages hop through further origins (social logins upstream of the provider) inside the Android app: those hops
  open in the phone's browser; the provider's own pages work.
- HS256 ID tokens (point 1). `response_mode=form_post` (needs a cross-site POST to the callback).

## Changes to AGENTS.md (applied by the plan)
In "Security and privacy", the no-telemetry bullet: after "the only package in the server that creates an `http.Client`" add "(the one
other is `internal/auth/oidc`, which talks only to the configured sign-in provider, decision 0037)", and add "the configured OpenID Connect
provider, when someone signs in with it" to the outbound connections. In the stack list, record "OpenID Connect: standard library,
`internal/auth/oidc` (decision 0037)" next to the approved modules.
