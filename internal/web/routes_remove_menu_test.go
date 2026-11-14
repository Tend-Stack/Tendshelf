package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestRemoveMenuQuickActions(t *testing.T) {
	p := newRemovePartApp(t)
	page := p.a.do(http.MethodGet, "/books/"+p.book.ID, nil, nil, p.owner).Body.String()
	base := "/books/" + p.book.ID + "/remove"
	for _, want := range []string{moreRemoveText, moreRemoveAudio, moreRemoveWhole, base + "?part=text", base + "?part=audio", base + "?part=whole"} {
		if !strings.Contains(page, want) {
			t.Errorf("the menu lacks %q", want)
		}
	}
	for name, ck := range map[string]*http.Cookie{"member": p.member, "guest": p.guest} {
		if body := p.a.do(http.MethodGet, "/books/"+p.book.ID, nil, nil, ck).Body.String(); strings.Contains(body, base) {
			t.Errorf("%s sees a remove item", name)
		}
	}
	// Each item leads to the confirm page with only that part.
	for part, want := range map[string]string{"audio": bookRemoveAudioButton, "whole": bookRemoveWholeButton, "text": "3 (The Dominican Affair.epub)"} {
		rec := p.a.do(http.MethodGet, base+"?part="+part, nil, nil, p.owner)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("part %s: %d", part, rec.Code)
		}
		for other, label := range map[string]string{"audio": bookRemoveAudioButton, "whole": bookRemoveWholeButton} {
			if other != part && strings.Contains(body, label) {
				t.Errorf("part %s also offers %s", part, other)
			}
		}
	}
	if rec := p.a.do(http.MethodGet, base+"?part=sideways", nil, nil, p.owner); rec.Code != http.StatusNotFound {
		t.Errorf("unknown part: %d", rec.Code)
	}
}
