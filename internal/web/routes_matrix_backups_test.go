package web

import (
	"net/http"
	"net/url"
)

// backupsSweepRows are the route matrix rows of the backup routes (M3b-K). All of them are the owner's
// alone (the server manage action): admins, members and guests get a 404.
func backupsSweepRows() []sweepRow {
	page := sweepOwnerOnly(http.StatusOK, http.StatusNotFound)
	post := sweepOwnerOnly(http.StatusSeeOther, http.StatusNotFound)
	schedule := sweepPost("POST /admin/backups/schedule", sweepStatic("/admin/backups/schedule"),
		sweepFormOf(url.Values{"schedule": {"weekly"}}), post, "/admin/backups?saved=1")
	now := sweepPost("POST /admin/backups/now", sweepStatic("/admin/backups/now"), nil, post, "/admin/backups?job=")
	// The redirect names the job the press queued (or joined): the only backup job there is.
	now.LocOf = func(e *sweepEnv) string {
		var id string
		if err := e.a.st.Reader().QueryRow(`SELECT id FROM jobs WHERE kind = 'db.backup' ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&id); err != nil {
			return "no backup job: " + err.Error()
		}
		return "/admin/backups?job=" + id
	}
	return []sweepRow{sweepGet("GET /admin/backups", sweepStatic("/admin/backups"), page), schedule, now}
}
