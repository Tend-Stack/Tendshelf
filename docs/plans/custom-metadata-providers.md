# Plan: custom metadata providers (decision 0046)
Two packages, in order: **P1** server (migration, guard, client, parser, store, test job, docs rule), **P2** web (admin page, routes,
lookup places, activity copy, changelog). Each about 150-200 builder calls. P2 starts from merged P1.

## 1. Invariants
1. Only `share.ManageServer` adds, tests or removes; at most 10 providers; asked only while `online.enabled` is on.
2. A provider is sent only `mediaType=book`, `query=<title>` and, if non-empty, `author=<author>`; no title, no request.
3. Every custom-client connection is checked after DNS on the dialled address: typed host public or local; any other host public only;
   refused class never. `http://` needs every resolved address local. Ports 80, 443, 1024-65535 only.
4. Redirects: at most 3, same scheme+host+port as the typed base. No cookie jar, no proxy, no Referer, keep-alive off, fixed UA.
5. The auth value never appears in a page, log, audit row, error, job payload or job result; only sent as `Authorization` to the search URL.
6. Custom answers are never written to `online_cache` and never counted in `online_usage`. No CHECK constraint changes.
7. With zero providers configured, every existing lookup and cover fetch behaves byte-for-byte as before (existing tests unchanged).

## 2. Decisions
- Key scheme `custom:<id>`, no table rebuild: cache and cap do not apply to custom answers, `book_lookups.provider` has no CHECK.
- Second `http.Client` inside `internal/metadata/online` (own transport): no new outbound package, no shared connection pool.
- Address classes come from `podnet` (export one function), not a copy: one place for the address lists.
- The `online.Client` holds the provider list in memory (`SetCustom`), reloaded by `metadata.Service` at start and after each change:
  one source (the table), and `FetchCover`/`CachedCover`, which see only a URL, can apply the origin rule.
- Test connection is a job polled by htmx, like `smtp.test`: long work never blocks a request.
- Own page `/admin/providers`, linked from the Online section of `/admin/libraries`: that page is already long.

## 3. Steps (P1)
1. `internal/db/migrations/0047_custom_providers.sql` (section 4). `internal/db/db.go`: `SchemaVersion = 47`.
2. `internal/db/migration_custom_providers_test.go` (section 5).
3. `internal/metadata/online/podnet/addr.go`: add `type AddrClass int` with `AddrPublic`, `AddrLocal`, `AddrRefused` and
   `func ClassifyAddr(a netip.Addr) AddrClass` (after Unmap and NAT64 reduction as `checkAddr` does). Local = `IsLoopback`,
   `IsPrivate` (covers `fc00::/7`), `100.64.0.0/10`. Refused = everything else `checkAddr` refuses. Rewrite `checkAddr` as
   `ClassifyAddr(a) == AddrPublic`. Export `func PortOK(p string) bool` from the port rule in `checkURL` and use it there.
4. `internal/metadata/online/customnet.go` (new): `newCustomHTTP(o Options) *http.Client`: `http.Transport{Proxy: nil,
   DisableKeepAlives: true, DialContext: guardedDial, TLSHandshakeTimeout: 10s, ResponseHeaderTimeout: 10s, MaxResponseHeaderBytes:
   64<<10}`, `Jar: nil`, `Timeout: requestTimeout`, `CheckRedirect`. A request carries `dialPolicy{host, scheme string; typed bool}` in its
   context. `guardedDial` resolves itself (`Options.CustomLookup` in tests, else `net.DefaultResolver.LookupNetIP`), classifies every
   address: typed host keeps public+local, other hosts public only; if `scheme == "http"` and any kept address is public, return
   `&Error{Code: "insecure"}`; none kept: `&Error{Code: "blocked"}`; dials the kept address literally with `net.Dialer{Timeout: 10s,
   Control: <re-check the same class rule>}` (`Options.CustomDial` in tests). `CheckRedirect`: >3 hops or, for a typed request, a hop
   whose lowercased scheme, host or port differs from the base, or, for a cover request, a non-https hop: `&Error{Code: "blocked"}`;
   delete `Referer`. `Options` gains `CustomLookup`, `CustomDial`, `CustomRootCAs *x509.CertPool` (tests only).
5. `internal/metadata/online/client.go`: split `fetch` into `fetchWith(ctx, hc *http.Client, rawURL, accept, auth string, limit
   int64)`; `fetch` calls it with `c.hc` and `""`. `Authorization` is set only when `auth != ""`. `Client` gains `chc *http.Client`
   (built in `New`), `custom map[string]CustomProvider` and `customOrder []string` under `c.mu`. `Error.Text` gains: `blocked` "Tendshelf
   will not connect to that address."; `insecure` "Use an https address for a site on the internet. Plain http works only for a site on
   your own network."; `unauthorized` "%s did not accept the authorization header." Update the package comment (two clients).
6. `internal/metadata/online/custom.go` (new): `type CustomProvider struct{ ID, Name, BaseURL, Auth string }`; `type Place struct{ Key,
   Label string }`; `CustomKey(id) = "custom:"+id`; `IsCustomKey(s)` (prefix, then 1-40 of `[0-9A-Za-z_-]`); `(c) SetCustom([]CustomProvider)`;
   `(c) Places() []Place` (Open Library, Apple Books, then customs in given order); `(c) label(p)` (custom name, else `label`);
   `(c) searchCustom(ctx, key, q)`: unknown key returns nil,nil; empty title nil,nil; URL = `strings.TrimSuffix(base,"/") + "/search?" +`
   `url.Values{mediaType, query, author-if-set}.Encode()`; `fetchWith(c.chc, …, "application/json", p.Auth, maxJSONBytes)` with a typed
   policy; status 401/403 `unauthorized`, 404 `bad_answer`, other non-2xx `server`; `Site: p.Name` on every error;
   `parseCustomSearch(raw, key, label, base)`. `(c) TestCustom(ctx, id) (found int, err error)` searches `Query{Title: "The Hobbit",
   Author: "J. R. R. Tolkien"}`.
7. Parser `parseCustomSearch` (in `custom.go`): top level must be an object with a `matches` list, else `bad_answer`. Per match: skip
   without `title`; `author`/`narrator` strings split on `,` and trimmed (`Person{Name}` / names); `publishedYear` via `year(str(v))`;
   `series` first element's `series`/`sequence` via `str`; `genres` strings deduped, max 20; `isbn` `cleanISBN`; `asin` `cleanASIN`;
   `language` `capitalize`; `ID` = ASIN, else ISBN, else ""; `cover` kept if `https://`, or `http://` on the base origin; cuts per
   decision 0046 point 7 with `cutRunes`; `tags`, `duration` ignored; then `capBooks`.
8. `internal/metadata/online/lookup.go`: `ChosenProviders` also keeps well-formed custom keys after the extras, input order, once each,
   at most 10. `LookupWith` plans `p` when `contains(Providers,p) || c.hasCustom(p)` (removed ones drop silently). `searchProvider`:
   custom key goes `rec.begin(p, c.label(p), "", q.titleAsk())` then `searchCustom`. `steps.go`: `rec.plan(planned, c.label)`.
9. `internal/metadata/online/cover.go`: `coverURLOK` becomes `(c) coverAllowed(raw) (route, ok)`: allowlisted host (today's path); a
   configured provider's origin (custom client, typed policy, no auth); any other `https` host with no port or `PortOK`, only while
   `len(c.custom) > 0` (custom client, public policy). `CachedCover` and `FetchCover` use it.
10. `internal/metadata/sources.go`: `CleanSources` keeps custom keys after the extras (same rule as step 8).
11. `internal/metadata/custom_providers.go` (new): `List(ctx) ([]CustomProviderView{ID, Name, Host string; HasAuth bool}, error)`;
    `AddCustomProvider(ctx, CustomProviderInput{Name, Address, Auth}, userID) (string, error)`; `RemoveCustomProvider(ctx, id, userID)`
    (`ErrProviderNotFound`); `reloadCustom(ctx)` called from `SetOnline` and after add/remove; `LookupPlaces() []online.Place`.
    Validation returns `*ProviderFieldError{Field: "name"|"address"|"auth", Code}`: trim; name 1-40 runes `name`, taken (NOCASE)
    `name_taken`; address parse fails or scheme not http/https or no host `address`; userinfo, query or fragment `address_parts`;
    `!podnet.PortOK` `port`; IP literal refused class `blocked`; `http` with public IP literal `insecure`; auth > 1000 bytes or any
    byte outside 0x20-0x7E `auth`; 10 rows `too_many`. Store scheme and host lowercased, path without trailing `/`. Audit per 0046 point 10
    (`Target: "server"`, detail `{name, host, auth: bool}`), same transaction as the write.
12. `internal/metadata/provider_test_job.go` (new): kind `KindProviderTest = "metadata.provider_test"`, payload `{"provider_id"}`, one
    try, result `{"ok":bool,"found":int,"code":string}`; `StartProviderTest(ctx, id, userID)` returns the queued or running job of that
    provider if one exists; audits `custom_provider.test`. Register in `Service.Register`.
13. `bulk.go` and `online.go`: no logic change beyond steps 8/10 (they already pass extras through). Confirm `capReached` and
    `AddOnlineUsage` ignore custom keys (they do: no row, `online.Providers` filter).
14. Docs: `AGENTS.md` outbound paragraph: after "a scheduled refresh only when the owner picks an interval…" sentence group, add "a lookup
    a person starts may also ask the lookup sites an admin added (decision 0046): only the title and author, checked after DNS, private
    addresses only for the address the admin typed, plain http only on the local network". `docs/architecture.md` invariant 5: add
    "and the lookup sites an admin added (decision 0046)"; the `internal/metadata/online` row: replace "The only `http.Client` in the
    program." with "Two `http.Client`s: the allowlisted one and the guarded one for admin-added lookup sites (decision 0046)."
### Steps (P2)
15. `internal/web/providers_copy.go` (new, every string marked `// NEW`): see section 4 copy.
16. `internal/web/pages/admin_providers.templ` (new) + generated file: title, intro, off note when lookups are off, the list (`ts-group`,
    rows: name, caption host and "Authorization header saved", buttons Test and Remove each in its own POST form with `_csrf`, a status
    slot `#provider-test-<id>`), then the add form (three `ts-field`s with labels, hints, `aria-describedby`, field error text,
    password-type input with `autocomplete="off"` for the header), primary "Add site". Follow `admin_email.templ` structure and classes.
17. `internal/web/routes_providers.go` (new): `GET /admin/providers`, `POST /admin/providers` (303 `/admin/providers?added=1`; field
    error 422 re-renders the page with the typed name and address, never the header), `POST /admin/providers/{id}/remove` (303
    `/admin/providers?removed=1`; unknown 404), `POST /admin/providers/{id}/test` (htmx: polling fragment; plain: 303
    `/admin/providers?test=<job>`; unknown 404), `GET /hx/admin/providers/test/{job}` (job must be `metadata.provider_test`, else 404).
    Each starts with `s.allow(w, r, share.ManageServer, share.Target{Kind: share.TargetServer})`. Register next to `/admin/email`.
18. `internal/web/pages/admin_libraries.templ` + `routes_admin.go`: under the Online switch a quiet link to `/admin/providers` labelled
    `providersLink` with `providersCount`; only for ManageServer.
19. `internal/web/lookup_places.go`: `placeChoices(saved []string, places []online.Place)`; "All" ticked when every place key is saved;
    `postedPlaces(r, places)` "all" returns every key; `s.lookupPlaces()` = `s.details.LookupPlaces()` (nil-safe). Update the two
    callers in `routes_details.go` and `routes_lookups.go`. `placeLabels` goes (labels come from `Place`).
20. `internal/web/activity_copy.go`: `custom_provider.add` "%s added a lookup site", `.remove` "%s removed a lookup site", `.test`
    "%s tested a lookup site".
21. `CHANGELOG.md` under `## Unreleased`, `### Added`: "Your own lookup sites: an owner can add lookup sites made for Audiobookshelf
    (address and an optional authorization header); everyone can choose them under Also search." `docs/roadmap.md` status note.

## 4. Data, routes, copy
```sql
-- 0047_custom_providers: lookup sites an admin added (decision 0046). auth_value is a secret: never shown or logged.
CREATE TABLE custom_providers (
  id         TEXT PRIMARY KEY,                       -- db.NewID("cp")
  name       TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 40),
  base_url   TEXT NOT NULL CHECK (length(base_url) BETWEEN 8 AND 500 AND (base_url LIKE 'https://%' OR base_url LIKE 'http://%')),
  auth_value TEXT NOT NULL DEFAULT '' CHECK (length(auth_value) <= 1000),
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL CHECK (created_at > 0)
) STRICT, WITHOUT ROWID;
CREATE UNIQUE INDEX custom_providers_name ON custom_providers(name COLLATE NOCASE);
```
List order: `created_at, id`. Copy (BRAND voice, all NEW): title "Your own lookup sites"; intro "Add a lookup site you run yourself,
such as one made for Audiobookshelf. Tendshelf sends it only a book's title and author."; off "Online lookups are off, so these sites
are not asked. Turn them on in Libraries & scanning."; empty "No sites yet."; Name hint "Shown under Also search."; Address hint "The
address before /search, like https://books.example.org or http://192.168.1.20:3001."; header label "Authorization header (optional)",
hint "Only if the site asks for one. Tendshelf keeps it and never shows it again."; "Add site"; "Site added. Choose it under Also search
when you look up a book."; "Remove"; "Site removed."; "Test"; "Asking %s…"; "%s answered with %d books for a test search."; "%s
answered, but found nothing for a test search."; failure = `online.Error.Text()`; saved mark "Authorization header saved"; link "Your
own lookup sites", count "%d sites" / "1 site" / "None yet". Errors: name "Give the site a name, up to 40 characters."; name_taken "You
already have a site with that name."; address "Enter an address that starts with https:// or http://."; address_parts "Leave out the
user name, the ? part and the # part."; port "Use port 80, 443 or a port from 1024 to 65535."; blocked and insecure = the online texts;
auth "Remove line breaks and keep it shorter."; too_many "You can add up to 10 sites. Remove one first."

## 5. Acceptance tests and gates
P1: `db/migration_custom_providers_test.go` `TestMigrationCustomProvidersFromPrevious` (openUpTo 46 + seed7, users survive, table
empty) and `TestMigrationCustomProvidersFromEmpty` (CHECKs refuse `ftp://x`, 41-rune name, duplicate name in other case).
`podnet/addr_test.go` `TestClassifyAddr` (127.0.0.1, 10.1.2.3, 100.100.1.1, fd00::1, ::ffff:192.168.1.1 local; 169.254.169.254,
fe80::1, 0.0.0.0, 224.0.0.1, 255.255.255.255, 64:ff9b::a9fe:a9fe refused; 8.8.8.8, 2606:4700::1 public); existing podnet tests unchanged.
`online/custom_test.go` (httptest servers on 127.0.0.1 plus `CustomLookup`/`CustomDial` fakes): `TestParseCustomSearch` (table, every
step-7 rule), `TestCustomSendsOnlyTitleAndAuthor` (exact query keys, author omitted when empty, base path kept, header present/absent,
UA, no Cookie after a Set-Cookie), `TestCustomSkipsWithoutTitle`, `TestCustomGuardRefuses` (typed name to 169.254.169.254 blocked; http
name to public insecure; mixed answers insecure; redirect other host, https to http, 4 hops blocked; port 25 blocked), `TestCustomGuardAllows`
(http 127.0.0.1, http name to 192.168.1.5, https TLS server with `CustomRootCAs`), `TestCustomLimits` (2 MiB+1 too_large, 401
unauthorized, 500 server, 429 then 200 retried once), `TestCustomNotCached`, `TestCustomCoverRules`, `TestLookupWithCustom` (removed key
dropped, step label = name, `SentBy` key). `metadata/custom_providers_test.go`: `TestAddCustomProviderValidates` (one row per code),
`TestCustomProviderAuditHidesSecret` (audit, job payload and result never contain the header or the URL path), `TestCustomProvidersReload`,
`TestCleanSourcesCustomKeys`, `TestProviderTestJob`, `TestBulkAsksCustomProvider` (no `online_usage` row for the key).
P2: `web/routes_matrix_providers_test.go` sweep rows for the five routes with the same role expectations as `POST /admin/online`
(member, contributor, signed out, share link refused); `web/providers_test.go` `TestAdminProvidersPageHidesHeader`, `TestAdminProvidersAddErrors`
(422 per code), `TestAdminProvidersRemoveUnknown404`, `TestAdminProvidersTestPoll`, `TestLookupPlacesIncludeCustom` (details sheet and
Libraries Look up list it; All includes it; removed one not shown); `activity_copy` cases. Capture `/admin/providers` at 390, 768, 1024,
1440 in all four themes (no board; follows the Email page).
Gates: `make generate` clean, `make check`, `make build`; P2 also the page-weight and a11y browser checks with `TENDSHELF_TEST_BROWSER` set.

## 6. Out of scope
Custom providers in automatic lookups for new books; editing a provider in place (remove and add); `tags`, `duration`; caching or a
daily cap for custom sites; podcasts and radio; translations (no message catalogs in this app).
