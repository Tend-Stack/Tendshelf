package web

import (
	"net/http"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// Settings that save when they change (the Libraries page). The form posts with htmx to its own route and
// targets the "Saved" mark beside it (an element whose id starts with "saved-"); the same form posts
// plainly, with a Save button, when there is no script. One route serves both.

const settingSavedWord = "Saved"

// savedTarget returns the id of the "Saved" mark an htmx request asked to be filled, or "".
func savedTarget(r *http.Request) string {
	if r.Header.Get("HX-Request") != "true" {
		return ""
	}
	t := r.Header.Get("HX-Target")
	if i := strings.LastIndexByte(t, '#'); i >= 0 {
		t = t[i+1:]
	}
	if !strings.HasPrefix(t, "saved-") || len(t) > 80 {
		return ""
	}
	for _, c := range t {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return ""
		}
	}
	return t
}

// answerSetting ends a saved setting: the "Saved" mark for a change that came from the page, otherwise
// the normal answer of a plain post.
func (s *server) answerSetting(w http.ResponseWriter, r *http.Request, loc string) {
	if id := savedTarget(r); id != "" {
		s.fragment(w, r, http.StatusOK, components.SettingSaved(id, settingSavedWord, true))
		return
	}
	s.answerPage(w, r, loc)
}

// refuseSetting answers a setting that was not saved: an out-of-band toast for htmx (which swaps error
// answers), plain words otherwise.
func (s *server) refuseSetting(w http.ResponseWriter, r *http.Request, status int, text string) {
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, status, components.ErrorToast(text))
		return
	}
	http.Error(w, text, status)
}

// lastValue is the last value of a form key: a hidden 0 before a checkbox's 1 makes the last one the answer.
func lastValue(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	return vals[len(vals)-1]
}
