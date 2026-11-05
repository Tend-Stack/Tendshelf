package upload

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// tempPrefix starts the name of the hidden folder a book is built in. The scanner skips dot names,
// so a half-built book is never imported.
const tempPrefix = ".tendshelf-upload-"

// PlaceResult is the stored result of an upload.place job.
type PlaceResult struct {
	State     string `json:"state"`
	BookPath  string `json:"book_path,omitempty"`
	ScanJobID string `json:"scan_job_id,omitempty"`
}

// errRetry means the layout changed under the build (a name appeared); the caller lays out again.
var errRetry = errors.New("upload: layout changed, trying another name")

func (s *Service) handlePlace(ctx context.Context, t *jobs.Task) (any, error) {
	var p placePayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || !idRe.MatchString(p.UploadID) {
		return nil, &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
	}
	return s.place(ctx, t.Fence, t.Progress, p.UploadID)
}

// jobFail is a failed placement: the upload is marked failed with code, and the job fails with the
// plain-words text. permanent stops the runner retrying.
func (s *Service) jobFail(ctx context.Context, g guard, uploadID, code, text string, permanent bool, cause error) error {
	if ctx.Err() == nil {
		if err := s.setState(ctx, g, uploadID, StateFailed, code); err != nil {
			s.log.Warn("upload: cannot mark upload failed", "upload", uploadID, "err", err)
		}
	}
	return &jobs.Error{Code: code, Text: text, Permanent: permanent, Err: cause}
}

// plainFail ends a placement that hit an error. A *jobs.Error keeps its code, words and
// permanence; anything else (a permission error, a read-only file system, an unplugged disk) is
// transient: the upload becomes failed with a plain message, the runner may retry, and Try again
// works. Nothing is ever left in placing. A shutdown (the context ending) is not a failure.
func (s *Service) plainFail(ctx context.Context, g guard, uploadID string, err error) error {
	var je *jobs.Error
	if errors.As(err, &je) {
		return s.jobFail(ctx, g, uploadID, je.Code, je.Text, je.Permanent, err)
	}
	if ctx.Err() != nil {
		return err
	}
	s.log.Error("upload: placement failed", "upload", uploadID, "err", err)
	return s.jobFail(ctx, g, uploadID, "place_failed",
		"Tendshelf could not finish adding this book. Check that the library folder is available, then press Try again.", false, err)
}

// place is the body of the upload.place job (the steps are numbered in the plan). g is the job's
// fence; a test passes nil.
func (s *Service) place(ctx context.Context, g guard, progress func(any), uploadID string) (any, error) {
	step := func(name string) { progress(map[string]string{"step": name}) }
	up, err := s.loadUpload(ctx, uploadID)
	if err != nil {
		return nil, &jobs.Error{Code: "not_found", Text: "That upload no longer exists.", Permanent: true, Err: err}
	}
	switch up.State {
	case StatePlaced:
		return s.afterPublish(ctx, g, up)
	case StateRefused, StateCancelled, StateExpired:
		return PlaceResult{State: up.State}, nil
	case StateQueued, StatePlacing, StateFailed:
	default:
		return nil, &jobs.Error{Code: "wrong_state", Text: "That upload is not ready to be placed.", Permanent: true}
	}
	folder, err := s.repo.Folder(ctx, up.FolderID)
	if err != nil {
		return nil, s.jobFail(ctx, g, uploadID, "folder_gone", "The library folder for uploads is no longer set.", true, err)
	}
	// Compare-and-set: placement starts only from a state it was queued into. An upload cancelled
	// (or expired) in the meantime is left as it is.
	if err := s.setState(ctx, g, uploadID, StatePlacing, "", StateQueued, StatePlacing, StateFailed); err != nil {
		var ue *Error
		if errors.As(err, &ue) && ue.Code == "wrong_state" {
			cur, lerr := s.loadUpload(ctx, uploadID)
			if lerr != nil {
				return nil, lerr
			}
			return PlaceResult{State: cur.State}, nil
		}
		return nil, err
	}
	lr, err := openLroot(folder.Path)
	if err != nil {
		return nil, s.plainFail(ctx, g, uploadID, err)
	}
	defer lr.Close()

	step("recover")
	published, err := s.recoverPlacement(ctx, g, uploadID, lr)
	if err != nil {
		return nil, s.plainFail(ctx, g, uploadID, err)
	}
	if up, err = s.loadUpload(ctx, uploadID); err != nil {
		return nil, err
	}
	if !published {
		step("checking")
		files, err := s.loadFiles(ctx, uploadID)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			part := s.partPath(uploadID, f.ID)
			fi, err := os.Stat(part)
			if err != nil || fi.Size() != f.Size {
				return nil, s.jobFail(ctx, g, uploadID, "incomplete", "The uploaded files are not all on the server. Choose them again.", true, err)
			}
			if f.SHA256 != "" {
				sum, err := media.FileSHA256(part)
				if err != nil {
					return nil, s.plainFail(ctx, g, uploadID, err)
				}
				if !strings.EqualFold(sum, f.SHA256) {
					return nil, s.jobFail(ctx, g, uploadID, "damaged", "A file arrived damaged. Choose the files again.", true, nil)
				}
			}
		}
		if len(files) == 1 && isZip(files[0].RelPath) {
			var refusedZip string
			if files, refusedZip, err = s.expand(ctx, up, files[0]); err != nil {
				return nil, s.plainFail(ctx, g, uploadID, err)
			}
			if refusedZip != "" {
				return s.refuse(ctx, g, up, refusal{refusedZip, media.CodeDRM})
			}
			// A split changes the upload's label (the first folder's name).
			if up, err = s.loadUpload(ctx, uploadID); err != nil {
				return nil, err
			}
		}
		names, refused, err := s.inspect(ctx, up, files)
		if err != nil {
			return nil, s.plainFail(ctx, g, uploadID, err)
		}
		if refused.file != "" {
			return s.refuse(ctx, g, up, refused)
		}
		step("placing")
		if err := s.build(ctx, g, up, lr, files, names); err != nil {
			return nil, s.plainFail(ctx, g, uploadID, err)
		}
		if up, err = s.loadUpload(ctx, uploadID); err != nil {
			return nil, err
		}
	}
	step("importing")
	return s.afterPublish(ctx, g, up)
}

// bookNames are the names the layout is built from.
type bookNames struct{ author, title, series, seq string }

// refusal names the staged file that stops a whole upload and why: media.CodeDRM for copy-protected
// audio or ebooks, CodeUnreadable for an ebook Tendshelf cannot open.
type refusal struct{ file, code string }

// CodeUnreadable is the upload error code of an ebook that is damaged, empty of text, too large or of a
// kind Tendshelf cannot open.
const CodeUnreadable = "unreadable"

// inspect probes every staged audio file and opens every staged ebook. It returns the file that is
// copy-protected or unreadable (the whole book is then refused), or the names for the layout (step e).
func (s *Service) inspect(ctx context.Context, up uploadRow, files []fileRow) (bookNames, refusal, error) {
	var audio []fileRow
	for _, f := range files {
		if media.Classify(f.RelPath) == media.KindAudio {
			audio = append(audio, f)
		}
	}
	sort.Slice(audio, func(i, j int) bool { return audio[i].RelPath < audio[j].RelPath })
	var first *media.Probe
	for i, f := range audio {
		pr, err := s.tools.Probe(ctx, s.partPath(f.UploadID, f.ID))
		var me *media.Error
		switch {
		case pr.Encrypted || (errors.As(err, &me) && me.Code == media.CodeDRM):
			return bookNames{}, refusal{f.RelPath, media.CodeDRM}, nil
		case ctx.Err() != nil:
			return bookNames{}, refusal{}, context.Cause(ctx)
		case errors.Is(err, media.ErrNotFound) || (errors.As(err, &me) && me.Code == media.CodeFFprobeMissing):
			return bookNames{}, refusal{}, &jobs.Error{Code: media.CodeFFprobeMissing, Text: media.ErrorText(media.CodeFFprobeMissing), Permanent: true, Err: err}
		case err != nil:
			// A file that cannot be read must not be placed unchecked: it might be protected.
			code := media.CodeProbeFailed
			if me != nil && me.Code != "" {
				code = me.Code
			}
			text := media.ErrorText(code)
			if text == "" {
				text = "Tendshelf could not read one of the files."
			}
			return bookNames{}, refusal{}, &jobs.Error{Code: code, Text: text, Permanent: code == media.CodeNotAudio, Err: err}
		}
		if i == 0 || first == nil {
			p := pr
			first = &p
		}
	}
	// Ebooks are opened the way the library will open them: a copy-protected or unreadable one refuses
	// the whole upload before anything reaches the library folder. A PDF beside audio is a booklet the
	// scanner ignores, so it is placed without being opened.
	var firstDoc *ebook.Doc
	var ebooks []fileRow
	for _, f := range files {
		if !isEbook(f.RelPath) || (len(audio) > 0 && ebook.ExtOf(f.RelPath) == "pdf") {
			continue
		}
		ebooks = append(ebooks, f)
	}
	sort.Slice(ebooks, func(i, j int) bool { return ebooks[i].RelPath < ebooks[j].RelPath })
	for _, f := range ebooks {
		doc, err := s.readEbook(ctx, s.partPath(f.UploadID, f.ID), ebook.ExtOf(f.RelPath))
		var rf *ebook.Refusal
		switch {
		case ctx.Err() != nil:
			return bookNames{}, refusal{}, context.Cause(ctx)
		case errors.As(err, &rf) && rf.Reason == ebook.ReasonDRM:
			return bookNames{}, refusal{f.RelPath, media.CodeDRM}, nil
		case err != nil:
			s.log.Info("upload: an ebook could not be opened", "upload", up.ID, "err", err)
			return bookNames{}, refusal{f.RelPath, CodeUnreadable}, nil
		}
		if firstDoc == nil {
			firstDoc = doc
		}
	}
	var d metadata.Details
	switch {
	case first != nil:
		d = metadata.FromTags(first.Tags, len(audio) == 1)
	case firstDoc != nil:
		d.Title, d.Authors = firstDoc.Meta.Title, firstDoc.Meta.Authors
		if firstDoc.Meta.Series != "" {
			d.Series = []metadata.SeriesRef{{Name: firstDoc.Meta.Series, Sequence: firstDoc.Meta.SeriesIndex}}
		}
	}
	bn := metadata.ParseBookName(up.Label)
	var n bookNames
	if len(d.Authors) > 0 {
		n.author = d.Authors[0]
	} else {
		n.author = bn.Author
	}
	n.title = d.Title
	if n.title == "" {
		n.title = bn.Title
	}
	if len(d.Series) > 0 {
		n.series, n.seq = d.Series[0].Name, d.Series[0].Sequence
	} else {
		n.series, n.seq = bn.Series, bn.Sequence
	}
	return n, refusal{}, nil
}

// refuse ends an upload whose staged audio is encrypted: nothing was written to the library
// folder, and the staged bytes are deleted.
func (s *Service) refuse(ctx context.Context, g guard, up uploadRow, r refusal) (any, error) {
	err := s.write(ctx, g, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE uploads SET state = ?, refused_file = ?, error_code = ?, updated_at = ? WHERE id = ?`,
			StateRefused, r.file, r.code, s.nowMs(), up.ID); err != nil {
			return err
		}
		return s.audit(ctx, tx, up.CreatedBy, "upload.refuse", "upload:"+up.ID, map[string]string{"reason": r.code, "file": r.file})
	})
	if err != nil {
		return nil, err
	}
	s.removeStaging(up.ID)
	return PlaceResult{State: StateRefused}, nil
}

// item is one file as it will be placed: its row and its path below the book's folder.
type item struct {
	f   fileRow
	rel string // slash form, safe names
}

// bookItems names every file inside the book. When the files came from a picked folder (every
// path has a common first segment) that segment is the folder's own name and is dropped. Names go
// through safeInner; two files that end up with the same name, whatever the case, get " (2)".
func bookItems(files []fileRow) []item {
	sorted := append([]fileRow(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].RelPath < sorted[j].RelPath })
	strip := len(sorted) > 0
	var first string
	for i, f := range sorted {
		segs := strings.Split(f.RelPath, "/")
		if len(segs) < 2 || (i > 0 && segs[0] != first) {
			strip = false
			break
		}
		first = segs[0]
	}
	dirName := map[string]string{} // lower-cased directory path -> the spelling first used
	taken := map[string]bool{}
	out := make([]item, 0, len(sorted))
	for _, f := range sorted {
		segs := strings.Split(f.RelPath, "/")
		if strip {
			segs = segs[1:]
		}
		var parts []string
		for i, seg := range segs {
			if i == len(segs)-1 {
				parts = append(parts, safeInner(seg, "file"))
				continue
			}
			key := strings.ToLower(path.Join(append(append([]string(nil), parts...), safeInner(seg, "folder"))...))
			name, ok := dirName[key]
			if !ok {
				name = safeInner(seg, "folder")
				dirName[key] = name
			}
			parts = append(parts, name)
		}
		rel := path.Join(parts...)
		base := parts[len(parts)-1]
		for n := 2; taken[strings.ToLower(rel)]; n++ {
			parts[len(parts)-1] = suffixedFile(base, n)
			rel = path.Join(parts...)
		}
		taken[strings.ToLower(rel)] = true
		out = append(out, item{f: f, rel: rel})
	}
	return out
}

// build lays the book out, builds it in the hidden folder and publishes it (steps f to k).
func (s *Service) build(ctx context.Context, g guard, up uploadRow, lr *lroot, files []fileRow, n bookNames) error {
	root := lr.path
	roots, err := s.repo.BookRootsIn(ctx, up.FolderID)
	if err != nil {
		return err
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	// Files are always copied (a hard link cannot be made through the os.Root), so the book's size
	// plus 5 % is needed in the library folder.
	need := s.cfg.Margin + total + total/20
	if free, err := s.free(root); err != nil || int64(free) < need {
		return &jobs.Error{Code: "no_space", Text: "The library folder is full. Free some space, then press Try again.", Permanent: true, Err: err}
	}
	items := bookItems(files)
	for attempt := 0; attempt < 5; attempt++ {
		parent, name, ok := layout(lr, n.author, n.series, n.seq, n.title, roots)
		if !ok {
			return &jobs.Error{Code: "layout", Text: "Tendshelf could not find a free place for this book in the library folder.", Permanent: true}
		}
		err := s.buildAt(ctx, g, up, lr, parent, name, items)
		if errors.Is(err, errRetry) {
			continue
		}
		return err
	}
	return &jobs.Error{Code: "layout", Text: "Tendshelf could not find a free place for this book in the library folder.", Permanent: true}
}

// mkdirLedger creates one directory the package owns: the row first, then the directory. Reports
// whether it was created; an existing real directory is reused (false), anything else retries.
func (s *Service) mkdirLedger(ctx context.Context, g guard, up uploadRow, lr *lroot, dir string) (created bool, err error) {
	if !within(lr.path, dir) {
		return false, fmt.Errorf("upload: %q is outside the upload folder", dir)
	}
	fi, lerr := lr.Lstat(dir)
	switch {
	case lerr == nil && fi.IsDir() && fi.Mode()&fs.ModeSymlink == 0:
		return false, nil
	case lerr == nil:
		return false, errRetry
	case !errors.Is(lerr, fs.ErrNotExist):
		return false, lerr
	}
	id, err := s.plan(ctx, g, up.ID, up.FolderID, dir, kindDir, pathPlanned)
	if errors.Is(err, errPathTaken) {
		return false, errRetry
	}
	if err != nil {
		return false, err
	}
	if err := lr.Mkdir(dir, 0o755); err != nil {
		if derr := s.dropPath(ctx, g, id); derr != nil {
			return false, derr
		}
		if errors.Is(err, fs.ErrExist) {
			// Someone else made it between the check and the call. A real directory is reused;
			// the row is gone, so it is never deleted by us.
			if fi, lerr := lr.Lstat(dir); lerr == nil && fi.IsDir() && fi.Mode()&fs.ModeSymlink == 0 {
				return false, nil
			}
			return false, errRetry
		}
		return false, err
	}
	return true, s.markPath(ctx, g, id, pathPresent)
}

// buildAt builds one book at parent/name. It returns errRetry when a name is taken meanwhile.
func (s *Service) buildAt(ctx context.Context, g guard, up uploadRow, lr *lroot, parent, name string, items []item) (err error) {
	root := lr.path
	final := filepath.Join(parent, name)
	temp := filepath.Join(parent, tempPrefix+up.ID)
	if !within(root, final) || !within(root, temp) {
		return fmt.Errorf("upload: %q is outside the upload folder", final)
	}

	// h. Missing ancestors, one by one, each recorded first.
	rel, err := filepath.Rel(root, parent)
	if err != nil {
		return err
	}
	dir := root
	if rel != "." {
		for _, seg := range strings.Split(rel, string(filepath.Separator)) {
			dir = filepath.Join(dir, seg)
			if _, err := s.mkdirLedger(ctx, g, up, lr, dir); err != nil {
				return err
			}
		}
	}
	if err := s.crash("after_dirs"); err != nil {
		return err
	}

	// i. The hidden build folder. temp_path and book_path are stored with its row, before it exists.
	if _, err := lr.Lstat(temp); err == nil {
		return &jobs.Error{Code: "layout", Text: "A leftover build folder is in the way. Remove it, then press Try again.", Permanent: true}
	}
	var tempRow string
	var rowIDs []string // rows planned for this attempt, dropped if it is abandoned
	err = s.write(ctx, g, func(tx *sql.Tx) error {
		id := db.NewID("upp")
		now := s.nowMs()
		if _, err := tx.ExecContext(ctx, `INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, created_at, updated_at)
  VALUES (?,?,?,?,?,?,?,?)`, id, up.ID, up.FolderID, temp, kindDir, pathPlanned, now, now); err != nil {
			return err
		}
		tempRow = id
		_, err := tx.ExecContext(ctx, `UPDATE uploads SET temp_path = ?, book_path = ?, updated_at = ? WHERE id = ?`, temp, final, now, up.ID)
		return err
	})
	if err != nil {
		return errRetryIfTaken(err)
	}
	abandon := func() {
		s.removeBuild(ctx, g, up.ID, lr, temp)
		if _, err := lr.Lstat(temp); errors.Is(err, fs.ErrNotExist) {
			// The folder was never made (or is gone): its planned row describes nothing.
			_ = s.write(ctx, g, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `DELETE FROM upload_paths WHERE id = ? AND state = ?`, tempRow, pathPlanned)
				return err
			})
		}
		for _, id := range rowIDs {
			_ = s.dropPath(ctx, g, id)
		}
		_ = s.write(ctx, g, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE uploads SET temp_path = NULL, book_path = NULL WHERE id = ?`, up.ID)
			return err
		})
	}
	if err := lr.Mkdir(temp, 0o755); err != nil {
		abandon()
		return err
	}
	if err := s.markPath(ctx, g, tempRow, pathPresent); err != nil {
		abandon()
		return err
	}

	// j. Every directory and file, the row first (with the final path it will have).
	plan := func(p, kind, state string) (string, error) {
		id, err := s.plan(ctx, g, up.ID, up.FolderID, p, kind, state)
		if err == nil {
			rowIDs = append(rowIDs, id)
		}
		return id, err
	}
	if _, err := plan(final, kindDir, pathPlanned); err != nil {
		abandon()
		return errRetryIfTaken(err)
	}
	madeDirs := map[string]bool{}
	for _, it := range items {
		relDir := path.Dir(it.rel)
		if relDir != "." {
			cum := ""
			for _, seg := range strings.Split(relDir, "/") {
				cum = path.Join(cum, seg)
				if madeDirs[cum] {
					continue
				}
				if _, err := plan(filepath.Join(final, filepath.FromSlash(cum)), kindDir, pathPlanned); err != nil {
					abandon()
					return errRetryIfTaken(err)
				}
				if err := lr.Mkdir(filepath.Join(temp, filepath.FromSlash(cum)), 0o755); err != nil {
					abandon()
					return err
				}
				madeDirs[cum] = true
			}
		}
		id, err := plan(filepath.Join(final, filepath.FromSlash(it.rel)), kindFile, pathStaged)
		if err != nil {
			abandon()
			return errRetryIfTaken(err)
		}
		dst := filepath.Join(temp, filepath.FromSlash(it.rel))
		if !within(temp, dst) {
			abandon()
			return fmt.Errorf("upload: %q is outside the build folder", dst)
		}
		cp, err := s.copyIn(lr, s.partPath(up.ID, it.f.ID), dst, it.f.Size)
		if err != nil {
			abandon()
			return err
		}
		if err := s.describePath(ctx, g, id, cp.size, cp.mtimeNs, cp.quick, cp.sha); err != nil {
			abandon()
			return err
		}
	}
	if err := s.crash("after_files"); err != nil {
		return err
	}

	// k. Publish: the name must not exist, then one rename.
	if _, err := lr.Lstat(final); err == nil {
		abandon()
		return errRetry
	} else if !errors.Is(err, fs.ErrNotExist) {
		abandon()
		return err
	}
	if err := lr.Rename(temp, final); err != nil {
		abandon()
		if errors.Is(err, fs.ErrExist) || errors.Is(err, fs.ErrNotExist) || isNotEmpty(err) {
			return errRetry
		}
		return err
	}
	if err := s.crash("after_rename"); err != nil {
		return err
	}
	return s.commitPublish(ctx, g, up.ID, up.CreatedBy, final, temp)
}

func errRetryIfTaken(err error) error {
	if errors.Is(err, errPathTaken) {
		return errRetry
	}
	return err
}

// commitPublish makes the published book's rows present and the upload placed, in one transaction.
func (s *Service) commitPublish(ctx context.Context, g guard, uploadID, user, final, temp string) error {
	rows, err := s.pathsOf(ctx, uploadID)
	if err != nil {
		return err
	}
	return s.write(ctx, g, func(tx *sql.Tx) error {
		for _, r := range rows {
			switch {
			case r.Path == temp:
				if err := s.markPathTx(ctx, tx, r.ID, pathRemoved); err != nil {
					return err
				}
			case (r.State == pathPlanned || r.State == pathStaged) && within(final, r.Path):
				if err := s.markPathTx(ctx, tx, r.ID, pathPresent); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE uploads SET state = ?, book_path = ?, temp_path = NULL, error_code = NULL, updated_at = ? WHERE id = ?`,
			StatePlaced, final, s.nowMs(), uploadID); err != nil {
			return err
		}
		return s.audit(ctx, tx, user, "upload.place", "upload:"+uploadID, map[string]string{"path": final})
	})
}

// afterPublish starts the scoped import and removes the staged bytes (steps l and m).
func (s *Service) afterPublish(ctx context.Context, g guard, up uploadRow) (any, error) {
	res := PlaceResult{State: StatePlaced, BookPath: up.BookPath, ScanJobID: up.ScanJobID}
	if up.ScanJobID == "" {
		folder, err := s.repo.Folder(ctx, up.FolderID)
		if err != nil {
			return nil, err
		}
		sub, err := filepath.Rel(folder.Path, up.BookPath)
		if err != nil || !within(folder.Path, up.BookPath) {
			return nil, fmt.Errorf("upload: book path %q is outside its folder", up.BookPath)
		}
		job, err := s.enq.Enqueue(ctx, scan.KindScan, scan.ScanPayload{LibraryID: up.LibraryID, FolderID: up.FolderID, Subdir: filepath.ToSlash(sub)},
			jobs.EnqueueOptions{CreatedBy: up.CreatedBy})
		if err != nil {
			return nil, fmt.Errorf("queue import: %w", err)
		}
		err = s.write(ctx, g, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE uploads SET scan_job_id = ? WHERE id = ?`, job, up.ID)
			return err
		})
		if err != nil {
			return nil, err
		}
		res.ScanJobID = job
	}
	s.removeStaging(up.ID)
	s.forget(up.ID)
	return res, nil
}

// copied describes a file copied into the build folder.
type copied struct {
	sha, quick    string
	size, mtimeNs int64
}

// copyIn copies a staged part file to dst, which must not exist (O_EXCL), through the library
// root. The copy is synced, read back and compared by SHA-256 with what was read from staging, so a
// library file never shares an inode with staging and is proven intact before it is published.
func (s *Service) copyIn(lr *lroot, src, dst string, size int64) (copied, error) {
	var c copied
	in, err := os.Open(src)
	if err != nil {
		return c, err
	}
	defer in.Close()
	out, err := lr.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return c, err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		out.Close()
		return c, err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return c, err
	}
	if err := out.Close(); err != nil {
		return c, err
	}
	c.sha = hex.EncodeToString(h.Sum(nil))
	back, err := lr.Open(dst)
	if err != nil {
		return c, err
	}
	defer back.Close()
	bh := sha256.New()
	if _, err := io.Copy(bh, back); err != nil {
		return c, err
	}
	if hex.EncodeToString(bh.Sum(nil)) != c.sha {
		return c, errors.New("upload: the copy does not match the staged file")
	}
	if c.quick, c.size, err = media.QuickHashFile(back); err != nil {
		return c, err
	}
	if c.size != size {
		return c, fmt.Errorf("upload: %s is %d bytes, expected %d", filepath.Base(dst), c.size, size)
	}
	fi, err := lr.Lstat(dst)
	if err != nil {
		return c, err
	}
	c.mtimeNs = fi.ModTime().UnixNano()
	return c, nil
}

// removeBuild deletes the hidden build folder of an upload. It is one of only two RemoveAll calls
// in the package: the folder must have the exact name for this upload and be a real directory, not
// a link, and the removal runs through the library root.
func (s *Service) removeBuild(ctx context.Context, g guard, uploadID string, lr *lroot, temp string) {
	if filepath.Base(temp) != tempPrefix+uploadID || !within(lr.path, temp) || filepath.Clean(temp) == lr.path {
		return
	}
	fi, err := lr.Lstat(temp)
	if err != nil || !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 {
		return
	}
	if err := lr.RemoveAll(temp); err != nil {
		s.log.Warn("upload: cannot remove the build folder", "upload", uploadID, "err", err)
		return
	}
	rows, err := s.pathsOf(ctx, uploadID)
	if err != nil {
		return
	}
	for _, r := range rows {
		if r.Path == temp && r.State != pathRemoved {
			_ = s.markPath(ctx, g, r.ID, pathRemoved)
		}
	}
}

func isNotEmpty(err error) bool { return errors.Is(err, syscall.ENOTEMPTY) }
