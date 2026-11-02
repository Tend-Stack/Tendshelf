//go:build dev

package web

import (
	"testing"
	"time"
)

// TestPlayerBarBrowserMetaLongAuthor: in the desktop bar the author gives way to the time left. With a very
// long author the author is cut with an ellipsis, and the time left stays whole inside the meta line (P3-11).
func TestPlayerBarBrowserMetaLongAuthor(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 1280, 800, true)
	time.Sleep(500 * time.Millisecond)
	var got struct {
		Author, Left                   string
		MetaR, LeftL, LeftR            float64
		AuthorScroll, AuthorClient     float64
		LeftScroll, LeftClient, MetaOK bool
	}
	// Set and measure in one script, so the next tick cannot redraw the author in between.
	tab.Eval(`(() => {
	  const author = document.querySelector("[data-ts-bar-author]");
	  const left = document.querySelector("[data-ts-bar-left]");
	  const meta = document.querySelector("[data-ts-bar-meta]");
	  author.textContent = "A very long author name that goes on and on and on and on and on and on and on and on · ";
	  const m = meta.getBoundingClientRect(), l = left.getBoundingClientRect();
	  return { Author: author.textContent, Left: left.textContent, MetaR: m.right, LeftL: l.left, LeftR: l.right,
	    AuthorScroll: author.scrollWidth, AuthorClient: author.clientWidth, MetaOK: getComputedStyle(meta).display === "flex" };
	})()`, &got)
	if !got.MetaOK {
		t.Fatal("the meta line is not a flex row")
	}
	if got.Left == "" {
		t.Fatal("the time left is empty")
	}
	if got.LeftR > got.MetaR+0.5 {
		t.Errorf("time left runs past the meta line: right %.1f, meta right %.1f", got.LeftR, got.MetaR)
	}
	if got.AuthorScroll <= got.AuthorClient {
		t.Errorf("the long author was not cut (scrollWidth %.0f, clientWidth %.0f)", got.AuthorScroll, got.AuthorClient)
	}
	var leftFits bool
	tab.Eval(`(() => { const l = document.querySelector("[data-ts-bar-left]"); return l.scrollWidth <= l.clientWidth + 1; })()`, &leftFits)
	if !leftFits {
		t.Error("the time left is cut")
	}
}
