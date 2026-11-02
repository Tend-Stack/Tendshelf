package web

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
)

// linkSweep is what the share-link rows act on (P3-5a): a book of its own, so the leak scan of the
// sweep book's words stays meaningful, and the tokens of an open link, a revoked one and one with a
// password, plus the id of a link the Stop row ends.
type linkSweep struct {
	open, revoked, locked string
	stopID                string
	audio, otherAudio     string
}

// linkSeed makes the link book and its links, as the owner. The sweep's own book is the
// "another book" a link must not reach (e.audio).
func linkSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	ctx := context.Background()
	// A library of its own: a grant on the sweep library must not show this book to anyone.
	_, folder := librarytest.NewLibrary(t, e.a.lib, "Link sweep library", adminAudioDir(t, true))
	b := librarytest.AddBook(t, e.a.lib, folder, librarytest.BookSpec{Title: "Linkpublic Tale", DurationMs: 3000, AddedAt: 6, Files: 1})
	files, err := e.a.lib.AudioFiles(ctx, b.ID)
	if err != nil || len(files) != 1 {
		t.Fatalf("link book files = %d, %v", len(files), err)
	}
	f := files[0]
	if err := wav.Write(f.Path, 3000, 330); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.st.Writer().ExecContext(ctx, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=3000, offset_ms=0, size=?, mtime_ns=? WHERE id=?`,
		fi.Size(), fi.ModTime().UnixNano(), f.ID); err != nil {
		t.Fatal(err)
	}
	peaksWrite(t, e.a, f)
	sha := fmt.Sprintf("%064x", 0xfeed02)
	cover := library.Cover{BookID: b.ID, Source: library.CoverFolder, Mime: "image/png", Width: 8, Height: 8, SHA256: sha, Path: sweepWriteCover(t, e.a.dir, sha), Tint: "#1f4a52"}
	if err := e.a.lib.Update(ctx, func(tx *library.Tx) error {
		if err := tx.PutCover(&cover); err != nil {
			return err
		}
		return tx.SetBookCover(b.ID, cover.ID)
	}); err != nil {
		t.Fatal(err)
	}
	owner, err := e.a.srv.users.ByID(ctx, e.uid[sweepOwner])
	if err != nil {
		t.Fatal(err)
	}
	hash, err := e.a.srv.users.HashPassword(ctx, "sweep link password")
	if err != nil {
		t.Fatal(err)
	}
	create := func(pw string) (share.Link, string) {
		var l share.Link
		var tok string
		err := e.a.st.WriteTx(ctx, func(tx *sql.Tx) error {
			var err error
			l, tok, err = e.a.srv.links.Create(ctx, tx, owner, share.LinkSpec{BookID: b.ID, Days: 7}, pw)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return l, tok
	}
	e.link.audio, e.link.otherAudio = f.ID, e.audio[0]
	_, e.link.open = create("")
	rl, tok := create("")
	e.link.revoked = tok
	if _, err := e.a.st.Writer().ExecContext(ctx, `UPDATE share_links SET revoked_at = created_at WHERE id = ?`, rl.ID); err != nil {
		t.Fatal(err)
	}
	_, e.link.locked = create(hash)
	l, _ := create("")
	e.link.stopID = l.ID
}

// linksSweepRows are the route matrix rows of the share-link routes. The /s/ routes are public:
// every role and the visitor get the same answer, because the session is never read. The two
// management routes are for people who manage users.
func linksSweepRows() []sweepRow {
	at := func(tok func(*linkSweep) string, suffix func(*linkSweep) string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return "/s/" + tok(&e.link) + suffix(&e.link) }
	}
	none := func(*linkSweep) string { return "" }
	open := func(l *linkSweep) string { return l.open }
	revoked := func(l *linkSweep) string { return l.revoked }
	locked := func(l *linkSweep) string { return l.locked }
	pub := func(r sweepRow, label string, code int) sweepRow {
		r.Label, r.Want, r.Anon = label, sweepSame(code), code
		return r
	}
	get := func(pattern string, p func(*sweepEnv, int) string, label string, code int) sweepRow {
		return pub(sweepGet(pattern, p, sweepSame(code)), label, code)
	}
	post := func(p func(*sweepEnv, int) string, label string, code int, form url.Values) sweepRow {
		return pub(sweepPost("POST /s/{token}", p, sweepFormOf(form), sweepSame(code), ""), label, code)
	}
	stream := func(tok func(*linkSweep) string, file func(*linkSweep) string) func(*sweepEnv, int) string {
		return at(tok, func(l *linkSweep) string { return "/stream/" + file(l) })
	}
	peaks := func(tok func(*linkSweep) string, file func(*linkSweep) string) func(*sweepEnv, int) string {
		return at(tok, func(l *linkSweep) string { return "/peaks/" + file(l) })
	}
	own := func(l *linkSweep) string { return l.audio }
	foreign := func(l *linkSweep) string { return l.otherAudio }

	openPost := post(at(open, none), "open link", http.StatusSeeOther, url.Values{})
	openPost.LocOf = func(e *sweepEnv) string { return "/s/" + e.link.open }
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	stopRow := sweepPost("POST /share/links/{id}/stop", func(e *sweepEnv, _ int) string { return "/share/links/" + e.link.stopID + "/stop" },
		nil, sweepStaff(http.StatusSeeOther, http.StatusNotFound), "/share")
	return []sweepRow{
		get("GET /s/{token}", at(open, none), "open link", http.StatusOK),
		get("GET /s/{token}", at(revoked, none), "revoked link", http.StatusNotFound),
		get("GET /s/{token}", at(locked, none), "locked link", http.StatusOK),
		get("GET /s/{token}/stream/{audioFileID}", stream(open, own), "the link's file", http.StatusOK),
		get("GET /s/{token}/stream/{audioFileID}", stream(open, foreign), "another book's file", http.StatusNotFound),
		get("GET /s/{token}/stream/{audioFileID}", stream(revoked, own), "revoked link", http.StatusNotFound),
		get("GET /s/{token}/stream/{audioFileID}", stream(locked, own), "locked link", http.StatusNotFound),
		get("GET /s/{token}/peaks/{audioFileID}", peaks(open, own), "the link's file", http.StatusOK),
		get("GET /s/{token}/peaks/{audioFileID}", peaks(open, foreign), "another book's file", http.StatusNotFound),
		get("GET /s/{token}/peaks/{audioFileID}", peaks(locked, own), "locked link", http.StatusNotFound),
		get("GET /s/{token}/cover", at(open, func(*linkSweep) string { return "/cover?w=64" }), "open link", http.StatusOK),
		get("GET /s/{token}/cover", at(locked, func(*linkSweep) string { return "/cover?w=64" }), "locked link", http.StatusNotFound),
		openPost,
		post(at(revoked, none), "revoked link", http.StatusNotFound, url.Values{"password": {"x"}}),
		post(at(locked, none), "wrong password", http.StatusUnprocessableEntity, url.Values{"password": {"not the password"}}),
		sweepPost("POST /books/{id}/share/link", sweepBookPath("/share/link"), sweepFormOf(url.Values{"link_days": {"7"}, "link_label": {"Sweep"}}), staff, ""),
		stopRow,
	}
}
