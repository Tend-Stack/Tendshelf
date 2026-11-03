package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// scanJobFor loads a scan job the signed-in person may see: the job must be a library scan and the
// person must be allowed to manage that library. Anything else is "no such job" (ok is false and
// nothing has been answered).
func (s *server) scanJobFor(r *http.Request, id string) (jobs.Job, string, bool, error) {
	j, err := s.jobs.Get(r.Context(), id)
	if errors.Is(err, jobs.ErrNotFound) {
		return jobs.Job{}, "", false, nil
	}
	if err != nil {
		return jobs.Job{}, "", false, err
	}
	libID := jobLibrary(j)
	if j.Kind != scan.KindScan || libID == "" {
		return jobs.Job{}, "", false, nil
	}
	if !s.can.Can(r.Context(), currentUser(r), share.ManageLibraries, share.Target{Kind: share.TargetLibrary, ID: libID, LibraryID: libID}) {
		return jobs.Job{}, "", false, nil
	}
	return j, libID, true, nil
}

// fromSetup reports whether the htmx request came from a setup step, so the status keeps the
// reassurance line while the scan runs.
func fromSetup(r *http.Request) bool {
	cur, err := url.Parse(r.Header.Get("HX-Current-URL"))
	return err == nil && strings.HasPrefix(cur.Path, "/setup/")
}

// jobViews answers GET /hx/jobs/{id} by the kind of the job: each kind has its own status fragment
// and its own rule for who may see it.
var jobViews = map[string]func(s *server, w http.ResponseWriter, r *http.Request, j jobs.Job){
	scan.KindScan:       (*server).getScanJob,
	metadata.KindOnline: (*server).getLookupJob,
}

// getJob answers GET /hx/jobs/{id}. An unknown id and an unknown kind are both "no such job".
func (s *server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.jobs.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, jobs.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "load job", err)
		return
	}
	view, ok := jobViews[j.Kind]
	if !ok {
		s.notFound(w, r)
		return
	}
	view(s, w, r, j)
}

// getScanJob is the scan status fragment, which polls itself until the scan is over and every file
// has been read.
func (s *server) getScanJob(w http.ResponseWriter, r *http.Request, _ jobs.Job) {
	j, libID, ok, err := s.scanJobFor(r, r.PathValue("id"))
	if err != nil {
		s.serverError(w, r, "load job", err)
		return
	}
	if !ok {
		s.notFound(w, r)
		return
	}
	v, err := s.scanView(r.Context(), r, j, libID, fromSetup(r))
	if err != nil {
		s.serverError(w, r, "scan status", err)
		return
	}
	s.fragment(w, r, http.StatusOK, components.ScanStatus(v))
}
