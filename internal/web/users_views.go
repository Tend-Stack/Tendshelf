package web

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// liveWindowMs is how recent the end of a listening session must be to read "Listening now":
// twice the gap that joins two saves into one session.
const liveWindowMs = 2 * progress.SessionGapMs

// seenNowMs is how recent a sign of life must be to read "Now".
const seenNowMs = 5 * 60 * 1000

// dayStart is local midnight of t's day.
func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// lastActiveText is the "Last active" cell for someone last seen at lastActiveMs whose last
// listening session ended at lastListenMs (both Unix milliseconds, 0 when never). live is true for
// "Listening now", which the page marks with the animated bars.
func lastActiveText(now time.Time, lastActiveMs, lastListenMs int64) (text string, live bool) {
	nowMs := now.UnixMilli()
	switch {
	case lastListenMs > 0 && nowMs-lastListenMs <= liveWindowMs:
		return usersListeningNow, true
	case lastActiveMs <= 0:
		return usersNever, false
	case nowMs-lastActiveMs <= seenNowMs:
		return usersNow, false
	}
	days := int(dayStart(now).Sub(dayStart(time.UnixMilli(lastActiveMs).In(now.Location()))).Hours()/24 + 0.5)
	switch {
	case days <= 0:
		return usersToday, false
	case days == 1:
		return usersYesterday, false
	case days < 7:
		return fmt.Sprintf("%d days ago", days), false
	case days < 14:
		return usersLastWeek, false
	case days < 60:
		return fmt.Sprintf("%d weeks ago", days/7), false
	}
	return usersOverMonth, false
}

// monthText is the listening total of a month: "12 h", "40 min" under an hour, "0 h" for none.
func monthText(ms int64) string {
	if ms <= 0 {
		return "0 h"
	}
	if ms < 3_600_000 {
		return fmt.Sprintf("%d min", max(1, int((ms+30_000)/60_000)))
	}
	return fmt.Sprintf("%d h", int((ms+1_800_000)/3_600_000))
}

// errUntilDay is a bad or past "Ends on" date.
var errUntilDay = errors.New("web: the end day is not a day after today")

// parseUntil reads the "Ends on" field (YYYY-MM-DD, local time). A grant ends at the start of the
// next local day, so the chosen day is the last one it works. Empty means no end (0). A bad date,
// or one whose end has passed, is errUntilDay.
func parseUntil(s string, now time.Time) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, now.Location())
	if err != nil {
		return 0, errUntilDay
	}
	end := t.AddDate(0, 0, 1)
	if !end.After(now) {
		return 0, errUntilDay
	}
	return end.UnixMilli(), nil
}

// untilValue is the form value (YYYY-MM-DD) of a grant's end: the last day it works. "" for none.
func untilValue(endMs int64, loc *time.Location) string {
	if endMs == 0 {
		return ""
	}
	return time.UnixMilli(endMs - 1).In(loc).Format("2006-01-02")
}

// renewEnd is where "Renew for 30 days" puts the end: the start of the local day 31 days from
// today, so today plus 30 whole days still works.
func renewEnd(now time.Time) int64 {
	return dayStart(now).AddDate(0, 0, 31).UnixMilli()
}

func roleWord(r users.Role) string {
	switch r {
	case users.RoleOwner:
		return usersRoleOwner
	case users.RoleAdmin:
		return usersRoleAdmin
	case users.RoleMember:
		return usersRoleMember
	}
	return usersRoleGuest
}

func personName(u users.User) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}

// usersBooksText is "1 book" or "{n} books".
func usersBooksText(n int) string { return countText(n, "1 book", "%d books") }

// accessText is the "Can listen to" cell. Owners and admins see everything by role.
func accessText(p share.Person) string {
	switch {
	case p.User.Role == users.RoleOwner || p.User.Role == users.RoleAdmin:
		return usersEveryone
	case p.Grants == 0:
		return usersNothingShared
	case p.ActiveGrants == 0:
		return usersAccessEnded
	case p.Everything:
		return usersEveryone
	}
	name := p.FirstName
	if name == "" && p.FirstScope == share.GrantAll {
		name = usersEveryone
	}
	if p.ActiveGrants > 1 {
		name = fmt.Sprintf("%s and %d more", name, p.ActiveGrants-1)
	}
	return name + " · " + usersBooksText(p.Books)
}

// subtitleText is "5 people · 1 invite waiting · 3 listening this week", zero segments dropped.
func subtitleText(people, waiting, listening int) string {
	parts := []string{countText(people, "1 person", "%d people")}
	if waiting > 0 {
		parts = append(parts, countText(waiting, "1 invite waiting", "%d invites waiting"))
	}
	if listening > 0 {
		parts = append(parts, fmt.Sprintf("%d listening this week", listening))
	}
	return strings.Join(parts, " · ")
}

// usersView builds the page from the people list. actor is the signed-in person; manage says
// whether they may manage a person of that role (share.Can, asked by the caller).
func usersView(csrf string, now time.Time, actor users.User, list []share.Person, waiting int, manage func(users.User) bool) pages.UsersView {
	weekAgo := now.UnixMilli() - 7*24*3_600_000
	listening := 0
	rows := make([]pages.UserRow, 0, len(list))
	for _, p := range list {
		if p.LastListen >= weekAgo {
			listening++
		}
		text, live := lastActiveText(now, p.LastActive, p.LastListen)
		row := pages.UserRow{
			ID: p.User.ID, Name: personName(p.User), RolePill: roleWord(p.User.Role), RoleKind: string(p.User.Role),
			Access: accessText(p), LastActive: text, Live: live, Month: monthText(p.MonthMs),
			Devices: fmt.Sprint(p.Devices), You: p.User.ID == actor.ID, Off: p.User.Disabled,
		}
		if p.User.ID != actor.ID && manage(p.User) {
			row.Href = "/admin/users/" + p.User.ID
		}
		rows = append(rows, row)
	}
	return pages.UsersView{
		CSRF:      csrf,
		Subtitle:  subtitleText(len(list), waiting, listening),
		TabPeople: fmt.Sprintf("People · %d", len(list)),
		Rows:      rows,
	}
}

// addDevices fills the Devices tab: the count is the sum of every person's live browsers and apps,
// the rows follow the People order, and a row carries a sign-out only for a person the viewer may
// manage (never themself: their own are on their Profile).
func (s *server) addDevices(ctx context.Context, v *pages.UsersView, list []share.Person, actor users.User, manage func(users.User) bool) error {
	sessions, err := s.sessions.ListAll(ctx, [32]byte{})
	if err != nil {
		return err
	}
	apps, err := s.devices.ListAll(ctx)
	if err != nil {
		return err
	}
	bySession := map[string][]auth.SessionInfo{}
	for _, si := range sessions {
		bySession[si.UserID] = append(bySession[si.UserID], si)
	}
	byApp := map[string][]auth.Device{}
	for _, d := range apps {
		byApp[d.UserID] = append(byApp[d.UserID], d)
	}
	now := s.now()
	total := 0
	for _, p := range list {
		id := p.User.ID
		if len(bySession[id])+len(byApp[id]) == 0 {
			continue
		}
		base := "/admin/users/" + id
		rows, _ := deviceRows(now, bySession[id], byApp[id], base, true)
		g := pages.UsersDevices{Name: personName(p.User)}
		switch {
		case id == actor.ID:
			g.Name += " (you)"
			g.Note = usersDevicesOwn
			for i := range rows {
				rows[i].Action = ""
			}
		case !manage(p.User):
			for i := range rows {
				rows[i].Action = ""
			}
		}
		g.Rows = rows
		total += len(rows)
		v.DeviceGroups = append(v.DeviceGroups, g)
	}
	if total > 0 {
		v.TabDevices = fmt.Sprintf(usersTabDevices, total)
	}
	return nil
}

// grantRows turns a person's grants into the editor rows.
func grantRows(list []share.NamedGrant, now time.Time) []pages.GrantRow {
	out := make([]pages.GrantRow, 0, len(list))
	for _, g := range list {
		row := pages.GrantRow{ID: g.ID, Name: g.Name, Download: g.CanDownload, Read: g.CanRead,
			Until: untilValue(g.ExpiresAt, now.Location()), Expired: !g.Active(now.UnixMilli())}
		switch g.Scope {
		case share.GrantAll:
			row.Name, row.Kind = usersEveryone, usersEverythingKind
		case share.GrantLibrary:
			row.Kind = "Library · " + usersBooksText(g.Books)
		case share.GrantShelf:
			row.Kind = g.OwnerName + "'s shelf · " + usersBooksText(g.Books)
		default:
			row.Kind = usersBookKind
		}
		switch {
		case g.ExpiresAt == 0:
			row.Ends = usersNoEnd
		case row.Expired:
			row.Ends = "Ended " + time.UnixMilli(g.ExpiresAt-1).In(now.Location()).Format("2 Jan")
		default:
			row.Ends = "Until " + time.UnixMilli(g.ExpiresAt-1).In(now.Location()).Format("2 Jan")
		}
		out = append(out, row)
	}
	return out
}
