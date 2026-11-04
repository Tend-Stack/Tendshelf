package web

import (
	"net/http"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// prefsNav builds the category list of the preferences window. It asks the same questions the
// sidebar and Profile ask (usersLink, ManageLibraries, ManageServer), so the list only hides links:
// every route behind a link still checks for itself and answers 404 when it must.
func (s *server) prefsNav(r *http.Request, current string, index bool) pages.PrefsNav {
	ctx := r.Context()
	u := currentUser(r)
	server := share.Target{Kind: share.TargetServer}
	showUsers, waiting := s.usersLink(r)
	return pages.PrefsNav{
		Current: current, Index: index,
		UserID: u.ID, Name: u.DisplayName, Role: roleLabel(string(u.Role)),
		Users: showUsers, UsersWaiting: waiting,
		Libraries: s.can.Can(ctx, u, share.ManageLibraries, server),
		Email:     s.can.Can(ctx, u, share.ManageServer, server),
		Backups:   s.can.Can(ctx, u, share.ManageServer, server),
	}
}

// prefsPage renders pane inside the preferences window. sideCurrent is the sidebar's item and
// current the key of the category; index marks /me/settings itself.
func (s *server) prefsPage(w http.ResponseWriter, r *http.Request, status int, title, sideCurrent, current string, index bool, pane, sheet templ.Component, styles ...string) {
	styles = append(append([]string{}, styles...), pages.PrefsStyles...)
	s.pageStyled(w, r, status, title, sideCurrent, pages.PrefsShell(s.prefsNav(r, current, index), pane), sheet, prefsDedupe(styles)...)
}

// prefsDedupe drops repeated stylesheet names, keeping the first of each.
func prefsDedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
