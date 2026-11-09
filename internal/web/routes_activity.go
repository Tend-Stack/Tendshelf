package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Activity (S9): one read-only page that shows audit_log in plain words, newest first, filtered by person and
// kind in SQL. Owner and admins only (the same ManageLibraries check as Library health); everyone else gets a 404.
// Decision: the page never shows an IP address, a token or a file path. Failed attempts are listed without an
// address because the page's job is "who did what", and the address would only help an attacker who reads over a
// shoulder. The detail is read only for the few fields the sentences use (role, via, on/off).

const activityPageSize = 50

func (s *server) registerActivity(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/activity", s.getActivity)
}

// activityIDs are the person and book ids a page of rows names, so the names come in two queries.
type activityIDs struct{ people, books []string }

func (ids *activityIDs) add(r db.AuditRow) {
	for _, id := range []string{r.UserID, strings.TrimPrefix(r.Target, "user:"), detailStr(r.Detail, "user"), detailStr(r.Detail, "by")} {
		if id != "" && !slices.Contains(ids.people, id) {
			ids.people = append(ids.people, id)
		}
	}
	if id := strings.TrimPrefix(r.Target, "book:"); r.Target != "" && !strings.ContainsAny(id, ": ") && !slices.Contains(ids.books, id) {
		ids.books = append(ids.books, id)
	}
}

func inClause(n int) string { return "(" + strings.TrimSuffix(strings.Repeat("?,", n), ",") + ")" }

func (s *server) getActivity(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	person, kind := q.Get("user"), q.Get("kind")
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(page, 1)

	f := db.AuditFilter{UserID: person, Offset: (page - 1) * activityPageSize, Limit: activityPageSize}
	if kind != "" {
		i := slices.IndexFunc(activityKinds, func(k activityKind) bool { return k.Key == kind })
		if i < 0 {
			s.notFound(w, r)
			return
		}
		f.Prefixes = activityKinds[i].Prefixes
	}
	rows, more, err := db.ListAudit(ctx, s.lib.Store().Reader(), f)
	if err != nil {
		s.serverError(w, r, "activity", err)
		return
	}
	people, err := s.activityPeople(ctx)
	if err != nil {
		s.serverError(w, r, "activity people", err)
		return
	}
	var ids activityIDs
	for _, row := range rows {
		ids.add(row)
	}
	names, err := s.activityNames(ctx, ids.people)
	if err != nil {
		s.serverError(w, r, "activity names", err)
		return
	}
	titles, err := s.activityBooks(ctx, r, ids.books)
	if err != nil {
		s.serverError(w, r, "activity books", err)
		return
	}

	v := pages.AdminActivityView{Title: activityTitle, Lead: activityLead, PersonLabel: activityPersonLbl, KindLabel: activityKindLbl,
		AllPeople: activityAllPeople, AllKinds: activityAllKinds, FilterLabel: activityFilterBtn, ListLabel: activityListLabel,
		Person: person, Kind: kind}
	for _, p := range people {
		v.People = append(v.People, pages.ActivityOption{Value: p.id, Label: p.name})
	}
	for _, k := range activityKinds {
		v.Kinds = append(v.Kinds, pages.ActivityOption{Value: k.Key, Label: k.Label})
	}
	if person != "" || kind != "" {
		v.ClearHref, v.ClearLabel = "/admin/activity", activityClear
	}
	now := s.now()
	me := currentUser(r).ID
	for _, row := range rows {
		who := activityWho{Actor: activitySomeone, Target: names[strings.TrimPrefix(row.Target, "user:")], Other: names[detailStr(row.Detail, "user")]}
		if row.UserID != "" {
			who.Actor = nameOr(names[row.UserID], activityGonePerson)
		}
		rv := pages.ActivityRowView{Sentence: activitySentence(row.Action, who, row.Detail)}
		at := time.UnixMilli(row.At).UTC()
		rv.Machine, rv.Ago, rv.Exact = at.Format(time.RFC3339), agoText(now, at), at.Format("2 Jan 2006, 15:04 UTC")
		switch id := strings.TrimPrefix(row.Target, "book:"); {
		case titles[id] != "":
			rv.TargetHref, rv.TargetLabel = "/books/"+id, titles[id]
		case strings.HasPrefix(row.Target, "user:") && names[strings.TrimPrefix(row.Target, "user:")] != "" && strings.TrimPrefix(row.Target, "user:") != me:
			uid := strings.TrimPrefix(row.Target, "user:")
			rv.TargetHref, rv.TargetLabel = "/admin/users/"+uid, names[uid]
		}
		v.Rows = append(v.Rows, rv)
	}
	if len(v.Rows) == 0 {
		v.Empty = activityEmpty
		if person != "" || kind != "" {
			v.Empty = activityEmptyMatch
		}
	}
	v.PageText = fmt.Sprintf(activityPageText, page)
	if page > 1 {
		v.NewerHref, v.NewerLabel = activityHref(person, kind, page-1), activityNewer
	}
	if more {
		v.OlderHref, v.OlderLabel = activityHref(person, kind, page+1), activityOlder
	}
	s.prefsPage(w, r, http.StatusOK, activityTitle, "admin-libraries", "activity", false, pages.AdminActivity(v), nil, adminStyles...)
}

// activityHref is the address of one page of the list with the filters kept.
func activityHref(person, kind string, page int) string {
	v := url.Values{}
	if person != "" {
		v.Set("user", person)
	}
	if kind != "" {
		v.Set("kind", kind)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if len(v) == 0 {
		return "/admin/activity"
	}
	return "/admin/activity?" + v.Encode()
}

type activityPerson struct{ id, name string }

// activityPeople are the people the Person filter offers: everyone who has an account, by name.
func (s *server) activityPeople(ctx context.Context) ([]activityPerson, error) {
	rs, err := s.lib.Store().Reader().QueryContext(ctx, `SELECT id, coalesce(nullif(display_name, ''), username) FROM users ORDER BY 2 COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []activityPerson
	for rs.Next() {
		var p activityPerson
		if err := rs.Scan(&p.id, &p.name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rs.Err()
}

// activityNames maps person ids to names. A removed person is absent from the map.
func (s *server) activityNames(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rs, err := s.lib.Store().Reader().QueryContext(ctx, `SELECT id, coalesce(nullif(display_name, ''), username) FROM users WHERE id IN `+inClause(len(ids)), args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	for rs.Next() {
		var id, name string
		if err := rs.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rs.Err()
}

// activityBooks maps book ids to titles for the books the viewer may see. A book that is gone, or that share.Can
// refuses, is absent, so no title leaks.
func (s *server) activityBooks(ctx context.Context, r *http.Request, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rs, err := s.lib.Store().Reader().QueryContext(ctx, `SELECT id, library_id, title FROM books WHERE id IN `+inClause(len(ids)), args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	u := currentUser(r)
	for rs.Next() {
		var id, lib, title string
		if err := rs.Scan(&id, &lib, &title); err != nil {
			return nil, err
		}
		if title != "" && s.can.Can(ctx, u, share.ViewBook, share.Target{Kind: share.TargetBook, ID: id, LibraryID: lib}) {
			out[id] = title
		}
	}
	return out, rs.Err()
}
