package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// ebookInboxSeed adds the unpaired ready files the Ebooks to pair rows act on: one with an edition
// (the pick page), and one pair file and one keep file for each of owner and admin, because a
// successful action takes the file out of the unpaired set.
func ebookInboxSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	a := e.a
	for i, name := range []string{"inbox", "ipair0", "ipair1", "inotsame0", "inotsame1", "ikeep0", "ikeep1", "idl"} {
		state := "ready"
		if name == "idl" {
			state = "failed" // "Could not be opened" with a download link
		}
		id := placeEbook(t, a, e.book, e.uid[sweepOwner], "sweep-"+name+".epub", state, "")
		sha := fmt.Sprintf("%064x", 7000+i)
		if _, err := a.st.Writer().ExecContext(context.Background(), `UPDATE ebook_files SET sha256 = ?, quick_hash = ?, book_id = NULL, paired_by = NULL WHERE id = ?`, sha, sha, id); err != nil {
			t.Fatal(err)
		}
		if name == "inbox" {
			if _, err := a.st.Writer().ExecContext(context.Background(), `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format,
title, authors_json, chapter_count, word_count, char_count, created_at) VALUES ('ed_inbox', ?, ?, ?, ?, 10, 'epub', 'Sweep Inbox Ebook', '[]', 3, 30, 300, 1)`,
				id, sha, ebook.Recipe, sha); err != nil {
				t.Fatal(err)
			}
		}
		if name == "idl" {
			if err := os.MkdirAll(filepath.Join(a.dir, "ebooks"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(a.dir, "ebooks", sha+".epub"), []byte("not really an epub"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		e.ebookPair[name] = id
	}
}

// ebookInboxSweepRows are the route matrix rows of /admin/ebooks: owner and admin manage libraries,
// a member and a guest get 404 on every one.
func ebookInboxSweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	see := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	list := sweepGet("GET /admin/ebooks", func(*sweepEnv, int) string { return "/admin/ebooks" }, staff)
	pick := sweepGet("GET /admin/ebooks/{fileID}", func(e *sweepEnv, _ int) string { return "/admin/ebooks/" + e.ebookPair["inbox"] }, staff)
	post := func(verb, key string, form func(*sweepEnv, int) url.Values) sweepRow {
		return sweepPost("POST /admin/ebooks/{fileID}/"+verb, func(e *sweepEnv, role int) string {
			return "/admin/ebooks/" + e.ebookPair[fmt.Sprintf("%s%d", key, min(role, 1))] + "/" + verb
		}, form, see, "/admin/ebooks")
	}
	pair := post("pair", "ipair", func(e *sweepEnv, _ int) url.Values { return url.Values{"book": {e.book.ID}} })
	notSame := post("not-same", "inotsame", func(e *sweepEnv, _ int) url.Values { return url.Values{"book": {e.book.ID}} })
	keep := post("keep", "ikeep", sweepFormOf(url.Values{}))
	file := sweepGet("GET /admin/ebooks/{fileID}/file", func(e *sweepEnv, _ int) string { return "/admin/ebooks/" + e.ebookPair["idl"] + "/file" }, staff)
	return []sweepRow{list, pick, pair, notSame, keep, file}
}
