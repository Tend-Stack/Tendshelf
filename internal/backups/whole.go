package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// Whole backups (S2). The database copy alone does not hold uploaded ebooks, covers, author pictures,
// the text of ebooks or the dictionaries the owner added. When the owner picks a backup folder, every
// backup also mirrors those folders of the data folder into it, incrementally. The audio library is never
// copied, and neither are caches or half-finished uploads.
//
// The backup folder holds one folder, RootName, laid out like the data folder so that restoring is a
// plain copy back:
//
//	tendshelf-backup/backups/tendshelf-copy-v<n>-<time>.db   the newest database copies (db.CopyKeep)
//	tendshelf-backup/ebooks, covers, people, text, dictionaries   mirrors of the same folders
//	tendshelf-backup/manifest.json   what was copied (size, time, SHA-256); how an unchanged file is skipped
//	tendshelf-backup/RESTORE.txt     the restore steps in plain words
//
// Nothing here writes outside the backup folder or changes the data folder.

// Settings and names.
const (
	SettingFolder   = "backup.folder"
	SettingFolderOK = "backup.folder_ok_at" // Unix ms of the last whole backup that finished and verified
	RootName        = "tendshelf-backup"
	manifestName    = "manifest.json"
	restoreName     = "RESTORE.txt"
	partSuffix      = ".tendshelf-part"

	// Grace is how long a file that vanished from the data folder stays in the backup before it is
	// removed. It bounds the backup's size while still surviving a deletion made by mistake.
	Grace = 30 * 24 * time.Hour
)

// Mirrored are the data folder's own folders that a whole backup copies. Everything else (caches, the
// upload staging area, the local backups folder, the database files) is skipped on purpose; the database
// goes through the verified copies in backups/.
var Mirrored = []string{"ebooks", "covers", "people", "text", "dictionaries"}

// Reasons a folder is refused, each safe to show.
var (
	ErrFolderRelative   = errors.New("backups: the backup folder must be a full path")
	ErrFolderMissing    = errors.New("backups: the backup folder does not exist")
	ErrFolderNotDir     = errors.New("backups: the backup folder is not a folder")
	ErrFolderInData     = errors.New("backups: the backup folder is inside the data folder")
	ErrFolderInLibrary  = errors.New("backups: the backup folder is inside a library folder")
	ErrFolderNotWritten = errors.New("backups: the backup folder cannot be written to")
	// ErrDiskFull means the backup folder ran out of space; the copy stopped cleanly.
	ErrDiskFull = errors.New("backups: the backup folder is full")
)

// CheckFolder validates a backup folder: a full path to an existing folder that can be written to and is
// not inside the data folder or any library folder (a copy there would end up in the books, or be wiped
// with the data it protects). libraries are the library folder paths. Symbolic links are resolved first.
func CheckFolder(target, dataDir string, libraries []string) error {
	if !filepath.IsAbs(target) {
		return ErrFolderRelative
	}
	real, err := filepath.EvalSymlinks(target)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrFolderMissing
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFolderNotWritten, err)
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return ErrFolderNotDir
	}
	if inside(resolved(dataDir), real) {
		return ErrFolderInData
	}
	for _, l := range libraries {
		if inside(resolved(l), real) {
			return ErrFolderInLibrary
		}
	}
	probe, err := os.CreateTemp(real, ".tendshelf-write-test-*")
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			return ErrDiskFull
		}
		return fmt.Errorf("%w: %v", ErrFolderNotWritten, err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return nil
}

// resolved is path with symbolic links resolved when possible, and made absolute and clean.
func resolved(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		path = r
	}
	if a, err := filepath.Abs(path); err == nil {
		path = a
	}
	return filepath.Clean(path)
}

// inside reports whether child is dir itself or below it.
func inside(dir, child string) bool {
	rel, err := filepath.Rel(dir, child)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

// Report is what one whole backup did.
type Report struct {
	Copied  int   // files copied or updated
	Same    int   // files already in the backup and unchanged
	Removed int   // files removed after being gone from the data folder for Grace
	Bytes   int64 // bytes copied this time
	Copies  int   // database copies placed in the backup folder
}

// entry is one file in the manifest.
type entry struct {
	Size         int64  `json:"size"`
	MTime        int64  `json:"mtime"` // Unix ms of the source file
	SHA256       string `json:"sha256"`
	MissingSince int64  `json:"missing_since,omitempty"`
}

type manifest struct {
	Version int              `json:"version"`
	Files   map[string]entry `json:"files"` // slash-form path below the backup root
}

func readManifest(path string) manifest {
	m := manifest{Version: 1, Files: map[string]entry{}}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &m) != nil || m.Files == nil {
		// Unreadable or from nowhere: start over. Every file is then compared to what is there and copied again.
		return manifest{Version: 1, Files: map[string]entry{}}
	}
	return m
}

func (m manifest) write(path string) error {
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	tmp := path + partSuffix
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// wrapDest lets a test make the destination fail the way a full disk does.
var wrapDest = func(w io.Writer) io.Writer { return w }

// Mirror copies the data folder's own files and the database copies into <target>/tendshelf-backup,
// incrementally: a file whose size and time match the manifest is skipped. Each copy goes to a temporary
// name, is read back and compared by SHA-256 with the source, and only then takes its place. A file that
// vanished from the data folder is removed from the backup after Grace. A full disk stops it cleanly with
// ErrDiskFull; what was copied up to then stays and is recorded.
func Mirror(ctx context.Context, dataDir, target string, now time.Time) (Report, error) {
	var rep Report
	root := filepath.Join(target, RootName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return rep, diskErr(err)
	}
	mpath := filepath.Join(root, manifestName)
	m := readManifest(mpath)
	// Whatever happens next, record the progress: a re-run then skips what is done.
	defer func() {
		if err := m.write(mpath); err != nil {
			_ = err // the next run re-verifies the files it cannot find in the manifest
		}
	}()
	removeParts(root)
	seen := map[string]bool{}
	for _, name := range Mirrored {
		src := filepath.Join(dataDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() { // folders are made as needed; links and devices are never followed
				return nil
			}
			rel, err := filepath.Rel(dataDir, p)
			if err != nil {
				return nil
			}
			key := filepath.ToSlash(rel)
			seen[key] = true
			return mirrorFile(root, p, key, &m, &rep, now)
		})
		if err != nil {
			return rep, err
		}
	}
	// Files gone from the data folder: keep them for Grace, then remove them.
	for key, e := range m.Files {
		if seen[key] || strings.HasPrefix(key, "backups/") {
			continue
		}
		if e.MissingSince == 0 {
			e.MissingSince = now.UnixMilli()
			m.Files[key] = e
			continue
		}
		if now.Sub(time.UnixMilli(e.MissingSince)) >= Grace {
			os.Remove(filepath.Join(root, filepath.FromSlash(key)))
			delete(m.Files, key)
			rep.Removed++
		}
	}
	// A file that came back stops counting as gone (mirrorFile cleared it when it copied or matched it).
	if err := mirrorCopies(ctx, dataDir, root, &rep); err != nil {
		return rep, err
	}
	if err := writeRestoreNote(root); err != nil {
		return rep, diskErr(err)
	}
	return rep, nil
}

// diskErr turns a "no space left" failure into ErrDiskFull and leaves any other error as it is.
func diskErr(err error) error {
	if errors.Is(err, syscall.ENOSPC) {
		return fmt.Errorf("%w: %v", ErrDiskFull, err)
	}
	return err
}

// removeParts deletes temporary files an interrupted run left behind.
func removeParts(root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, partSuffix) {
			os.Remove(p)
		}
		return nil
	})
}

// mirrorFile copies one file when it is new or changed.
func mirrorFile(root, src, key string, m *manifest, rep *Report, now time.Time) error {
	fi, err := os.Stat(src)
	if err != nil {
		return nil // gone since the walk: the next run sees it
	}
	dst := filepath.Join(root, filepath.FromSlash(key))
	mtime := fi.ModTime().UnixMilli()
	if e, ok := m.Files[key]; ok && e.Size == fi.Size() && e.MTime == mtime {
		if di, err := os.Stat(dst); err == nil && di.Size() == e.Size {
			e.MissingSince = 0
			m.Files[key] = e
			rep.Same++
			return nil
		}
	}
	sum, err := copyVerified(src, dst, fi.Size())
	if err != nil {
		return diskErr(err)
	}
	m.Files[key] = entry{Size: fi.Size(), MTime: mtime, SHA256: sum}
	rep.Copied++
	rep.Bytes += fi.Size()
	return nil
}

// copyVerified copies src to dst through a temporary name and returns the SHA-256. The copy is read back
// and compared before it replaces anything, and a source that changed while it was read is refused.
func copyVerified(src, dst string, size int64) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp := dst + partSuffix
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(wrapDest(out), io.TeeReader(in, h))
	if err != nil {
		return fail(err)
	}
	if err := out.Sync(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if after, err := os.Stat(src); err != nil || after.Size() != n || n != size {
		os.Remove(tmp)
		return "", fmt.Errorf("%s changed while it was copied", filepath.Base(src))
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if back, err := hashFile(tmp); err != nil || back != sum {
		os.Remove(tmp)
		return "", fmt.Errorf("the copy of %s did not read back the same", filepath.Base(src))
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return sum, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// mirrorCopies places the data folder's database copies into <root>/backups, verifies each one, and keeps
// the newest db.CopyKeep there.
func mirrorCopies(ctx context.Context, dataDir, root string, rep *Report) error {
	local, err := db.ListCopies(dataDir)
	if err != nil {
		return err
	}
	for _, c := range local {
		if err := ctx.Err(); err != nil {
			return err
		}
		dst := filepath.Join(root, "backups", c.Name)
		if di, err := os.Stat(dst); err == nil && di.Size() == c.Bytes {
			continue
		}
		src := filepath.Join(dataDir, "backups", c.Name)
		if _, err := copyVerified(src, dst, c.Bytes); err != nil {
			return diskErr(err)
		}
		if err := db.VerifyCopy(ctx, dst, c.Version); err != nil {
			os.Remove(dst)
			return fmt.Errorf("the database copy %s did not verify: %w", c.Name, err)
		}
		rep.Copies++
	}
	kept, err := db.ListCopies(root)
	if err != nil {
		return err
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].At.After(kept[j].At) })
	for i := db.CopyKeep; i < len(kept); i++ {
		os.Remove(filepath.Join(root, "backups", kept[i].Name))
	}
	return nil
}

// RestoreNote is the text of RESTORE.txt, written into the backup folder so it is there when the
// computer that made it is not.
const RestoreNote = `How to bring Tendshelf back from this backup

1. Stop Tendshelf. If it is installed on a new computer, install it but do not start it yet.
2. Find the data folder: the default is ~/.local/share/tendshelf on Linux, Library/Application Support/Tendshelf
   on a Mac and %LOCALAPPDATA%\Tendshelf on Windows (or the folder you gave with -data). Use an empty
   folder, or move the old one aside first.
3. Copy these folders from this backup into the data folder: ebooks, covers, people, text, dictionaries.
   Leave out manifest.json and this note.
4. Open the folder called backups here. Pick the newest file (the biggest date in its name). Copy it into
   the data folder and rename it to tendshelf.db.
5. Start Tendshelf. Your people, progress, bookmarks, ebooks and covers are back. Your audiobook files were
   never in this backup: point your libraries at the same folders and Tendshelf finds them again.
`

func writeRestoreNote(root string) error {
	p := filepath.Join(root, restoreName)
	if b, err := os.ReadFile(p); err == nil && string(b) == RestoreNote {
		return nil
	}
	return os.WriteFile(p, []byte(RestoreNote), 0o600)
}
