# 0028 Invitations in the app, and QR codes for inviting and signing in
Status: accepted (the operator approved `rsc.io/qr` v0.2.0, 2026-11-06). Plan: `docs/plans/invite-and-qr.md`. Parents: [0024](0024-android-app.md),
[0026](0026-app-sign-in.md). This decision adds one narrow exception to 0026: a `tendshelf:` link may start a flow. It carries no code, and 0026's rule
"no scheme or intent takes a code from outside" stands. It also keeps the rule "a device token never becomes a session".

## Context
The app's first screen asks for a server, so a person who was invited and has no server is stuck. People also want to add a phone without typing a password
on it. The phone app needs two credentials (a WebView session and a device token, 0026), the system has no scanner without Google Play services, a self-hosted
server has no fixed domain, and any app can claim a custom scheme.

## Decision
1. **Every QR holds a plain `https` (or `http`) address on the server**: `/invite/{token}` or `/pair/{token}`. A camera opens it in the browser. So a person
   without the app lands on a page that works, and one QR format serves everyone.
2. **No scanner in the app for now.** The system camera reads the QR and the server's page hands over to the app. No dependency and no camera permission.
   zxing with CameraX adds about 2 MB (estimate), a permission this app otherwise never needs, and a screen an emulator cannot test. It would save two taps
   and change nothing else, because the QR stays the same, so it can be added later without touching this design.
3. **App Links are out.** Verification needs `assetlinks.json` on hosts listed in the manifest at build time, and every server has its own host. An unverified
   `https` filter opens the browser on Android 12+ and shows a chooser on older phones, so it gives nothing and leaks the link to anyone else who registers it.
4. **`tendshelf://open?link=<escaped address>` is the app's only way in from outside, and never appears in a QR, an email or a printed link.** The handover is a
   person's tap on the server's page, on an `intent:` address that names `package=app.tendshelf.android`. Only that package can receive it, so another app that
   claims the scheme never sees the token, and no Tendshelf page or message ever produces a bare `tendshelf:` link for it to catch. Going the other way, anyone
   can send the app a `tendshelf:` link, so the app treats it as untrusted text: it can hold no code, it must pass the strict parser, and nothing moves until the
   person confirms the address in a native dialog.
5. **A QR token grants nothing by itself.** The phone that first claims it (one claim, then the token is spent) receives a 4-digit number on its own screen. The
   person types that number on the device that showed the QR. So approval proves that this phone is in front of this person. One wrong number ends the pairing.
   The claimer gets an HttpOnly, `SameSite=Strict` cookie, because PKCE guards the token but not the session: without the cookie, anyone holding the QR could
   wait for approval and take the session.
6. **3 minutes, absolute; one claim; one live pairing per showing session; ends with that session.** The QR dies once claimed. A claim does not extend it.
7. **The existing flow does the rest.** After approval the phone taps Connect. In one transaction the server creates the session and a `device_codes` row with the
   phone's challenge, and answers the 303 to `/app/connect/done`. From there it is `SwapDeviceCode`, `BindDevice` and the same device row, revoked the same way.
   The session comes from an approved pairing, like an accepted invite, never from a token. The new table holds only what happens before a code exists.
8. **Show the QR on the signed-in device, scan with the phone.** The phone is the one with a camera and the one that needs the credential. See below.
9. **Each person, for themselves.** `/me/devices/add` acts on the caller's own session and takes no user id. An admin cannot add a phone to someone else, and a
   device token cannot reach the route.
10. **A connected phone is never moved silently.** A link for the server it is signed in to is refused with words. A link for another server goes through the
    existing "change server" confirmation, which names what is lost.
11. **The QR is a server-side inline SVG drawn by `rsc.io/qr` v0.2.0** (BSD-3-Clause, standard library only). One `<path>`, no `style=""`, no script, no `data:`; CSS
    draws black on white in all four themes, with a 4-module quiet zone inside the `viewBox`. Writing Reed-Solomon and module placement here is about 300
    lines nobody can check without a second encoder. `skip2/go-qrcode` picks its mask, but is larger and has been quiet since 2020. rsc.io/qr uses mask 0 only,
    which is valid QR; the content is mostly random, so the usual reason to choose a mask matters little.
12. **Plain `http` stays allowed (0024) and is warned about, on both devices and in the native dialog.** A passive sniffer who claims first cannot finish, because
    the number goes to the sniffer's own screen. An active attacker on `http` already wins, as 0026 says.

## Showing the QR, against scanning it from the signed-in device
| | Signed-in device shows, phone scans (chosen) | Phone shows, signed-in device scans |
| --- | --- | --- |
| Camera | On the phone, always | Laptops and desktops often have none; a web page would need a camera prompt and a decoder in JavaScript |
| Attacker's QR | The victim loses nothing to the QR: no credential leaves, and the address is shown first | QRLjacking: scanning the attacker's code signs the attacker's device in, the classic attack on this direction |
| Stolen QR | Useless without the number on the phone in the person's hand | Same risk, with the code on the less trusted screen |
| What the person sees | Their own device, asking about a phone they are holding | A login prompt arriving from a code of unknown origin |

## Threats and answers
**Screenshot, shoulder or screen share:** the thief can only claim. The number lands on the thief's phone, the person's page names an unknown phone, and their
own phone is told the code was used. Worst case is a lost attempt, and "Not my phone" records it. **Fake server QR:** the camera opens the fake page, the fake page
hands the app its own address, and the native dialog shows that exact address, in ASCII because `Origin.parse` refuses any other host, so a lookalike in another
alphabet cannot be spelled. The app sends no credential. The rest is the same risk as typing a wrong address: a fake sign-in page can ask for a password, which is
why the dialog tells people to go on only if they know the address. A connected phone is not moved without a named confirmation and keeps its token out of the new
origin (`ServerGate`). **Attacker's QR on the same server (sign in the victim's phone as the attacker):** the attacker never sees the number, and the phone names the
person on its Connect button. **Real-time call ("read me the number" or "type this number"):** not stopped by the protocol. The page says to type it only if the phone
is in your hand; the phone is listed, audited and revocable. **Malicious app set as the phone's browser:** it sees the address and can claim it, and it can do the same to
any password. Accepted. **Stolen session:** can already mint a token (0026), so the QR adds no power.

## Consequences
Migration `0033_device_pairings`, one module, four public `/pair` routes, a `tendshelf` intent filter, and an `Add a phone` page. Browsers see only the named
additions. A person without a working camera QR reader uses the copyable link and the app's paste box, or a password. Out of scope: an in-app scanner, signing a
browser in by QR, QR on share links, a public-address setting, email or push alerts for a new device.
