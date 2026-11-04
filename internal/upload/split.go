package upload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// bookGroup is the part of an archive that makes one book when the archive holds several top-level
// folders: the folder's name, its extracted files and, when it holds copy-protected audio, the
// first such file (the book is then refused and none of its files is kept).
type bookGroup struct {
	top   string
	files []extracted
	prot  string
	size  int64
}

// expand unpacks the one staged archive of an upload inside its staging folder (plan U3).
//
// An archive with one top-level folder, or with only loose files, is one book: the files come back
// as synthetic rows named x0000.part and so on, rebuilt on every attempt. An archive with two or
// more top-level folders that hold audio is split (see split). All zip-wide checks (entry count, total
// size, ratio, names, encryption) have run before either path.
// The unpacked bytes are counted on top of the person's quota, before and while they are written.
func (s *Service) expand(ctx context.Context, up uploadRow, zf fileRow) ([]fileRow, string, error) {
	dir := s.stagingDir(up.ID)
	if dir == "" {
		return nil, "", errors.New("upload: bad staging folder")
	}
	name := func(i int) string { return filepath.Join(dir, fmt.Sprintf("x%04d.part", i)) }
	clear := func() {
		for i := 0; i < maxZipEntries; i++ {
			if os.Remove(name(i)) != nil {
				break
			}
		}
	}
	clear()
	var mine int64
	if err := s.st.Reader().QueryRowContext(ctx, `SELECT COALESCE(SUM(bytes_total), 0) FROM uploads
  WHERE created_by = ? AND state IN ('receiving','queued','placing','failed')`, up.CreatedBy).Scan(&mine); err != nil {
		return nil, "", err
	}
	budget := min(s.cfg.MaxBook, s.cfg.PerPerson-mine)
	if budget <= 0 {
		return nil, "", &jobs.Error{Code: "quota", Text: "You already have a lot waiting on the server. Finish or cancel an upload first.", Permanent: true}
	}
	free, err := s.free(dir)
	if err != nil {
		return nil, "", err
	}
	room := max(int64(free)-s.cfg.Margin, 0)
	got, left, protected, err := extractZip(ctx, s.partPath(up.ID, zf.ID), zipLimits{budget: budget, free: room}, name)
	if err != nil {
		clear()
		return nil, "", err
	}

	// Which top-level folders hold audio (or protected audio)?
	audioTop := map[string]bool{}
	top := func(rel string) (string, bool) {
		i := strings.IndexByte(rel, '/')
		if i < 0 {
			return "", false
		}
		return rel[:i], true
	}
	for _, g := range got {
		if t, ok := top(g.rel); ok && media.Classify(g.rel) == media.KindAudio {
			audioTop[t] = true
		}
	}
	for _, p := range protected {
		if t, ok := top(p); ok {
			audioTop[t] = true
		}
	}
	if len(audioTop) < 2 {
		if len(protected) > 0 {
			clear()
			return nil, protected[0], nil
		}
		return s.oneBook(ctx, up, got, left, clear)
	}
	return s.split(ctx, up, zf, got, left, protected, audioTop, top, name, clear)
}

// oneBook returns the extracted files of a one-book archive as the upload's files.
func (s *Service) oneBook(ctx context.Context, up uploadRow, got []extracted, left int, clear func()) ([]fileRow, string, error) {
	audio := 0
	rows := make([]fileRow, len(got))
	for i, g := range got {
		if media.Classify(g.rel) == media.KindAudio {
			audio++
		}
		rows[i] = fileRow{ID: fmt.Sprintf("x%04d", i), UploadID: up.ID, Idx: i, RelPath: g.rel, Size: g.size}
	}
	if audio == 0 {
		clear()
		return nil, "", zipFail("no_audio", "There is no audio file in that zip file.", nil)
	}
	_ = s.write(ctx, nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE uploads SET skipped = ? WHERE id = ?`, left, up.ID)
		return err
	})
	return rows, "", nil
}

// split turns an archive of several book folders into one upload for each folder. The upload the
// archive came as becomes the first book (it keeps its row, job and place in the page); every other
// folder gets a sibling upload of its own with its own staging folder, file rows and state, queued
// like any finished upload. A folder with copy-protected audio is refused alone. Loose files at the
// top and folders without audio are left out and counted on the first book.
//
// Order: files are moved into their final staging folders first, then one transaction rewrites the
// original and creates the siblings, then the archive is deleted and the sibling jobs are queued. A
// failure before the transaction leaves the archive in place, so the next attempt splits again from
// scratch. The archive's size left the person's quota with it: each book now counts its own bytes.
func (s *Service) split(ctx context.Context, up uploadRow, zf fileRow, got []extracted, left int, protected []string,
	audioTop map[string]bool, top func(string) (string, bool), name func(int) string, clear func()) (rows []fileRow, refused string, err error) {
	byTop := map[string]*bookGroup{}
	var tops []string
	for t := range audioTop {
		byTop[t] = &bookGroup{top: t}
		tops = append(tops, t)
	}
	sort.Strings(tops)
	for _, p := range protected {
		if t, ok := top(p); ok && byTop[t].prot == "" {
			byTop[t].prot = p
		}
	}
	for _, g := range got {
		if t, ok := top(g.rel); ok && byTop[t] != nil {
			byTop[t].files = append(byTop[t].files, g)
			byTop[t].size += g.size
			continue
		}
		_ = os.Remove(g.file)
		left++
	}

	type target struct {
		id    string
		g     *bookGroup
		files []fileRow
	}
	var targets []target
	var made []string // paths to undo on a failure
	undo := func() {
		for i := len(made) - 1; i >= 0; i-- { // files were made after their folder
			_ = os.Remove(made[i])
		}
		clear()
	}
	for i, t := range tops {
		g := byTop[t]
		id := up.ID
		if i > 0 {
			id = db.NewID("upl")
		}
		tg := target{id: id, g: g}
		if g.prot != "" {
			for _, f := range g.files {
				_ = os.Remove(f.file)
			}
			targets = append(targets, tg)
			continue
		}
		if i > 0 {
			if err := os.Mkdir(s.stagingDir(id), 0o700); err != nil {
				undo()
				return nil, "", err
			}
			made = append(made, s.stagingDir(id))
		}
		for j, f := range g.files {
			fid := db.NewID("upf")
			dst := s.partPath(id, fid)
			if err := os.Rename(f.file, dst); err != nil {
				undo()
				return nil, "", err
			}
			made = append(made, dst)
			tg.files = append(tg.files, fileRow{ID: fid, UploadID: id, Idx: j, RelPath: f.rel, Size: f.size, BytesReceived: f.size})
		}
		targets = append(targets, tg)
	}
	now := s.nowMs()
	err = s.write(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM upload_files WHERE upload_id = ?`, up.ID); err != nil {
			return err
		}
		for i, tg := range targets {
			label := cleanLabel(tg.g.top)
			skipped := 0
			if i == 0 {
				skipped = left
				if _, err := tx.ExecContext(ctx, `UPDATE uploads SET label = ?, bytes_total = ?, bytes_received = ?, skipped = ?, updated_at = ? WHERE id = ?`,
					label, tg.g.size, tg.g.size, skipped, now, up.ID); err != nil {
					return err
				}
			} else {
				state, refusedFile, code := StateQueued, any(nil), any(nil)
				if tg.g.prot != "" {
					state, refusedFile, code = StateRefused, tg.g.prot, media.CodeDRM
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO uploads(id, library_id, folder_id, created_by, label, state, bytes_total,
  bytes_received, refused_file, error_code, skipped, split_from, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,0,?,?,?)`,
					tg.id, up.LibraryID, up.FolderID, up.CreatedBy, label, state, tg.g.size, tg.g.size, refusedFile, code, up.ID, now, now); err != nil {
					return err
				}
				if tg.g.prot != "" {
					if err := s.audit(ctx, tx, up.CreatedBy, "upload.refuse", "upload:"+tg.id, map[string]string{"reason": media.CodeDRM, "file": tg.g.prot}); err != nil {
						return err
					}
				}
			}
			for _, f := range tg.files {
				if _, err := tx.ExecContext(ctx, `INSERT INTO upload_files(id, upload_id, idx, rel_path, size, modified_ms, bytes_received)
  VALUES (?,?,?,?,?,0,?)`, f.ID, f.UploadID, f.Idx, f.RelPath, f.Size, f.BytesReceived); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		undo()
		return nil, "", err
	}
	_ = os.Remove(s.partPath(up.ID, zf.ID))
	syncDir(s.incomingDir())

	for _, tg := range targets[1:] {
		if tg.g.prot != "" {
			continue
		}
		job, qerr := s.enq.Enqueue(ctx, KindPlace, placePayload{UploadID: tg.id}, jobs.EnqueueOptions{CreatedBy: up.CreatedBy})
		if qerr != nil {
			// The sibling stays queued; the sweep fails it after a day and Try again places it.
			s.log.Error("upload: cannot queue a book of a split archive", "upload", tg.id, "err", qerr)
			continue
		}
		_ = s.write(ctx, nil, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE uploads SET job_id = ? WHERE id = ?`, job, tg.id)
			return err
		})
	}
	if targets[0].g.prot != "" {
		return nil, targets[0].g.prot, nil
	}
	return targets[0].files, "", nil
}

// Siblings lists the uploads an archive was split into (not the archive's own upload), oldest first.
func (s *Service) Siblings(ctx context.Context, userID, id string) ([]View, error) {
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT `+uploadCols+` FROM uploads WHERE split_from = ? AND created_by = ? ORDER BY rowid`, id, userID)
	if err != nil {
		return nil, err
	}
	var ups []uploadRow
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		ups = append(ups, u)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]View, 0, len(ups))
	for _, u := range ups {
		files, err := s.loadFiles(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, s.view(u, files))
	}
	return out, nil
}
