package online

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestChapterLookupClient(t *testing.T) {
	ctx := context.Background()
	fixture := sample(t, "audnexus_chapters.json")

	t.Run("a store that does not sell it, then a hit", func(t *testing.T) {
		e := newEnv(t)
		e.net.routes = nil
		// The French store is asked first for a French book; the default store has it.
		e.net.on("https://api.audnex.us/books/B0TESTBOOK/chapters?region=fr", reply{status: 404, body: []byte(`{"code":"REGION_UNAVAILABLE"}`)})
		e.net.on("https://api.audnex.us/books/B0TESTBOOK/chapters", reply{body: fixture})
		l, err := e.c.Chapters(ctx, "B0TESTBOOK", "french")
		if err != nil || len(l.Chapters) != 3 || l.Chapters[1] != (ChapterItem{StartMs: 62000, LengthMs: 900000, Title: "Chapter 1"}) {
			t.Fatalf("list: %v %+v", err, l)
		}
		if !l.Accurate || l.RuntimeMs != 1802000 || l.IntroMs != 2000 || l.OutroMs != 5000 || l.Sent != 2 {
			t.Errorf("fields: %+v", l)
		}
		if len(e.net.urls) != 2 || !strings.HasSuffix(e.net.urls[0], "chapters?region=fr") {
			t.Errorf("urls: %v", e.net.urls)
		}
	})

	t.Run("every store says no", func(t *testing.T) {
		e := newEnv(t)
		e.net.routes = nil
		_, err := e.c.Chapters(ctx, "B0TESTBOOK", "")
		var oe *Error
		if !errors.As(err, &oe) || oe.Code != "no_chapters" || oe.Text() != "Audnexus has no chapters for this book." {
			t.Fatalf("err: %v", err)
		}
		if len(e.net.urls) != 3 {
			t.Errorf("at most three stores: %v", e.net.urls)
		}
	})

	t.Run("a server error stops at once", func(t *testing.T) {
		e := newEnv(t)
		e.net.routes = nil
		e.net.on("https://api.audnex.us/books/", reply{status: 500})
		_, err := e.c.Chapters(ctx, "B0TESTBOOK", "")
		var oe *Error
		if !errors.As(err, &oe) || oe.Code != "server" || len(e.net.urls) != 1 {
			t.Errorf("err %v, urls %v", err, e.net.urls)
		}
	})

	t.Run("the saved copy sends nothing", func(t *testing.T) {
		e := newEnv(t)
		e.net.routes = nil
		e.net.on("https://api.audnex.us/books/B0TESTBOOK/chapters", reply{body: fixture})
		if _, err := e.c.Chapters(ctx, "B0TESTBOOK", ""); err != nil {
			t.Fatal(err)
		}
		e.net.reset()
		l, err := e.c.Chapters(ctx, "B0TESTBOOK", "")
		if err != nil || len(e.net.urls) != 0 || l.Sent != 0 || len(l.Chapters) != 3 {
			t.Errorf("cached: %v %v sent %d", err, e.net.urls, l.Sent)
		}
	})
}
