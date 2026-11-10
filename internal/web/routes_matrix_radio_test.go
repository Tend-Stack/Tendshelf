package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/radio"
)

// seedRadio gives the owner, the admin and the member one shared station and the owner a second one.
func (e *sweepEnv) seedRadio(t *testing.T, fx *podFix) {
	t.Helper()
	ctx := context.Background()
	st := radio.NewStore(e.a.st, nil)
	save := func(streamURL string, people ...int) string {
		var id string
		for _, i := range people {
			s, _, err := st.Save(ctx, e.uid[i], radio.Input{StreamURL: streamURL, Name: "Sweep FM"})
			if err != nil {
				t.Fatal(err)
			}
			id = s.ID
		}
		return id
	}
	e.pod.station = save(fx.audioURL(1), sweepOwner, sweepAdmin, sweepMember)
	e.pod.otherStation = save(fx.audioURL(2), sweepOwner)
}

// radioSweepRows are the radio rows of the route matrix (plan R1). The guest is refused by role; a station
// somebody else keeps answers like an unknown id.
func radioSweepRows() []sweepRow {
	const (
		ok  = http.StatusOK
		nf  = http.StatusNotFound
		see = http.StatusSeeOther
	)
	notGuest := sweepNotGuest(ok, nf)
	ownerOnly := [4]int{ok, nf, nf, nf}
	station := func(prefix, suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return prefix + e.pod.station + suffix }
	}
	otherStation := func(prefix, suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return prefix + e.pod.otherStation + suffix }
	}
	labelled := func(r sweepRow, label string) sweepRow { r.Label = label; return r }
	api := func(r sweepRow) sweepRow { r.Anon = http.StatusUnauthorized; return r }
	apiUnsafe := func(pattern, method string, path func(*sweepEnv, int) string, want [4]int) sweepRow {
		return sweepRow{Pattern: pattern, Method: method, Path: path, Want: want}
	}
	staff415 := sweepNotGuest(http.StatusUnsupportedMediaType, nf)
	rows := []sweepRow{
		sweepGet("GET /radio/live/{stationID}", station("/radio/live/", ""), notGuest),
		labelled(sweepGet("GET /radio/live/{stationID}", otherStation("/radio/live/", ""), ownerOnly), "someone else's station"),
		labelled(sweepPost("/radio/live/{stationID}", station("/radio/live/", ""), nil, [4]int{http.StatusMethodNotAllowed, 0, 0, 0}, ""), "POST"),
		sweepGet("GET /hx/radio/now/{stationID}", station("/hx/radio/now/", ""), notGuest),
		sweepGet("GET /radio", sweepStatic("/radio"), notGuest),
		sweepGet("GET /radio/search", sweepStatic("/radio/search"), notGuest),
		sweepGet("GET /hx/player/radio/{stationID}", station("/hx/player/radio/", ""), notGuest),
		labelled(sweepGet("GET /hx/player/radio/{stationID}", otherStation("/hx/player/radio/", ""), ownerOnly), "someone else's station"),
		labelled(sweepGet("GET /hx/radio/now/{stationID}", otherStation("/hx/radio/now/", ""), ownerOnly), "someone else's station"),
		sweepPost("POST /radio/stations", sweepStatic("/radio/stations"), nil, sweepNotGuest(see, nf), "/radio"),
		sweepPost("POST /radio/stations/{stationID}/remove", station("/radio/stations/", "/remove"), nil, sweepNotGuest(see, nf), "/radio"),
		labelled(sweepPost("POST /radio/stations/{stationID}/remove", otherStation("/radio/stations/", "/remove"), nil, [4]int{see, nf, nf, nf}, "/radio"), "someone else's station"),

		api(sweepGet("GET /api/v1/radio/stations", sweepStatic("/api/v1/radio/stations"), notGuest)),
		api(sweepGet("GET /api/v1/radio/recent", sweepStatic("/api/v1/radio/recent"), notGuest)),
		api(sweepGet("GET /api/v1/radio/live/{stationID}", station("/api/v1/radio/live/", ""), notGuest)),
		api(labelled(sweepGet("GET /api/v1/radio/live/{stationID}", otherStation("/api/v1/radio/live/", ""), ownerOnly), "someone else's station")),
		api(sweepGet("GET /api/v1/radio/now/{stationID}", station("/api/v1/radio/now/", ""), notGuest)),
		api(labelled(sweepGet("GET /api/v1/radio/now/{stationID}", otherStation("/api/v1/radio/now/", ""), ownerOnly), "someone else's station")),
		api(apiUnsafe("POST /api/v1/radio/stations", http.MethodPost, sweepStatic("/api/v1/radio/stations"), staff415)),
		api(apiUnsafe("DELETE /api/v1/radio/stations/{stationID}", http.MethodDelete, station("/api/v1/radio/stations/", ""), sweepNotGuest(http.StatusNoContent, nf))),
		api(labelled(apiUnsafe("DELETE /api/v1/radio/stations/{stationID}", http.MethodDelete, otherStation("/api/v1/radio/stations/", ""), [4]int{http.StatusNoContent, nf, nf, nf}), "someone else's station")),
	}
	for i := range rows {
		if rows[i].Pattern == "POST /radio/stations" {
			rows[i].Form = func(e *sweepEnv, _ int) url.Values { return url.Values{"stream_url": {e.pod.fx.audioURL(1)}} }
		}
	}
	return rows
}
