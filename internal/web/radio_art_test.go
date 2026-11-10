package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/radio"
)

// startRadioRunner runs the real job runner so the station logo job executes for real.
func (w *radioWorld) startRunner() {
	w.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w.t.Cleanup(cancel)
	if err := w.a.runner.Start(ctx); err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() {
		stop, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		w.a.runner.Stop(stop)
	})
}

func (w *radioWorld) artPath(id string) string {
	var p string
	w.a.st.Reader().QueryRow(`SELECT art_path FROM radio_stations WHERE id = ?`, id).Scan(&p)
	return p
}

// TestRadioLogo: adding a station by its OndaCast name fetches the logo through the guarded client in the
// background, the local art route serves it to the person who saved the station (and 404s for anyone else, for a
// bad width and when radio is off), and the pages, the player fragment and the app's API name that route, never
// the logo's own address.
func TestRadioLogo(t *testing.T) {
	w := newRadioWorld(t)
	w.startRunner()
	if rec := w.post("/radio/stations", w.ben, url.Values{"slug": {"cool-fm"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %q", rec.Code, rec.Body)
	}
	var id string
	w.a.st.Reader().QueryRow(`SELECT id FROM radio_stations WHERE ondacast_slug = 'cool-fm'`).Scan(&id)
	waitFor(t, "the logo job", func() bool { return w.artPath(id) != "" })

	rec := w.get("/radio/art/"+id+"?w=160", w.ben)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("ETag") == "" {
		t.Errorf("art: %d %v", rec.Code, rec.Header())
	}
	if rec := w.get("/radio/art/"+id, w.cy); rec.Code != http.StatusNotFound {
		t.Errorf("stranger art: %d", rec.Code)
	}
	if rec := w.get("/radio/art/"+id, w.guest); rec.Code != http.StatusNotFound {
		t.Errorf("guest art: %d", rec.Code)
	}
	if rec := w.get("/radio/art/"+id+"?w=0", w.ben); rec.Code != http.StatusBadRequest {
		t.Errorf("bad width: %d", rec.Code)
	}
	if rec := w.post("/radio/art/"+id, w.ben, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST art: %d", rec.Code)
	}

	// The pages, the player fragment and the API use the local route.
	page := w.get("/radio", w.ben).Body.String()
	if !strings.Contains(page, `/radio/art/`+id+`?v=`) || strings.Contains(page, "logo.test") {
		t.Errorf("Radio page art: %q", page)
	}
	frag := w.get("/hx/player/radio/"+id, w.ben).Body.String()
	if !strings.Contains(frag, `data-cover-url="/radio/art/`+id+`?v=`) || strings.Contains(frag, "logo.test") {
		t.Errorf("player fragment lacks the logo: %q", frag)
	}
	tok := w.bearer(w.benID)
	list := deviceBearerReq(w.a, http.MethodGet, "/api/v1/radio/stations", tok, nil)
	if !strings.Contains(list.Body.String(), `"artUrl":"/api/v1/radio/art/`+id+`?v=`) || strings.Contains(list.Body.String(), "logo.test") {
		t.Errorf("API list: %q", list.Body)
	}
	if art := deviceBearerReq(w.a, http.MethodGet, "/api/v1/radio/art/"+id+"?w=160", tok, nil); art.Code != http.StatusOK || art.Header().Get("Content-Type") != "image/png" {
		t.Errorf("API art: %d %v", art.Code, art.Header())
	}

	// Off is off: the route answers 404 although the file is kept.
	w.fx.enable(t, w.a, false)
	if rec := w.get("/radio/art/"+id, w.ben); rec.Code != http.StatusNotFound {
		t.Errorf("art with radio off: %d", rec.Code)
	}
}

// TestRadioLogoLazy: a station with a logo address but no file yet answers 404 for the picture, queues the
// fetch on that first view, and serves it once the job ran. A station with no logo address stays a plain tile.
func TestRadioLogoLazy(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	if _, err := w.a.st.Writer().Exec(`UPDATE radio_stations SET art_url = ? WHERE id = ?`, w.fx.pub.URL("logo.test", "/x.png"), id); err != nil {
		t.Fatal(err)
	}
	if rec := w.get("/radio/art/"+id, w.ben); rec.Code != http.StatusNotFound {
		t.Fatalf("art before the job: %d", rec.Code)
	}
	if strings.Contains(w.get("/radio", w.ben).Body.String(), "/radio/art/") {
		t.Error("the page links a logo that is not kept yet")
	}
	w.startRunner()
	// The first view queued the fetch (the runner was not running then, so the job waited); now it runs.
	waitFor(t, "the logo job", func() bool { return w.artPath(id) != "" })
	if rec := w.get("/radio/art/"+id, w.ben); rec.Code != http.StatusOK {
		t.Errorf("art after the job: %d", rec.Code)
	}
}

// TestRadioRecentSave: a station played but not saved shows a Save button on the Recent list; it saves by id
// without dialling the station, a stranger or an unknown id gets 404, and the 100 limit holds.
func TestRadioRecentSave(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	if err := w.fx.svc.NotePlay(context.Background(), w.benID, id); err != nil {
		t.Fatal(err)
	}
	if rec := w.post("/radio/stations/"+id+"/remove", w.ben, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", rec.Code)
	}
	page := w.get("/radio", w.ben).Body.String()
	if !strings.Contains(page, `action="/radio/stations/`+id+`/save"`) || !strings.Contains(page, ">Save</button>") {
		t.Fatalf("Recent row lacks Save: %q", page)
	}
	if rec := w.post("/radio/stations/"+id+"/save", w.cy, nil); rec.Code != http.StatusNotFound {
		t.Errorf("stranger save: %d", rec.Code)
	}
	if rec := w.post("/radio/stations/"+id+"/save", w.guest, nil); rec.Code != http.StatusNotFound {
		t.Errorf("guest save: %d", rec.Code)
	}
	if rec := w.post("/radio/stations/rs_nope/save", w.ben, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown save: %d", rec.Code)
	}
	hits := w.fx.hits.Load()
	if rec := w.post("/radio/stations/"+id+"/save", w.ben, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %q", rec.Code, rec.Body)
	}
	if w.fx.hits.Load() != hits {
		t.Error("saving by id dialled the station")
	}
	favs, _ := w.fx.svc.Favourites(context.Background(), w.benID)
	if len(favs) != 1 || favs[0].ID != id {
		t.Fatalf("favourites after save: %+v", favs)
	}
	if strings.Contains(w.get("/radio", w.ben).Body.String(), `/save"`) {
		t.Error("a saved station still offers Save")
	}
	// Saving twice changes nothing.
	if rec := w.post("/radio/stations/"+id+"/save", w.ben, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("save again: %d", rec.Code)
	}

	// A full list refuses.
	for i := 0; i < radio.MaxFavourites; i++ {
		in := radio.Input{StreamURL: w.fx.pub.URL("radio.test", "/full"+strconv.Itoa(i)+".mp3")}
		if _, _, err := w.fx.svc.Save(context.Background(), w.cyID, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.fx.svc.NotePlay(context.Background(), w.cyID, id); err != nil {
		t.Fatal(err)
	}
	if rec := w.post("/radio/stations/"+id+"/save", w.cy, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "100 stations") {
		t.Errorf("save on a full list: %d %q", rec.Code, rec.Body)
	}
}

// TestRadioAPIArtURLFetchable: a station with a logo address but no kept file already names the API art route,
// one with no logo address names none.
func TestRadioAPIArtURLFetchable(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	tok := w.bearer(w.benID)
	if body := deviceBearerReq(w.a, http.MethodGet, "/api/v1/radio/stations", tok, nil).Body.String(); strings.Contains(body, "artUrl") {
		t.Errorf("no logo address, yet artUrl: %q", body)
	}
	if _, err := w.a.st.Writer().Exec(`UPDATE radio_stations SET art_url = ? WHERE id = ?`, w.fx.pub.URL("logo.test", "/x.png"), id); err != nil {
		t.Fatal(err)
	}
	body := deviceBearerReq(w.a, http.MethodGet, "/api/v1/radio/stations", tok, nil).Body.String()
	if !strings.Contains(body, `"artUrl":"/api/v1/radio/art/`+id+`"`) || strings.Contains(body, "logo.test") {
		t.Errorf("fetchable artUrl: %q", body)
	}
	if rec := deviceBearerReq(w.a, http.MethodGet, "/api/v1/radio/art/"+id, tok, nil); rec.Code != http.StatusNotFound {
		t.Errorf("art before fetch: %d", rec.Code)
	}
	if rec := deviceBearerReq(w.a, http.MethodGet, "/api/v1/radio/art/"+id, w.bearer(w.cyID), nil); rec.Code != http.StatusNotFound {
		t.Errorf("stranger API art: %d", rec.Code)
	}
}
