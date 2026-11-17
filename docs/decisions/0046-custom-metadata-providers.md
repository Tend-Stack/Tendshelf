# 0046 Custom metadata providers (your own lookup sites)
Status: accepted (operator, 2026-11-17: "Custom metadata providers: Yes."). Plan: `docs/plans/custom-metadata-providers.md`.
Parents: the online lookup rules in `AGENTS.md` (Security and privacy), [0038](0038-podcasts.md) (the guarded podcast client and its
address rules).

## Context
People who come from Audiobookshelf often run a small lookup service of their own (for a store Tendshelf does not ask, or their own
catalogue), usually on their home network or tailnet. Audiobookshelf calls these custom providers. Each answers
`GET {base}/search?mediaType=book&query=<title>&author=<author>`, optionally with an `Authorization` header, with
`{"matches":[{title, subtitle, author, narrator, publisher, publishedYear, description, cover, isbn, asin, genres, tags,
series:[{series, sequence}], language, duration}]}`. We read that format for understanding only; no Audiobookshelf code is used.

Until now the only lookup destinations were a fixed list of public hosts (`apiHosts` and the cover hosts in
`internal/metadata/online/client.go`), checked by host name with no DNS check. A provider an admin types breaks that model twice: the
host is not on any list, and the common case is a private address, which the podcast client (`podnet`) refuses on purpose.

What exists and is reused: `internal/metadata/online` (the lookup client, spacing, the 429 retry, size caps, the `Error` words, scoring,
the step recorder, the cover cache), `podnet`'s address classification, the per-person "Also search" places (`lookup.v1`,
`internal/web/lookup_places.go`), the bulk and manual lookup jobs, the `online.enabled` switch, `db.Audit`, the job-and-poll pattern of
the email test (`smtp.test`). Nothing parallel is added: custom providers are more places in the same lookup.

## Decision
1. **Who.** Only a person with `share.ManageServer` adds, tests or removes a provider. At most 10. Every person may then tick a provider
   under "Also search" like Open Library; it is asked only while online lookups are on (`online.enabled`). Automatic lookups for new
   books keep asking only the reliable source, as today.
2. **Which package.** The requests are made by `internal/metadata/online`, by a second `http.Client` built in that package
   (`customnet.go`) with its own transport. The list of server packages that create an `http.Client` does not change (`online`,
   `online/podnet`, `internal/auth/oidc`); the list of destinations does: it gains the hosts an admin typed, and cover pictures those
   hosts name. The two clients never share a transport, so a connection opened under one rule is never reused under the other.
3. **What is sent.** Only the book's title (`query`) and, when known, its author (`author`), plus `mediaType=book`. Never the ASIN, ISBN,
   language, file names, the person or any identifier. A query with no title does not ask the provider. The `Authorization` header is
   sent only to the provider's own search address, with the value the admin typed. No cookies (no jar), no `Referer`, no proxy from the
   environment, the fixed User-Agent the other lookups send.
4. **Addresses: the typed host may be private, nothing else may.** Every connection is checked after DNS, on the address actually
   dialled (the podnet technique). Addresses fall into three classes (`podnet.ClassifyAddr`): *public*; *local* (loopback, RFC 1918,
   `fc00::/7`, CGNAT `100.64.0.0/10`, which tailnets use); *refused* (link-local including `169.254.169.254` cloud metadata, unspecified,
   multicast, broadcast, documentation and reserved ranges, site-local and the IPv6 forms that can hide an IPv4 address). For the host the
   admin typed, public and local are allowed. For any other host (a cover picture on another site), only public. Refused is refused
   everywhere. Why this is safe enough: the admin can already reach their own network; the request is a fixed GET with a fixed path
   suffix and two words; the answer is parsed into book fields and never shown raw; errors never carry the address or the body; and the
   ports allowed are 80, 443 and 1024-65535, so mail, SSH and other privileged services cannot be poked. DNS rebinding to a refused
   address fails at dial time.
5. **Plain http only on your own network.** `https://` is always allowed. `http://` is allowed only when every address the typed host
   resolves to is local; an `http://` host that resolves to a public address is refused with "Use an https address for a site on the
   internet", so an authorization value never crosses the internet in clear text. Checked at save for an IP literal and at every dial for
   a name.
6. **Redirects never leave the typed origin.** At most 3; each hop must keep the scheme, host and port of the typed address (so no
   https-to-http downgrade). A cover fetch from another site follows at most 3 redirects, https only, public addresses only.
7. **Limits.** 10 s per request, 2 MiB per answer, at most 5 matches used per search, text fields cut (title, subtitle, publisher and
   series 500 characters, description 20,000), at most 20 genres, one request per second per host, one retry after a 429. Answers are
   not cached (they depend on the authorization value, and the site is the admin's own) and are not counted against the daily cap of
   2,000 requests, which protects public services. Keep-alive is off for this client.
8. **Covers.** A cover URL from a provider is kept when it is `https`, or when it is on the provider's own origin. It is fetched through
   the existing allowlisted path when its host is on the cover list, through the provider rule when it is on a configured provider's
   origin, and otherwise through the public-only rule, and that last case only while at least one provider is configured, so with none
   configured cover fetching behaves exactly as before. Cover URLs reach `FetchCover` only from stored lookup results, never from a
   request; that stays true.
9. **The authorization value is a secret.** Stored as typed in the database (it must be sent; the SMTP password is stored the same way
   and travels in backups the same way). It is never shown back, not even in part: the page says "Authorization header saved". To
   change it, remove the site and add it again. It never goes into a log, an audit row, an error, a job payload or a job result.
10. **Audit.** `custom_provider.add` (name, host, whether a header was given), `custom_provider.remove` (name, host) and
    `custom_provider.test` (name, ok). The host only, never the path (a path can carry a token) or the header.
11. **Identity.** A provider's lookup key is `custom:<id>`. The `online_cache` and `online_usage` CHECK lists stay as they are, since
    custom answers are neither cached nor counted; `book_lookups.provider` has no CHECK and records the key. No table rebuild.

## Not decided here (later, if asked)
Custom providers in automatic lookups for new books; editing a saved provider in place; the `tags` and `duration` fields; podcasts.

## Rule change
`AGENTS.md` (Security and privacy, the outbound list) and `docs/architecture.md` (invariant 5 and the `internal/metadata/online` row) say
that user-initiated lookups may also reach the lookup sites an admin added, under the rules above.
