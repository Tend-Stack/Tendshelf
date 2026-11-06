package web

import (
	"log/slog"
	"net/http"
	"slices"

	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The optional places of a lookup. Audible and Audnexus are always asked; the person ticks the
// others one by one or "All". The choice is saved per person (user_settings, lookup.v1) and feeds
// both the book details sheet and the Libraries "Look up" button.
const (
	placesLegend = "Also search"
	placesNote   = "Audible is always searched."
	placesAll    = "all"
)

var placeLabels = map[string]string{"openlibrary": "Open Library", "itunes": "Apple Books"}

// placeChoices are the checkboxes for a saved choice. When every place is chosen only "All" is
// ticked, so a plain form (no script) reads the same way it was drawn: All wins, else the ticked ones.
func placeChoices(saved []string) []pages.PlaceChoice {
	saved = metadata.CleanSources(saved)
	all := len(saved) == len(online.ExtraProviders)
	out := make([]pages.PlaceChoice, 0, len(online.ExtraProviders)+1)
	for _, p := range online.ExtraProviders {
		out = append(out, pages.PlaceChoice{Value: p, Label: placeLabels[p], Checked: !all && slices.Contains(saved, p)})
	}
	return append(out, pages.PlaceChoice{Value: placesAll, Label: "All", Checked: all})
}

// postedPlaces reads the checkboxes of a posted form. set is false when the form carried no places
// (an old page), so the saved choice stays as it was. "All" means every place; junk is ignored.
func postedPlaces(r *http.Request) (places []string, set bool) {
	set = r.PostFormValue("places_set") == "1"
	vals := r.PostForm["place"]
	if slices.Contains(vals, placesAll) {
		return slices.Clone(online.ExtraProviders), set
	}
	return metadata.CleanSources(vals), set
}

// savedPlaces is the signed-in person's saved choice; a read failure is logged and means none.
func (s *server) savedPlaces(r *http.Request) []string {
	if s.details == nil {
		return nil
	}
	p, err := s.details.LoadSources(r.Context(), currentUser(r).ID)
	if err != nil {
		slog.Warn("load lookup places", "err", err)
		return nil
	}
	return p
}

// rememberPlaces saves a posted choice when the form carried one.
func (s *server) rememberPlaces(r *http.Request, places []string, set bool) error {
	if !set {
		return nil
	}
	return s.details.SaveSources(r.Context(), currentUser(r).ID, places)
}
