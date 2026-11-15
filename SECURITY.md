# Security

Tendshelf holds people's books, reading history and sign-ins on a server they run themselves, so a
security problem matters. Thank you for telling us about one.

## Report a vulnerability privately

**Do not open a public issue for a security problem.** A public issue tells everyone before a fix exists.

Use GitHub's private report instead: on <https://github.com/Tend-Stack/Tendshelf>, open the **Security**
tab and choose **Report a vulnerability**. Only the maintainers see it.

Please include:

- the Tendshelf version (`tendshelf -version`) and how it runs (binary, Docker, the desktop app, the
  Android app);
- what an attacker can do, and what they need first (no account, a member account, a share link, the
  same network);
- the steps to see it, as small as you can make them.

Remove anything private before you send it: tokens, invitation and share links, and the address of your
server.

## What to expect

- An answer that the report arrived, within a week.
- A fix in a bug-fix release of the current release line, and a line in `CHANGELOG.md` once it is out.
- Credit in the changelog if you want it.

Tendshelf is a small project with no bounty programme.

## Supported versions

Only the newest release line gets security fixes. Versions are `YEAR.RELEASE.FIX`; upgrade to the newest
`FIX` of the newest `YEAR.RELEASE`.

## What counts

In scope: the server, the web pages it serves, the desktop app and the Android app in this repository.
Examples: reading a book, a title or a count the signed-in person was not granted; turning a device token,
a feed token or an app key into a session; reaching a private network address through podcasts, radio or
metadata look-ups; writing, renaming or deleting a library file outside the two paths described in
`AGENTS.md`.

Not a vulnerability: a server its owner chose to expose without HTTPS, anything that needs the owner's own
account or shell access to the machine, and copy-protected files being refused (that is deliberate).

The rules the code is held to are in [AGENTS.md](AGENTS.md) ("Security and privacy") and
[docs/architecture.md](docs/architecture.md).
