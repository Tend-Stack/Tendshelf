package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Library health (S3): one read-only page of the books that need a look. People who manage libraries only; the
// rest get a 404. Every list is filtered in SQL by what the person can reach. Nothing here changes a file or a row.

const (
	healthPageSize = 50 // rows per page of one group
	healthPreview  = 5  // rows per group on the overview
)

func (s *server) registerHealth(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/health", s.getHealth)
}

// healthText is a group's heading and note.
func healthText(group string) (head, note string) {
	switch group {
	case library.HealthMissing:
		return healthMissingHead, healthMissingNote
	case library.HealthRefused:
		return healthRefusedHead, healthRefusedNote
	case library.HealthUnreadable:
		return healthUnreadHead, healthUnreadNote
	case library.HealthNoChapters:
		return healthNoChapHead, healthNoChapNote
	case library.HealthNoCover:
		return healthNoCoverHead, healthNoCoverNote
	case library.HealthNoMatch:
		return healthNoMatchHead, healthNoMatchNote
	}
	return healthPastEndHead, healthPastEndNote
}

// healthRow decides the fix link of a book in a group. lookups says whether the lookups page exists.
func healthRow(group string, h library.HealthRow, lookups bool) pages.HealthRowView {
	row := pages.HealthRowView{Title: h.Title, Href: "/books/" + h.ID}
	fix := func(href, label string) {
		row.FixHref, row.FixLabel, row.Aria = href, label, label+": "+h.Title
	}
	switch group {
	case library.HealthNoChapters, library.HealthChaptersPastEnd:
		fix("/books/"+h.ID+"/chapters", healthEditChapter)
	case library.HealthMissing:
		if h.FolderGone {
			fix("/admin/folders/"+h.FolderID+"/emptied", healthReviewDir)
		} else {
			fix(row.Href, healthOpenBook)
		}
	case library.HealthNoMatch:
		if lookups {
			fix(needsLookHref(h.LibraryID), healthLookItUp)
		} else {
			fix(row.Href, healthOpenBook)
		}
	case library.HealthUnreadable:
		row.Note = healthUnreadable
	default:
		fix(row.Href, healthOpenBook)
	}
	return row
}

func (s *server) getHealth(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	ctx := r.Context()
	sc := s.scope(r)
	lookups, err := s.lookupsOn(r)
	if err != nil {
		s.serverError(w, r, "online setting", err)
		return
	}
	v := pages.AdminHealthView{Title: healthTitle, Lead: healthLead}
	group := r.URL.Query().Get("group")
	if group == "" {
		counts, err := s.lib.Health(ctx, sc, healthPreview)
		if err != nil {
			s.serverError(w, r, "library health", err)
			return
		}
		total := 0
		for _, c := range counts {
			total += c.Count
			if c.Count == 0 {
				continue
			}
			v.Groups = append(v.Groups, healthGroupView(c.Group, c.Count, c.Rows, lookups))
			g := &v.Groups[len(v.Groups)-1]
			if c.Count > len(c.Rows) {
				g.AllHref, g.AllLabel = "/admin/health?group="+c.Group, fmt.Sprintf(healthShowAll, c.Count)
			}
		}
		if total == 0 {
			v.AllClear = healthAllClear
		}
		s.healthRender(w, r, v)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	rows, n, err := s.lib.HealthPage(ctx, sc, group, (page-1)*healthPageSize, healthPageSize)
	if errors.Is(err, library.ErrUnknownHealthGroup) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "library health", err)
		return
	}
	v.Single = true
	v.Groups = []pages.HealthGroupView{healthGroupView(group, n, rows, lookups)}
	v.BackHref, v.BackLabel = "/admin/health", healthBack
	pagesN := max((n+healthPageSize-1)/healthPageSize, 1)
	v.PageText = fmt.Sprintf(healthPageText, min(page, pagesN), pagesN)
	base := "/admin/health?group=" + group + "&page="
	if page > 1 {
		v.PrevHref, v.PrevLabel = base+strconv.Itoa(page-1), healthPrev
	}
	if page < pagesN {
		v.NextHref, v.NextLabel = base+strconv.Itoa(page+1), healthNext
	}
	s.healthRender(w, r, v)
}

func healthGroupView(group string, n int, rows []library.HealthRow, lookups bool) pages.HealthGroupView {
	head, note := healthText(group)
	g := pages.HealthGroupView{ID: group, Head: head, Note: note, Count: strconv.Itoa(n)}
	for _, h := range rows {
		g.Rows = append(g.Rows, healthRow(group, h, lookups))
	}
	return g
}

func (s *server) healthRender(w http.ResponseWriter, r *http.Request, v pages.AdminHealthView) {
	s.prefsPage(w, r, http.StatusOK, healthTitle, "admin-libraries", "libraries", false, pages.AdminHealth(v), nil, adminStyles...)
}
