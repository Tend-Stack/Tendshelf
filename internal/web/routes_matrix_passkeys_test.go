package web

import (
	"net/http"
)

// passkeySweepRows are the route matrix rows of passkeys (decision 0041). The matrix sends forms, and the
// four ceremony routes take JSON only, so each answers 415 whatever the role, signed out or not (the two
// /login/passkey paths are public). The Passkeys page and the removal are the person's own, the same for
// every role; the admin's removal follows the Users area: staff for a member, never for an admin or the owner.
// The ceremonies themselves are covered by passkeys_test.go.
func passkeySweepRows() []sweepRow {
	json := sweepSame(http.StatusUnsupportedMediaType)
	public := func(r sweepRow) sweepRow { r.Anon = http.StatusUnsupportedMediaType; return r }
	staffPost := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	onlyOwner := [4]int{http.StatusSeeOther, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound}
	none := sweepSame(http.StatusNotFound)
	removal := func(label string, path func(*sweepEnv, int) string, want [4]int, who func(*sweepEnv) string) sweepRow {
		r := sweepPost("POST /admin/users/{id}/passkeys/remove", path, nil, want, "")
		r.Label = label
		if who != nil {
			r.LocOf = func(e *sweepEnv) string { return "/admin/users/" + who(e) + "?passkeys=removed" }
		}
		return r
	}
	return []sweepRow{
		public(sweepPost("POST /login/passkey/options", sweepStatic("/login/passkey/options"), nil, json, "")),
		public(sweepPost("POST /login/passkey", sweepStatic("/login/passkey"), nil, json, "")),
		sweepGet("GET /me/passkeys", sweepStatic("/me/passkeys"), sweepSame(http.StatusOK)),
		sweepPost("POST /me/passkeys/options", sweepStatic("/me/passkeys/options"), nil, json, ""),
		sweepPost("POST /me/passkeys", sweepStatic("/me/passkeys"), nil, json, ""),
		sweepPost("POST /me/passkeys/{pid}/remove", sweepStatic("/me/passkeys/pk_none/remove"), nil, sweepSame(http.StatusNotFound), ""),
		removal("", func(e *sweepEnv, _ int) string { return "/admin/users/" + e.extra + "/passkeys/remove" }, staffPost, func(e *sweepEnv) string { return e.extra }),
		removal("admin target", func(e *sweepEnv, _ int) string { return "/admin/users/" + e.uid[sweepAdmin] + "/passkeys/remove" }, onlyOwner, func(e *sweepEnv) string { return e.uid[sweepAdmin] }),
		removal("owner target", func(e *sweepEnv, _ int) string { return "/admin/users/" + e.uid[sweepOwner] + "/passkeys/remove" }, none, nil),
	}
}
