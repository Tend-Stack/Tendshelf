# 0030 Sign-in links: a way back in for people without a password, or who forgot it
Status: proposed (operator request, 2026-11-07). Plan: `docs/plans/sign-in-links.md`. Parents: the M2 Family invites (`internal/auth/invite.go`,
`internal/share/invites.go`), the QR pages of [0028](0028-qr-sign-in.md), and the session rules in AGENTS.md "Security and privacy".

## Context
An invited person can choose "keep your place on this device" on `/welcome` and never set a password. When that session ends (30-day sliding TTL)
or they use another browser, they cannot get back in: `/login` refuses an account with no password hash, `/me/password` needs the current password,
and the only reset is `tendshelf reset-password` on the server's command line. A person who forgot their password has the same dead end. The roadmap
records this as reported (the "invite and QR" status note).

What exists and is reused: the invite-accept transaction (end the browser's session, start one, audit `auth.login`), the IP limiter behind
`InviteAllowed`/`InviteFailed`, `share.Can(ManageUsers, TargetUser)` and `recheckManage` on the person page, `InviteLinkPanel` and `qrView`/`reachableHost`
for "shown once, with a QR", `inviteHeaders` and `InviteState` for public token pages, `/welcome` with `SetFirstCredentials`, and
`Sessions.EndAllForUser` (ends sessions and revokes device tokens). Nothing here adds a parallel login system.

## Decision
1. **A sign-in link is made by someone who manages the person**, from the Account section of `/admin/users/{id}`, under the existing rules: the owner
   for anyone else, an admin for members, listeners and contributors. Nobody makes one for themselves, and nobody makes one for the owner; the owner's
   recovery stays the command line. A disabled account gets none.
2. **Its own table, `sign_in_links`**, not `invites` (an invite row would show as a pending invite and could create an account). The token is 32 random
   bytes; only its SHA-256 is stored. It works once, for **24 hours**. At most one open link per person (a partial unique index). Making a new link,
   turning the account off, or changing the person's role revokes every earlier link of that person.
3. **Shown once**: the address and its QR code (no QR on an address a phone cannot reach, as for invites), and the plain time it stops working. The
   answer is a page, never a redirect, so the token stays out of `Location` and the history. **Not emailed**: the invite mail path is a job kind with
   its own message, outbox and retry rules, and the address on file was typed by whoever sent the invite; a reset sent there is a new trust decision.
4. **Opening it**: `GET /signin/{token}` only checks the link and asks "Sign in as {name}?"; `POST /signin/{token}` spends it, so a link scanner or a
   mail preview cannot use it up. Spending runs in one write transaction like accepting an invite: mark used (only if still open), end the browser's
   current session, start a new one, record that session's hash on the link row, audit. Every way a link can fail (malformed, unknown, used, expired,
   revoked, owner, disabled) gets the same page and status, so the page never tells whether an account exists. Every failure costs the address one
   try on the shared sign-in limiter. A session that belongs to a connected app (`device_id` set) is refused and the link stays unused.
5. **Then a password.** A person with no password lands on the existing `/welcome` (unchanged). A person with a password lands on
   `/me/new-password`, "Choose a new password", which asks for no current password. It is open only to the session the link started
   (`sign_in_links.session_hash`, `ON DELETE SET NULL` to `sessions`), within **one hour** of spending the link, and while that link is not revoked.
   A stolen ordinary session still cannot change credentials: it has no such row. Saving the new password, in one transaction, sets the hash, ends
   every session of the person and revokes their device tokens (`EndAllForUser`, as the command-line reset does), and starts one fresh session for
   this browser; deleting the old sessions clears the binding, so the step cannot be repeated.
6. **The person page shows the username**, which the person needs on `/login`, and whether a link is open and until when.
7. **`/login` says where to go**: "No password yet, or forgot it? Ask whoever runs this server for a sign-in link."
8. **Never cached, never by token.** `/signin/` joins the service worker's pass list and the AGENTS.md list; `/signin/{token}` is public like
   `/invite/{token}` (one segment only), and `/me/new-password` sits behind the gate like every `/me` page, so a device token reaches neither.

## Security invariants
- The raw token exists only in the one response that shows it and in the person's hands; logs and audit rows never hold it.
- A link signs in only the person it was made for, only once, only within 24 hours, only while the account is on and not the owner.
- Making a link re-checks `share.Can` on fresh rows inside the write transaction (`recheckManage`); a refusal is 404.
- Changing a password without the current one needs the session a link started, within an hour; nothing else unlocks it.
- A successful reset ends every other session and every device token of the person.
- The public page answers the same way for every failure, and failures are rate limited per address before the database is read.

## Not handled, and why
- Emailing the link (point 3). Add it with its own message if operators ask; the table already holds what a mail job would need except the token.
- A self-service "forgot password" form on `/login`: it needs a mail path and leaks which usernames exist; the admin is the trust anchor here.
- The owner's own recovery stays `tendshelf reset-password`: nobody above the owner can vouch for them.
- Opening a sign-in link inside the Android app: the app's handoff accepts invite and pair links only; the person uses the phone's browser, then
  signs in to the app with the new password.
- `/welcome` keeps today's behaviour (it does not end other sessions); an admin can sign out a person's browsers from the person page.

## Changes to AGENTS.md (applied by the plan, step 14)
In "Security and privacy", in the bullet "Session, invite and API tokens are random 32-byte values", read "Session, invite, sign-in link and API
tokens". In the service worker bullet, in "It never caches or answers `/s/...`, `/invite/...`, `/pair/...`", add `/signin/...` after `/pair/...`.
