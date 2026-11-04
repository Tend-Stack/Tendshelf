package upload

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// FileIn is one file of a manifest: its path below the picked folder (slash form), its size and
// the modified time the browser reports.
type FileIn struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModifiedMs int64  `json:"modified_ms"`
	// SHA256 is the optional hex SHA-256 of the whole file; when present placement verifies it.
	SHA256 string `json:"sha256,omitempty"`
}

// Manifest describes one book before any byte is sent.
type Manifest struct {
	Label string   `json:"label"`
	Files []FileIn `json:"files"`
}

const (
	maxPathBytes    = 1024
	maxPathSegments = 8
	maxLabelBytes   = 255
)

// placedExt lists the non-audio extensions that go into the book's folder: pictures and sidecars.
var sha256Re = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

var placedExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	".opf": true, ".nfo": true, ".txt": true, ".json": true, ".cue": true,
}

// hiddenPath reports whether any segment is a dot name or one of the scanner's junk names. ".."
// does not count: it is a bad path, not a hidden one.
func hiddenPath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			continue
		}
		if scan.SkipName(seg) {
			return true
		}
	}
	return false
}

// kept reports whether a manifest entry is placed: audio, a picture or a sidecar, not hidden.
func kept(p string) bool {
	if hiddenPath(p) {
		return false
	}
	switch media.Classify(p) {
	case media.KindAudio, media.KindProtected:
		return true
	}
	return placedExt[strings.ToLower(filepath.Ext(p))]
}

// checkPath validates one manifest path. The message names the rule in plain words.
func checkPath(p string) string {
	switch {
	case !utf8.ValidString(p):
		return "That file name is not valid text."
	case strings.ContainsRune(p, 0):
		return "That file name has a character Tendshelf cannot use."
	case strings.Contains(p, `\`):
		return "That file name has a backslash. Choose the files again."
	case len(p) == 0 || len(p) > maxPathBytes:
		return "That file name is too long."
	}
	segs := strings.Split(p, "/")
	if len(segs) > maxPathSegments {
		return "That file is in too many nested folders."
	}
	for _, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			return "That file name is not allowed."
		}
		for _, r := range seg {
			if unicode.IsControl(r) {
				return "That file name has a character Tendshelf cannot use."
			}
		}
	}
	return ""
}

func cleanLabel(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	for len(out) > maxLabelBytes {
		out = out[:maxLabelBytes]
		for !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	return out
}

// Create checks a manifest and opens an upload for it. The order of the checks is part of the
// contract: the first failing check is the one reported.
func (s *Service) Create(ctx context.Context, userID, libraryID string, m Manifest) (View, error) {
	lib, err := s.repo.Library(ctx, libraryID)
	if errors.Is(err, library.ErrNotFound) {
		return View{}, errNotFound
	} else if err != nil {
		return View{}, err
	}
	if lib.UploadFolderID == "" {
		return View{}, fail(409, "uploads_off", "This library does not take uploads.")
	}
	folder, err := s.repo.Folder(ctx, lib.UploadFolderID)
	if err != nil {
		return View{}, fail(409, "uploads_off", "This library does not take uploads.")
	}

	if len(m.Files) == 0 {
		return View{}, fail(422, "no_audio", "There is no audio file in it.")
	}
	if len(m.Files) > s.cfg.MaxFiles {
		return View{}, fail(413, "too_large", "That is too many files for one book.")
	}
	// An upload of one .zip file is one book: the archive is unpacked on the server at placement.
	zipOnly := len(m.Files) == 1 && isZip(m.Files[0].Path) && !hiddenPath(m.Files[0].Path)
	var total int64
	for _, f := range m.Files {
		if !kept(f.Path) && !zipOnly {
			continue
		}
		if f.Size <= 0 {
			return View{}, &Error{Status: 422, Code: "empty_file", Message: "That file is empty.", Field: f.Path}
		}
		if f.Size > s.cfg.MaxFile {
			return View{}, &Error{Status: 413, Code: "too_large", Message: "That file is too large.", Field: f.Path}
		}
		total += f.Size
		if total > s.cfg.MaxBook {
			return View{}, fail(413, "too_large", "That is more than one book can hold.")
		}
	}

	seen := map[string]bool{}
	for _, f := range m.Files {
		if f.SHA256 != "" && !sha256Re.MatchString(f.SHA256) {
			return View{}, &Error{Status: 422, Code: "bad_checksum", Message: "That file's checksum is not valid.", Field: f.Path}
		}
		if msg := checkPath(f.Path); msg != "" {
			return View{}, &Error{Status: 422, Code: "bad_path", Message: msg, Field: f.Path}
		}
		low := strings.ToLower(f.Path)
		if seen[low] {
			return View{}, &Error{Status: 422, Code: "bad_path", Message: "Two files have the same name.", Field: f.Path}
		}
		seen[low] = true
	}
	var files []FileIn
	skipped, audio := 0, 0
	for _, f := range m.Files {
		if media.Classify(f.Path) == media.KindProtected && !hiddenPath(f.Path) {
			return View{}, &Error{Status: 422, Code: "copy_protected", Field: f.Path,
				Message: "This file is copy-protected, so Tendshelf will not open it."}
		}
		if zipOnly {
			files = append(files, f)
			continue
		}
		if !kept(f.Path) {
			skipped++
			continue
		}
		if media.Classify(f.Path) == media.KindAudio {
			audio++
		}
		files = append(files, f)
	}
	if audio == 0 && !zipOnly {
		return View{}, fail(422, "no_audio", "There is no audio file in it.")
	}

	var receiving int
	var pending, mine int64
	if err := s.st.Reader().QueryRowContext(ctx, `SELECT
  COALESCE(SUM(created_by = ? AND state = 'receiving'), 0),
  COALESCE(SUM(CASE WHEN state = 'receiving' THEN bytes_total - bytes_received ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN created_by = ? THEN bytes_total ELSE 0 END), 0)
  FROM uploads WHERE state IN ('receiving','queued','placing','failed')`,
		userID, userID).Scan(&receiving, &pending, &mine); err != nil {
		return View{}, err
	}
	if mine+total > s.cfg.PerPerson {
		return View{}, fail(507, "quota", "You already have a lot waiting on the server. Finish or cancel an upload first.")
	}
	if receiving >= s.cfg.MaxReceiving {
		return View{}, fail(429, "too_many", "Finish or cancel one of your other uploads first.")
	}
	if err := os.MkdirAll(s.incomingDir(), 0o700); err != nil {
		return View{}, err
	}
	if free, err := s.free(s.incomingDir()); err != nil || int64(free) < pending+total+s.cfg.Margin {
		return View{}, fail(507, "no_space", "The server's data folder is full.")
	}
	need := total + total/20 + s.cfg.Margin
	if free, err := s.free(folder.Path); err != nil || int64(free) < need {
		return View{}, fail(507, "no_space", "The library folder is full.")
	}

	label := cleanLabel(m.Label)
	if label == "" {
		for _, f := range files {
			if media.Classify(f.Path) == media.KindAudio || zipOnly {
				label = cleanLabel(strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path)))
				break
			}
		}
	}
	now := s.nowMs()
	id := db.NewID("upl")
	rows := make([]fileRow, len(files))
	for i, f := range files {
		rows[i] = fileRow{ID: db.NewID("upf"), UploadID: id, Idx: i, RelPath: f.Path, Size: f.Size, ModifiedMs: f.ModifiedMs, SHA256: f.SHA256}
	}
	staging := s.stagingDir(id)
	if err := os.Mkdir(staging, 0o700); err != nil {
		return View{}, err
	}
	syncDir(s.incomingDir())
	err = s.write(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO uploads(id, library_id, folder_id, created_by, label, state, bytes_total,
  bytes_received, skipped, created_at, updated_at) VALUES (?,?,?,?,?,?,?,0,?,?,?)`,
			id, lib.ID, folder.ID, userID, label, StateReceiving, total, skipped, now, now); err != nil {
			return err
		}
		for _, r := range rows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO upload_files(id, upload_id, idx, rel_path, size, modified_ms, sha256)
  VALUES (?,?,?,?,?,?,?)`, r.ID, id, r.Idx, r.RelPath, r.Size, r.ModifiedMs, nullIfEmpty(strings.ToLower(r.SHA256))); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = os.Remove(staging)
		return View{}, err
	}
	u, err := s.loadUpload(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.view(u, rows), nil
}
