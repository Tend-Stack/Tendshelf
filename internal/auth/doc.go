// Package auth is sign-in, sessions and first-run setup: session tokens (only their SHA-256
// is stored), the sliding 30-day session, failed-attempt limits per address and per account,
// trusted-proxy address handling, the setup code and the middleware that puts the signed-in
// user into the request context.
//
// Authorization (what a signed-in person may do) is internal/share. Device tokens and API
// authentication arrive with later packages; see docs/architecture.md and docs/roadmap.md.
package auth
