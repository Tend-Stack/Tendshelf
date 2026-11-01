// Package share holds Can, the single authorization decision every handler uses, and the
// SQL filters that keep listings and search inside what a person may see.
//
// Until grants arrive (P3) only the owner and admins may do anything on a library or book;
// members may use their own shelves. Grants per library and per book, invitations and share
// links come later; see docs/architecture.md and docs/roadmap.md.
package share
