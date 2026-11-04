//go:build dev

package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/backups"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestBackupsVisual captures the Backups page at 390, 768, 1024 and 1440 px in dark and light, empty,
// with three copies, and with a failed backup. The page has no board (it is built from the Email
// page's parts), so the captures are compared with the Email page for pattern. The clock and the data
// folder are fixed so the captures repeat.
func TestBackupsVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	clock := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	folder := visualTempDir(t)
	a := newTestApp(t, func(o *Options) {
		o.DataDir = folder
		o.Now = func() time.Time { return clock }
	})
	owner := a.as(t, users.RoleOwner)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		r.AddCookie(owner)
		a.h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)

	shoot := func(state string) {
		for _, sh := range visualMatrix(false) {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", state, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(srv.URL + "/admin/backups?theme=" + theme)
				tab.WaitFor(`document.querySelector(".ts-backups") !== null`, 10*time.Second)
				var m struct {
					Over       bool     `json:"over"`
					Small      []string `json:"small"`
					Unlabelled []string `json:"unlabelled"`
				}
				tab.Eval(shareProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if len(m.Unlabelled) > 0 {
					t.Errorf("inputs without a label: %v", m.Unlabelled)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("backups-%s-%s-%d.png", state, theme, w)))
			})
		}
	}

	shoot("empty")
	for i := 3; i >= 1; i-- {
		if _, err := db.WriteCopy(context.Background(), a.st, folder, clock.Add(-time.Duration(i)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	shoot("copies")
	id, err := a.srv.backups.Start(context.Background(), "", "scheduled")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Writer().Exec(`UPDATE jobs SET state = 'failed', error_code = 'backup_failed', updated_at = ? WHERE id = ? AND kind = ?`,
		time.Now().UnixMilli(), id, backups.Kind); err != nil {
		t.Fatal(err)
	}
	shoot("failed")
	t.Logf("screenshots in %s", out)
}
