//go:build dev

package web

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/upload"
)

// TestLibrariesPageVisual captures the Libraries page, organised by library (Folders, Watching, Online details,
// Adding books), with two libraries, lookups on and uploads set up, at 390, 768, 1024 and 1440 px in dark and
// light. It fails on horizontal overflow, a target under 44 px, an unlabelled input or a page without exactly
// one h1. Opt-in: TENDSHELF_TEST_BROWSER.
func TestLibrariesPageVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	app := newTestApp(t, compareMod(t, &compareNet{}), func(o *Options) {
		svc := upload.New(upload.Config{DataDir: o.DataDir, ChunkSize: 1024, MaxFile: 1 << 20, MaxBook: 4 << 20},
			o.Store, o.Library, media.Tools{}, o.Jobs, nil)
		if err := svc.Register(o.Jobs); err != nil {
			t.Fatal(err)
		}
		o.Uploads = svc
	})
	adminSeed(t, app)
	_, _ = librarytest.NewLibrary(t, app.lib, "Podcasts", "/srv/media/podcasts")
	libs, err := app.lib.Libraries(context.Background())
	if err != nil || len(libs) != 2 {
		t.Fatalf("libraries: %v %v", libs, err)
	}
	folders, err := app.lib.Folders(context.Background(), libs[0].ID)
	if err != nil || len(folders) == 0 {
		t.Fatalf("folders: %v %v", folders, err)
	}
	if err := app.lib.SetUploadFolder(context.Background(), libs[0].ID, folders[0].ID); err != nil {
		t.Fatal(err)
	}
	base := themedServer(t, app.signedIn(t))
	var shots []visualShot
	for _, theme := range []string{"dark", "light"} {
		for _, w := range []int{390, 768, 1024, 1440} {
			shots = append(shots, visualShot{theme, w})
		}
	}
	for _, sh := range shots {
		theme, w := sh.Theme, sh.Width
		t.Run(fmt.Sprintf("libraries-%s-%d", theme, w), func(t *testing.T) {
			tab := b.NewTab(w, 900, w == 390, false)
			tab.Navigate(base + "/admin/libraries?theme=" + theme)
			var m struct {
				Over       bool     `json:"over"`
				Small      []string `json:"small"`
				Unlabelled []string `json:"unlabelled"`
				H1         int      `json:"h1"`
			}
			tab.Eval(lookupsProbeJS, &m)
			if m.Over {
				t.Errorf("horizontal overflow")
			}
			if len(m.Small) > 0 {
				t.Errorf("targets under 44 px: %v", m.Small)
			}
			if len(m.Unlabelled) > 0 {
				t.Errorf("inputs without a label: %v", m.Unlabelled)
			}
			if m.H1 != 1 {
				t.Errorf("%d h1 elements", m.H1)
			}
			tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("libpage-%s-%d.png", theme, w)))
		})
	}
}
