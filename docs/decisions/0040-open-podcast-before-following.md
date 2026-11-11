# 0040 Open, browse and play a podcast before following it
Status: accepted and built (2026-11-11; store and permissions, then pages). Parents: [0038](0038-podcasts.md) points 3, 11 and the
first-use addendum, AGENTS.md "Security and privacy". **Amends** those three; the AGENTS.md outbound-request line gains the words
below. Adds migration 0043.

## Context
Search results and the starter lists offered only Follow. A person who wanted to look at a show, read its episodes or hear one had to
follow it first, which saves a show, counts toward the 200 and sends new-episode counts and scheduled refreshes.

## Decision
- A person can tap a search hit or a suggested show to open it. Opening is per person (`podcast_opens`, one row per person and show,
  sibling of `radio_plays`). It is not following: the show is not in the person's shows, the API list, New episodes, the 200 limit,
  scheduled refresh, auto-download or OPML export.
- `POST /podcasts/open` takes a slug only (never an address), so a link or a prefetch can never open or fetch anything. A slug the
  person already follows or opened skips OndaCast and the limiter. Opening queues one feed refresh when the show was never fetched or
  its last fetch is over an hour old, and then only when back-off allows. A show page GET only keeps an existing open alive.
- Follow on an opened show's page posts the show id (the person must already know the show), and deletes that person's open row.
  Follow-only controls (Following, Refresh, Stop following, Settings, "new" badges) are not drawn and their routes answer 404 for a
  non-follower.
- The relation is read inside `share.Can` and in the queue SQL, never from the request, so one person's open never lets another reach
  the show, its poll, art, audio, chapters, episode page, player, progress or queue.
- An open lasts 30 days after its last visit; the prune job removes expired opens and then shows nobody follows, keeps or opened,
  with their cache folders. An open with an episode of that show in Up next is never expired or dropped by the cap.
- Not audited (browsing, like radio plays); Follow stays audited. Android keeps its followed-only lists; its player resumes an
  opened-show episode through the same web handlers. The service worker is unchanged.

### Amended text
- 0038 point 3: "A person may play an episode only of a show they follow or opened themselves (from search or the suggestions, in the last 30 days, or longer while one of its episodes is in their Up next), or one already in their own history, favourites or places; that relation is read inside `share.Can`, never from the request. Opening a show is not following it: it is not listed in the person's shows, gets no new-episode count, scheduled refresh or auto-download, and does not count toward the 200."
- 0038 point 11 addition: "50 opened shows per person (the oldest goes first); opening a show not opened before 60 an hour per person." The cap of 50 does not count opens with an episode in Up next, so a person can hold a few more than 50 rows.
- First-use addendum: "Starter lists and search results are suggestions only: nothing is followed or saved, and nothing is fetched from a publisher, until the person taps. Tapping a show opens it (its feed and picture are fetched from the publisher; nothing is followed); Follow follows it. Both resolve the feed through `PodcastFeedURL` at that moment."

## Consequences
One table, one route, one limiter. Orphan removal and the prune job also check opens. Out of scope: a "recently opened" shelf, opening by RSS
address without following, per-show settings or auto-download for non-followers, native Android browsing, auditing opens.
