package ebook

import (
	"context"
	"database/sql"
	"os"
	"strings"
)

// oldEdition is an earlier reading of a file, with its document when that can still be loaded.
type oldEdition struct {
	ID, DocSHA string
	Doc        *Doc
}

// CharPos counts the UTF-16 units before a in the whole document, and the total. ok is false when a
// does not name a block of d.
func (d *Doc) CharPos(a Anchor) (pos, total int, ok bool) {
	nch, np, _, valid := ParseSID(a.SID)
	for ci, c := range d.Chapters {
		for bi, b := range c.Blocks {
			n := u16Len(b.Text)
			if valid && ci+1 == nch && bi+1 == np {
				pos, ok = total+min(max(a.Off, 0), n), true
			}
			total += n
		}
	}
	return pos, total, ok
}

// olderEditions lists the editions of a file other than keep, newest first, loading their documents.
func (s *Service) olderEditions(ctx context.Context, fileID, keepSHA, keepRecipe string) ([]oldEdition, error) {
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT id, doc_sha256 FROM editions WHERE ebook_file_id = ?
AND NOT (source_sha256 = ? AND recipe = ?) ORDER BY created_at DESC, id DESC`, fileID, keepSHA, keepRecipe)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []oldEdition
	for rs.Next() {
		var o oldEdition
		if err := rs.Scan(&o.ID, &o.DocSHA); err != nil {
			return nil, err
		}
		if d, err := ReadDoc(s.textDir(), o.DocSHA); err == nil {
			o.Doc = d
		}
		out = append(out, o)
	}
	return out, rs.Err()
}

// rehome moves the marks of every older edition onto the new document. A mark that cannot be placed
// keeps its row with edition_id NULL: it stays in the marks list with its quote and has no jump (a
// reading position falls back to its percent). Runs inside the ingest transaction.
func (s *Service) rehome(ctx context.Context, tx *sql.Tx, olds []oldEdition, nd *Doc, newID string, now int64) error {
	for _, o := range olds {
		if err := rehomeHighlights(ctx, tx, o, nd, newID, now); err != nil {
			return err
		}
		if err := rehomeBookmarks(ctx, tx, o, nd, newID, now); err != nil {
			return err
		}
		if err := rehomePositions(ctx, tx, o, nd, newID, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM editions WHERE id = ?`, o.ID); err != nil {
			return err
		}
	}
	return nil
}

func rehomeHighlights(ctx context.Context, tx *sql.Tx, o oldEdition, nd *Doc, newID string, now int64) error {
	type row struct {
		id, ssid, esid, quote, pre, suf string
		soff, eoff                      int
	}
	rs, err := tx.QueryContext(ctx, `SELECT id, start_sid, start_off, end_sid, end_off, quote, quote_prefix, quote_suffix
FROM highlights WHERE edition_id = ?`, o.ID)
	if err != nil {
		return err
	}
	var rows []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.id, &r.ssid, &r.soff, &r.esid, &r.eoff, &r.quote, &r.pre, &r.suf); err != nil {
			rs.Close()
			return err
		}
		rows = append(rows, r)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return err
	}
	for _, r := range rows {
		lines := strings.Split(r.quote, "\n")
		first, last := lines[0], lines[len(lines)-1]
		start, ok := Reanchor(nd, first, r.pre, "", Anchor{r.ssid, r.soff})
		var end Anchor
		if ok && len(lines) == 1 {
			end = Anchor{Off: start.Off + u16Len(first)}
			end.SID = sentenceAt(nd, start.SID, end.Off-1)
			ok = end.SID != ""
		} else if ok {
			var e Anchor
			if e, ok = Reanchor(nd, last, "", r.suf, Anchor{r.esid, r.eoff - u16Len(last)}); ok {
				end = Anchor{SID: sentenceAt(nd, e.SID, e.Off+u16Len(last)-1), Off: e.Off + u16Len(last)}
				ok = end.SID != "" && CompareSID(start.SID, end.SID) <= 0
			}
		}
		if !ok {
			if _, err := tx.ExecContext(ctx, `UPDATE highlights SET edition_id = NULL, updated_at = ? WHERE id = ?`, now, r.id); err != nil {
				return err
			}
			continue
		}
		ch, _, _, _ := ParseSID(start.SID)
		if _, err := tx.ExecContext(ctx, `UPDATE highlights SET edition_id = ?, chapter_idx = ?, start_sid = ?, start_off = ?,
end_sid = ?, end_off = ?, updated_at = ? WHERE id = ?`, newID, ch, start.SID, start.Off, end.SID, end.Off, now, r.id); err != nil {
			return err
		}
	}
	return nil
}

// sentenceAt names the sentence holding off in the block that sid belongs to ("" when the block is
// gone). The offset is clamped into the block.
func sentenceAt(d *Doc, sid string, off int) string {
	nch, np, _, ok := ParseSID(sid)
	if !ok || nch > len(d.Chapters) || np > len(d.Chapters[nch-1].Blocks) {
		return ""
	}
	return anchorIn(d.Chapters[nch-1].Blocks[np-1], max(off, 0)).SID
}

func rehomeBookmarks(ctx context.Context, tx *sql.Tx, o oldEdition, nd *Doc, newID string, now int64) error {
	rs, err := tx.QueryContext(ctx, `SELECT id, sid FROM bookmarks WHERE edition_id = ? AND sid IS NOT NULL`, o.ID)
	if err != nil {
		return err
	}
	type row struct{ id, sid string }
	var rows []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.id, &r.sid); err != nil {
			rs.Close()
			return err
		}
		rows = append(rows, r)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return err
	}
	for _, r := range rows {
		// A bookmark stores only a sentence id, so the sentence's own text is the quote.
		quote, off := sentenceText(o.Doc, r.sid)
		a, ok := Reanchor(nd, quote, "", "", Anchor{r.sid, off})
		if !ok {
			if _, err := tx.ExecContext(ctx, `UPDATE bookmarks SET edition_id = NULL, updated_at = ? WHERE id = ?`, now, r.id); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE bookmarks SET edition_id = ?, sid = ?, updated_at = ? WHERE id = ?`, newID, a.SID, now, r.id); err != nil {
			return err
		}
	}
	return nil
}

// sentenceText returns the text of the sentence sid names in d and its UTF-16 start in the block.
func sentenceText(d *Doc, sid string) (string, int) {
	if d == nil {
		return "", 0
	}
	nch, np, k, ok := ParseSID(sid)
	if !ok || nch > len(d.Chapters) || np > len(d.Chapters[nch-1].Blocks) {
		return "", 0
	}
	b := d.Chapters[nch-1].Blocks[np-1]
	if k >= len(b.Sentences) {
		return "", 0
	}
	return unitsSlice(b.Text, b.Sentences[k][0], b.Sentences[k][1]), b.Sentences[k][0]
}

func rehomePositions(ctx context.Context, tx *sql.Tx, o oldEdition, nd *Doc, newID string, now int64) error {
	rs, err := tx.QueryContext(ctx, `SELECT user_id, book_id, sid, off, quote FROM reading_positions WHERE edition_id = ?`, o.ID)
	if err != nil {
		return err
	}
	type row struct {
		user, book, sid, quote string
		off                    int
	}
	var rows []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.user, &r.book, &r.sid, &r.off, &r.quote); err != nil {
			rs.Close()
			return err
		}
		rows = append(rows, r)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return err
	}
	for _, r := range rows {
		q, _, _ := strings.Cut(r.quote, "\n")
		a, ok := Reanchor(nd, q, "", "", Anchor{r.sid, r.off})
		pos, total, ok2 := nd.CharPos(a)
		if !ok || !ok2 {
			if _, err := tx.ExecContext(ctx, `UPDATE reading_positions SET edition_id = NULL, server_updated_at = ? WHERE user_id = ? AND book_id = ?`,
				now, r.user, r.book); err != nil {
				return err
			}
			continue
		}
		ch, _, _, _ := ParseSID(a.SID)
		bp := 0
		if total > 0 {
			bp = min(10000, pos*10000/total)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE reading_positions SET edition_id = ?, chapter_idx = ?, sid = ?, off = ?, char_pos = ?,
percent_bp = ?, server_updated_at = ? WHERE user_id = ? AND book_id = ?`, newID, ch, a.SID, a.Off, pos, bp, now, r.user, r.book); err != nil {
			return err
		}
	}
	return nil
}

// dropDocs deletes each older edition's document that no edition shares any more. Called after the
// ingest transaction committed.
func (s *Service) dropDocs(ctx context.Context, olds []oldEdition) {
	for _, o := range olds {
		var n int
		if err := s.db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM editions WHERE doc_sha256 = ?`, o.DocSHA).Scan(&n); err == nil && n == 0 {
			_ = os.Remove(DocPath(s.textDir(), o.DocSHA))
		}
	}
}

// unitsSlice returns the part of s between UTF-16 offsets a and b.
func unitsSlice(s string, a, b int) string {
	var sb strings.Builder
	u := 0
	for _, r := range s {
		n := 1
		if r >= 0x10000 {
			n = 2
		}
		if u >= a && u+n <= b {
			sb.WriteRune(r)
		}
		u += n
	}
	return sb.String()
}
