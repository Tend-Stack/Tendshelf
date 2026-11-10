package podnet

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// The starter shows are a list the project curates, not a chart: a head start for a person with nothing followed
// yet. They are only suggestions. Nothing is followed or fetched from a publisher until the person taps Follow,
// which resolves the feed through PodcastFeedURL at that moment (decision 0038, addendum 2026-11-10).

//go:embed starter_shows.json
var starterShowsJSON []byte

// Starter groups, in the order the page draws them.
const (
	StarterTrueCrime = "true-crime"
	StarterMore      = "more"
)

// OndaCastOrigin is what a relative picture address from OndaCast is relative to.
const OndaCastOrigin = "https://ondacast.com"

// StarterShow is one suggested show.
type StarterShow struct {
	Group    string `json:"group"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	ImageURL string `json:"image_url"`
}

// StarterShows returns the curated list, true crime first. A damaged embedded file reads as an empty list.
func StarterShows() []StarterShow {
	var all []StarterShow
	if json.Unmarshal(starterShowsJSON, &all) != nil {
		return nil
	}
	var out []StarterShow
	for _, g := range []string{StarterTrueCrime, StarterMore} {
		for _, s := range all {
			if s.Group == g {
				s.ImageURL = AbsoluteOndaCast(s.ImageURL)
				out = append(out, s)
			}
		}
	}
	return out
}

// AbsoluteOndaCast makes an OndaCast picture address absolute: a path starting with /api/ is relative to
// ondacast.com. Anything else is returned as it came.
func AbsoluteOndaCast(p string) string {
	if strings.HasPrefix(p, "/api/") {
		return OndaCastOrigin + p
	}
	return p
}
