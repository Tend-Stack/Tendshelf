# 0017 The alignment worker speaks over pipes, not loopback HTTP

Status: accepted (2026-11-04). Plan: `docs/plans/m5-r3-alignment.md`, package P2. Amends point 3 of
[decision 0001](0001-read-along.md) (loopback only, a per-launch token) and the "Owned word timing server"
lifetime reused from TendVocal.

## Context
Decision 0001 described the optional alignment worker as a loopback HTTP server with a per-launch token.
That needs a listening port, a token against other local processes, and an `http.Client` in the server,
which would be a second exception to the rule that only `internal/metadata/online` creates one.

## Decision
`internal/align/runtime` starts the worker as a private child and talks to it over the child's own stdin
and stdout: one request at a time, a JSON header line plus a raw binary body, in both directions
(protocol `tendshelf-emissions-v1`, `docs/read-along.md` section 2 "Worker"). The worker has no listener, no port
and no network path (its sockets are disabled as well). The per-launch nonce of decision 0001 stays: it
travels in the environment, is echoed in the ready line and every response, and binds replies to this launch.
The worker script is embedded in the binary and written to the data folder before launch, so the protocol
and the script always match the binary. The package imports neither `net` nor `net/http` (test-enforced).

## Consequences
- No port to collide with, scan or firewall; no token another local process could try to guess.
- Closing stdin ends the worker even where `Pdeathsig` does not apply (other platforms) or misses.
- Decision 0001 points 1, 2, 4 and 5 are unchanged: the worker is optional, owner-installed, never
  downloaded, and the server builds and runs without it.
- A request cannot be interrupted politely: a deadline or cancellation kills the worker, and the next
  request starts a new one (a few seconds to load the model).

## Rejected
An `http.Client` confined to `internal/align/runtime` as a documented loopback exception: more code, a
listening port, and a second exception to the single-HTTP-client rule, for no benefit over pipes.
