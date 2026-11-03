package web

import (
	"html"
	"net/http"
	"strings"
	"testing"
)

// panelTop is the part of the options panel above its action buttons: the status lines and the
// "Waiting for you" block. ok is false when the page has no panel.
func panelTop(tab string) (top string, ok bool) {
	start := strings.Index(tab, `id="details-panel-head"`)
	if start < 0 {
		return "", false
	}
	end := strings.Index(tab[start:], "ts-dpanel__acts")
	if end < 0 {
		return tab[start:], true
	}
	return tab[start : start+end], true
}

// TestDetailsPanel: people who may edit see the side panel with the actions on the Details tab;
// members and listeners see none. The status line and the waiting block sit inside it.
func TestDetailsPanel(t *testing.T) {
	l := newWriteLab(t, 1, false)
	for name, cookie := range map[string]*http.Cookie{"owner": l.owner, "admin": l.admin} {
		tab := l.get(cookie, l.tab(), false).Body.String()
		if _, ok := panelTop(tab); !ok {
			t.Fatalf("%s: no options panel", name)
		}
		for _, want := range []string{detailsPanelTitle, writeOpenLink, writeOpenCaption} {
			if !strings.Contains(tab, html.EscapeString(want)) {
				t.Errorf("%s: panel lacks %q", name, want)
			}
		}
	}
	for name, cookie := range map[string]*http.Cookie{"member": l.member, "guest": l.guest} {
		if tab := l.get(cookie, l.tab(), false).Body.String(); strings.Contains(tab, "ts-dpanel") {
			t.Errorf("%s sees the options panel", name)
		}
	}

	l.start(t)
	jobID := l.jobOf(t, l.post(t, l.owner, "/books/"+l.book.ID+"/write"))
	l.waitDone(t, l.owner, jobID)
	top, ok := panelTop(l.get(l.owner, l.tab(), false).Body.String())
	if !ok || !strings.Contains(top, writePendingHead) || !strings.Contains(top, html.EscapeString(writeConfirmBtn)) {
		t.Error("the waiting block is not inside the panel")
	}
	rec := l.post(t, l.owner, "/books/"+l.book.ID+"/write/confirm")
	top, ok = panelTop(l.get(l.owner, location(rec), false).Body.String())
	if !ok || !strings.Contains(top, html.EscapeString(writeKept)) {
		t.Error("the Kept line is not inside the panel")
	}
}
