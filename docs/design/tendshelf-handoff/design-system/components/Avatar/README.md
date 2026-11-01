Round avatars with initials, and small outline badges that name a person's role.

## Avatars

`ts-avatar` with `--sm` (32), `--md` (44) or `--lg` (72, serif initials). The background is one of the cover-palette darks, picked by a hash of the user id so it stays stable: `#5a4029`, `#1f4a52`, `#3b2f4a`, `#2e3a2a`, `#8c3b24`, `#5a6b73`. The initials are `#f1e8da` and meet at least 4.5:1 on all six (the slate is the lowest, at 4.6:1). A person can upload a photo; it replaces the initials and keeps the circle. As an image, the avatar gets `role="img"` and the person's name as `aria-label`.

## Roles

- **Owner:** runs the server. Ember outline. Only one.
- **Admin:** can manage users, libraries and metadata.
- **Member:** can listen, download, make shelves and invite listeners to what they can see.
- **Listener:** can listen and download what was shared with them.

Badges appear in Users & invites and the share sheet only, never in the listening UI. They are text, never color alone.
