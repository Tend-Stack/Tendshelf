package web

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// The folder browser is a mode of the two folder form pages (?browse=<path>; an empty value shows
// the starting places). It only lists folder names: the chosen path still goes through
// checkFolderPath when the form is submitted, and nothing here writes anything.
const (
	addFolderURL   = "/admin/libraries/folders/new"
	setupFolderURL = "/setup/folder"
	maxBrowseQuery = 4096
)

// folderStyles are the stylesheets of the Add a folder page, which carries the Browse link.
var folderStyles = append(append([]string{}, adminStyles...), "css/pages-folders.css")

// folderSheetStyles are what the folder browser sheet links inside itself.
var folderSheetStyles = []string{"css/pages-folders.css"}

// browseFieldURL is the link behind the Browse button: it opens the browser at the typed path when
// that is absolute, and at the starting places otherwise.
func browseFieldURL(formURL, typed string) string {
	typed = strings.TrimSpace(typed)
	if !filepath.IsAbs(typed) || utf8.RuneCountInString(typed) > maxBrowseQuery {
		typed = ""
	}
	return formURL + "?browse=" + url.QueryEscape(typed)
}

// folderPrefill is the folder picked in the browser (?folder=), cleaned, or "" when there is none
// or it is not an absolute path. It only fills the field; the form still validates on submit.
func folderPrefill(r *http.Request) string {
	p := strings.TrimSpace(r.URL.Query().Get("folder"))
	if p == "" || utf8.RuneCountInString(p) > maxBrowseQuery || !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Clean(p)
}

// fbPlaces is the list of starting places that exist on this computer.
func fbPlaces() []scan.Place {
	home, _ := os.UserHomeDir()
	return scan.Places(runtime.GOOS, home, func(p string) bool {
		fi, err := os.Stat(p)
		return err == nil && fi.IsDir()
	})
}

// dataFolderHider returns the scan.ListFolders hide function for a listing of path: it hides the
// data folder, however the listed path reaches it.
func (s *server) dataFolderHider(path string) func(string) bool {
	if s.dataDir == "" {
		return nil
	}
	data, err := filepath.Abs(s.dataDir)
	if err != nil {
		return nil
	}
	dataReal := data
	if r, err := filepath.EvalSymlinks(data); err == nil {
		dataReal = filepath.Clean(r)
	}
	base, err := filepath.EvalSymlinks(path)
	if err != nil {
		base = path
	}
	return func(full string) bool {
		return filepath.Clean(full) == data || filepath.Join(base, filepath.Base(full)) == dataReal
	}
}

// folderBrowser builds the browser for a request, or reports false when the query has no browse
// key. formURL is the page the browser belongs to. The caller sets Sheet.
func (s *server) folderBrowser(r *http.Request, formURL string) (components.FolderBrowserView, bool) {
	q := r.URL.Query()
	if _, ok := q["browse"]; !ok {
		return components.FolderBrowserView{}, false
	}
	v := components.FolderBrowserView{FormURL: formURL}
	raw := strings.TrimSpace(q.Get("browse"))
	if raw == "" || utf8.RuneCountInString(raw) > maxBrowseQuery || !filepath.IsAbs(raw) {
		v.Places = fbPlaces()
		return v, true
	}
	p := filepath.Clean(raw)
	l, err := scan.ListFolders(r.Context(), p, s.dataFolderHider(p))
	v.Path = p
	if parent := filepath.Dir(p); parent != p {
		v.Parent = parent
		v.UpURL = v.BrowseLink(parent)
	}
	switch {
	case errors.Is(err, scan.ErrNotFolder):
		v.Message = msgBrowseOpen
		v.Places = fbPlaces()
		return v, true
	case errors.Is(err, scan.ErrTooSlow):
		v.Message = msgBrowseSlow
		v.PlacesURL = v.BrowseLink("")
		return v, true
	case err != nil:
		v.Message = msgBrowseOpen
		v.Places = fbPlaces()
		return v, true
	}
	v.Path, v.PlacesURL = l.Path, v.BrowseLink("")
	v.PickURL = formURL + "?folder=" + url.QueryEscape(l.Path)
	if l.TooMany {
		v.Message = msgBrowseTooMany
		return v, true
	}
	for _, name := range l.Folders {
		v.Folders = append(v.Folders, components.FolderLink{Name: name, URL: v.BrowseLink(filepath.Join(l.Path, name))})
	}
	if len(v.Folders) == 0 {
		v.Empty = msgBrowseEmpty
	}
	v.AudioHere = audioHereText(l.AudioHere)
	return v, true
}
