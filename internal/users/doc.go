// Package users owns the users table: accounts and roles (owner, admin, member,
// guest), argon2id password hashing and the validation of new accounts.
//
// Hashing is the only place Tendshelf touches a password. At most
// Options.MaxHashers hashes run at once (each one needs about 19 MiB), and a hash
// never runs inside a write transaction. Devices and per-user settings arrive
// with later packages; see docs/architecture.md and docs/roadmap.md.
package users
