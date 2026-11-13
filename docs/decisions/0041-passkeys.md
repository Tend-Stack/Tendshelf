# 0041 Passkeys (WebAuthn) beside passwords
Date: 2026-11-12. Status: accepted and built, 2026-11-13 (S10 of `docs/plans/next-stage.md`). Plan: `docs/plans/passkeys.md`. Parents: OpenID Connect
[0037](0037-openid-connect.md), app sign-in [0026](0026-app-sign-in.md), sign-in links [0030](0030-sign-in-links.md), and "Security and
privacy" in AGENTS.md. The operator approved `go-webauthn` on 2026-11-03 (architecture.md, "Dependencies and approvals").

## Context
`docs/architecture.md` promises "Passkeys (WebAuthn) later, added beside passwords". Sign-in today is the password form on `/login`, an
invite, a sign-in link, an OpenID Connect provider and, for the phone app, the page flow of 0026. What exists and is reused:
`auth.Service` sessions and `Sessions.Create`, the shared address and account limiters, `SafeNext`, the CSRF middleware (it binds a token
to a browser before sign-in), `requestOrigin` and `Proxies.Secure`, `shellFrom`, the `data-ts-module` page-module loader, `share.Can`
with `recheckManage`, and `db.Audit`. Nothing is replaced and no second session system is added.

## Decision
1. **No new module: the standard library**, in one new package `internal/auth/passkey`. A passkey ceremony needs a small strict CBOR
   reader (attestation object, COSE key, extensions), three signature checks (`crypto/ecdsa` ES256, `crypto/rsa` RS256, `crypto/ed25519`
   EdDSA) and the client-data checks of WebAuthn Level 2 sections 7.1 and 7.2. `go-webauthn` would bring a CBOR library, TPM, JWT,
   `mapstructure`, `uuid` and more for attestation formats Tendshelf will not trust. As in 0037, "one binary, nothing to keep up to date"
   decides it. The approval stays open; no `NOTICES.md` row is needed.
2. **Attestation `none`.** Tendshelf asks for no attestation and trusts none: any format is accepted, its statement is read only to
   check it is well formed. A home server has no use for an authenticator allow-list and no trust anchors to keep current.
3. **Algorithms: EdDSA (-8), ES256 (-7), RS256 (-257)**, RSA keys 2048 to 4096 bits, EC points checked on the curve.
4. **The relying-party id is the host name the browser used** (lowercase, no port), and every passkey row stores the id it was made for.
   Tendshelf has no public-address setting, and a passkey is bound to its RP id forever, so a passkey made at `books.example` works only
   there. IP-address hosts are refused (WebAuthn does not allow them) and Tailscale names (`*.ts.net`) work like any other name.
   **The Host header need not be trusted:** the browser checks the RP id against its real origin and puts that origin in the signed
   client data. The server still checks that the client-data origin equals this request's origin exactly, that the authenticator data's
   RP id hash matches the stored RP id, and that the row's RP id is this host's. A forged Host yields only a ceremony no browser
   completes. A reverse proxy must pass the Host header through; one that rewrites it to `127.0.0.1` turns passkeys off there.
5. **Secure context only.** Passkeys are offered over https (directly or through a trusted proxy) and on `localhost`. Over plain http on
   a LAN address `/login` shows no button and the account page says why in plain words.
6. **User verification required** and **discoverable credentials required**: the passkey is the whole sign-in, with no username and no
   password, so the device's screen lock must be checked. A security key without a PIN cannot be added; the copy says so.
7. **Challenges** are 32 random bytes, kept only as SHA-256 in `passkey_ceremonies`, single use, 5 minutes. The row is found by the hash
   of the challenge the browser returns, so no extra cookie is needed; the finishing POST carries the CSRF token, which stops a
   cross-site page from signing a browser in. A registration row is bound to the session that started it. At most 500 open rows and 30
   starts per address in 15 minutes; every failed finish costs the address one try on the shared sign-in limiter, checked before the
   database is read.
8. **User handle**: a random 32-byte `users.passkey_handle`, made at the first passkey, never the account id or name.
9. **Adding a passkey needs the current password**, or for an account without one a session started in the last 15 minutes. A stolen
   cookie alone must not add a lasting way in.
10. **Sign count and copies.** When either count is non-zero and the new count does not grow, the sign-in is refused and the passkey is
    marked "may have been copied"; synced passkeys that always report 0 are fine. Backup eligibility may not change; the backup state
    is updated. The list shows "Synced across your devices" or "On one device", in words.
11. **The same sign-in.** A passkey sign-in ends in the usual `sessions` row and cookie, refuses a turned-off account with the same
    answer as an unknown passkey, follows `SafeNext`, and is audited `auth.login` (`via: passkey`). Failures are `auth.passkey_failed`
    (`reason`); also `passkey.added`, `passkey.removed`, `passkey.removed_all`, `passkey.copied`. No challenge, signature or credential id
    is logged or audited.
12. **Lifecycle.** Changing the password, signing out everywhere and turning an account off end sessions and keep passkeys (a turned-off
    account's passkeys are refused until it is back on). Removing a passkey ends no session. The last passkey of an account with no
    password and no provider identity cannot be removed. An admin removes all of a person's passkeys from the Account section of that
    person's page, through `share.Can(ManageUsers)` and the in-transaction recheck; a person removes their own only.
13. **Shells.** The Android WebView and the WebKitGTK desktop window do not offer WebAuthn reliably, so the server renders no passkey
    control when the window is marked as a shell, and the page script shows the button only where `PublicKeyCredential` exists in a
    secure context. Native Credential Manager support is a later decision.
14. **Script.** One ES module, `static/js/passkey.js`, loaded only on `/login` and `/me/passkeys` through `data-ts-module`, readable and
    never minified, under its own size budget. The CSP is unchanged. The JSON ceremony endpoints are plain POSTs under `/login/passkey`
    and `/me/passkeys`; only the two sign-in paths are reachable signed out.

## Security invariants
- A passkey signs in only the account whose handle and credential it carries, on the host it was made for, with a fresh unused
  challenge, user verification and a valid signature.
- A passkey is added only by its own signed-in person, with their password or a fresh session, bound to that session's challenge.
- Nobody removes another person's passkeys except through `share.Can(ManageUsers)`; nobody can remove the last way into an account.
- The password form always stays, so a lost or broken passkey never locks anyone out.

## Not handled, and why
- Native passkeys in the Android app and the desktop window: needs Credential Manager and WebKit work; a later decision.
- Autofill sign-in (conditional mediation), passkeys during onboarding and on `/welcome`, renaming a passkey: later slices; remove and
  add covers renaming.
- Attestation trust and authenticator names (point 2). Related origins across several host names (point 4).
- Turning passwords off and passkeys as a second factor: lockout risk; their own decision.
- Passkey sign-in for `/api/v1/` clients: a device token never comes from a page ceremony (0026).

## Changes to AGENTS.md (applied by the plan)
In the stack list, after "OpenID Connect: standard library, `internal/auth/oidc` (decision 0037)." add "Passkeys (WebAuthn): standard
library, `internal/auth/passkey` (decision 0041)."
