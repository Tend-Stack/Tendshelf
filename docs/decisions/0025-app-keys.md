# 0025 App keys for reading apps
Status: accepted (plan `docs/plans/next-server.md` section 6; the operator approves this decision, which changes one rule in
`AGENTS.md`).

## Context
E-readers and reading apps (KOReader, Kobo-side tools, Moon+ Reader, Foliate and others) speak OPDS for catalogues and
KOReader's own protocol for progress. They sign in by a typed user name and password, on a small or e-ink keyboard. Giving
them the login password would hand a whole account to a gadget; giving them a device token (32 random bytes, base64)
would make it impossible to type. Tendshelf has one place that already lists, expires and revokes an app's credential: the
`devices` table with `/me/devices`.

## Decision
1. **Keys are kinds of device, not a new table.** `devices.kind` gains `opds` and `koreader`; they share the life cycle (hash only,
   last-seen, revoke, 180-day idle expiry, the limit of 50 live devices) and the existing list and sign-out.
2. **A key opens one surface.** An `opds` key opens `/opds/` only; a `koreader` key opens `/kosync/` only. Neither works as a bearer token,
   on a page or under `/api/v1`, and `ParseDeviceKind` does not know them, so `POST /api/v1/auth/token` cannot make one.
3. **A key is 24 characters from `a-z2-7`: 120 random bits, typable.** This is shorter than the 32-byte rule for sessions, invites and
   API tokens, and the exception is deliberate: the key is typed on an e-ink keyboard, KOReader hashes exactly what is typed so no
   separators can be added, and it reaches only two rate-limited, read-mostly surfaces. 120 bits is far beyond any online guess: failures are
   limited to 10 in 15 minutes per client address, and every request counts against a per-device limit. Only the SHA-256 is stored (for
   `koreader`, the SHA-256 of the hex MD5 of the key, because KOReader sends the MD5). The key is shown once, in the answer to the POST.
4. **Basic sign-in names the person too.** The user name must match the key's owner (case-insensitively), so a key in one log line is not enough.
   The login password never opens `/opds` or `/kosync`.
5. **These routes sit beside `/healthz`, outside the cookie chain.** No cookie is read, so a session grants nothing there; there is no
   redirect to `/login` and CSRF does not apply (GET only).
6. **Feeds filter by grants in SQL** with `share.BookFilterFor(DownloadText)`, which agrees with `share.Can`. Only ready, unmissing, unrefused
   ebook files are listed or served; copy-protected files are never offered. Audio is not listed.

## Consequences
Reversible: only new keys have the new shape; removing the feature means revoking them. A migration rebuilds `devices` once (no table
has a foreign key to it), so the Android app's later handoff needs no second rebuild. Out of scope: OPDS 2.0, OPDS-PSE, audio in
feeds, per-shelf feeds, key scopes beyond the two kinds, keys that expire by date.
