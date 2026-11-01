package mediajobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder for image.DecodeConfig
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// Outcomes of Covers.Run.
const (
	OutcomeSet       = "set"       // a cover row or the book's cover changed
	OutcomeUnchanged = "unchanged" // the same cover was already in place; nothing was written
	OutcomeNone      = "none"      // the book has no usable picture
	OutcomeGone      = "gone"      // the book no longer exists
	OutcomeMissing   = "missing"   // the book is marked missing
)

const (
	maxFolderImage = 20 << 20 // bytes
	maxDimension   = 10000    // pixels, either side
)

// Covers finds a book's cover picture, stores it, makes its thumbnails and works out its accent
// colours. It only reads library files and writes under the data directory.
type Covers struct {
	repo  *library.Repo
	tools media.Tools
	data  string
	jobs  Enqueuer
	log   *slog.Logger
	now   func() time.Time
}

// NewCovers returns the cover service. data is the data directory. Stale temporary files from
// earlier crashes are swept once, here.
func NewCovers(repo *library.Repo, tools media.Tools, data string, enq Enqueuer, log *slog.Logger) *Covers {
	if log == nil {
		log = slog.Default()
	}
	c := &Covers{repo: repo, tools: tools, data: data, jobs: enq, log: log, now: time.Now}
	sweepTemp(CoversDir(data), c.now())
	sweepTemp(ThumbsDir(data), c.now())
	return c
}

// candidate is a picture chosen for a book, checked to decode, not yet stored.
type candidate struct {
	data          []byte
	ext, mime     string
	source        string
	width, height int
}

// folderStems and folderExts give the preference order of folder pictures: cover.* before folder.*.
var (
	folderStems = []string{"cover", "folder"}
	folderExts  = []string{"jpg", "jpeg", "png", "webp"}
)

// folderPictures lists the regular, non-symlink picture files of dir named cover.* or folder.*
// (any case), best first.
func folderPictures(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type found struct {
		rank int
		name string
	}
	var list []found
	for _, e := range entries {
		if !e.Type().IsRegular() { // a symlink reports its own type here, so it is skipped
			continue
		}
		lower := strings.ToLower(e.Name())
		ext := strings.TrimPrefix(path.Ext(lower), ".")
		stem := strings.TrimSuffix(lower, path.Ext(lower))
		si, ei := indexOf(folderStems, stem), indexOf(folderExts, ext)
		if si < 0 || ei < 0 {
			continue
		}
		list = append(list, found{si*len(folderExts) + ei, e.Name()})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].rank != list[j].rank {
			return list[i].rank < list[j].rank
		}
		return list[i].name < list[j].name
	})
	out := make([]string, len(list))
	for i, f := range list {
		out[i] = filepath.Join(dir, f.name)
	}
	return out
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// check decodes the header of a picture and says what it is. Only JPEG and PNG within the size
// limit pass.
func check(data []byte, source string) (*candidate, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	c := &candidate{data: data, source: source, width: cfg.Width, height: cfg.Height}
	switch format {
	case "jpeg":
		c.ext, c.mime = "jpg", "image/jpeg"
	case "png":
		c.ext, c.mime = "png", "image/png"
	default:
		return nil, errors.New("not a JPEG or PNG picture")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxDimension || cfg.Height > maxDimension {
		return nil, &media.Error{Code: media.CodeImageTooLarge, Text: media.ErrorText(media.CodeImageTooLarge)}
	}
	return c, nil
}

// fatal reports whether an error should end the run instead of moving on to the next candidate:
// the context ended, or ffmpeg is missing or too slow (a retry or a configuration fix helps).
func fatal(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	var me *media.Error
	return errors.As(err, &me) && (me.Code == media.CodeFFmpegMissing || me.Code == media.CodeFFmpegTimeout)
}

func playable(f *library.AudioFile) bool {
	return f.MissingSince == 0 && f.ProbeJSON != nil && !f.Encrypted && f.ProbeError == "" &&
		media.Classify(f.Path) == media.KindAudio
}

// loadFolder reads one folder picture; a WebP goes through ffmpeg to PNG. A nil candidate with a nil
// error means the file cannot be used and the next one should be tried.
func (c *Covers) loadFolder(ctx context.Context, p string) (*candidate, error) {
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, nil
	}
	if fi.Size() > maxFolderImage {
		c.log.Warn("cover skipped", "file", p, "code", media.CodeImageTooLarge)
		return nil, nil
	}
	var data []byte
	if strings.EqualFold(path.Ext(p), ".webp") {
		if data, err = c.tools.ImageToPNG(ctx, p); err != nil {
			if fatal(ctx, err) {
				return nil, err
			}
			c.log.Warn("cover skipped", "file", p, "err", err)
			return nil, nil
		}
	} else {
		f, err := os.Open(p)
		if err != nil {
			c.log.Warn("cover skipped", "file", p, "err", err)
			return nil, nil
		}
		defer f.Close()
		if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
			return nil, nil
		}
		if data, err = io.ReadAll(io.LimitReader(f, maxFolderImage+1)); err != nil || len(data) > maxFolderImage {
			c.log.Warn("cover skipped", "file", p, "err", err)
			return nil, nil
		}
	}
	cand, err := check(data, library.CoverFolder)
	if err != nil {
		c.log.Warn("cover skipped", "file", p, "err", err)
		return nil, nil
	}
	return cand, nil
}

// loadEmbedded extracts the first attached picture of the first playable file that has one.
func (c *Covers) loadEmbedded(ctx context.Context, files []library.AudioFile) (*candidate, error) {
	for i := range files {
		f := &files[i]
		if !playable(f) {
			continue
		}
		var p media.Probe
		if json.Unmarshal(f.ProbeJSON, &p) != nil || len(p.Pictures) == 0 || p.Encrypted {
			continue
		}
		data, _, err := c.tools.ExtractPicture(ctx, f.Path, p.Pictures[0])
		if err != nil {
			if fatal(ctx, err) {
				return nil, err
			}
			c.log.Warn("embedded cover skipped", "file", f.Path, "err", err)
			return nil, nil
		}
		cand, err := check(data, library.CoverEmbedded)
		if err != nil {
			c.log.Warn("embedded cover skipped", "file", f.Path, "err", err)
			return nil, nil
		}
		return cand, nil
	}
	return nil, nil
}

// pick chooses the picture: a folder image the owner placed (folder books only), else the
// embedded picture, else none.
func (c *Covers) pick(ctx context.Context, book library.Book, files []library.AudioFile) (*candidate, error) {
	single := false
	for i := range files {
		if files[i].Path == book.RootPath {
			single = true
		}
	}
	if !single {
		for _, p := range folderPictures(book.RootPath) {
			cand, err := c.loadFolder(ctx, p)
			if err != nil {
				return nil, err
			}
			if cand != nil {
				return cand, nil
			}
		}
	}
	return c.loadEmbedded(ctx, files)
}

// coverEqual says whether a stored row already holds what the job would write.
func coverEqual(a, b library.Cover) bool {
	return a.Source == b.Source && a.Mime == b.Mime && a.Width == b.Width && a.Height == b.Height &&
		a.Path == b.Path && a.Tint == b.Tint && bytes.Equal(a.Accent, b.Accent)
}

func ours(source string) bool {
	return source == library.CoverFolder || source == library.CoverEmbedded
}

// Run finds, stores and records the cover of one book. The files are written before the database
// transaction, which starts with guard (a job's fence check; nil is allowed); a second run on an
// unchanged book writes no row and no file. A folder picture is read directly, so the run needs no
// ffmpeg for it beyond the thumbnails that are missing; an embedded picture is extracted again on
// every run to be compared. A book with no usable picture keeps whatever cover it has.
func (c *Covers) Run(ctx context.Context, bookID string, guard func(context.Context, *sql.Tx) error) (string, error) {
	book, err := c.repo.Book(ctx, bookID)
	switch {
	case errors.Is(err, library.ErrNotFound):
		return OutcomeGone, nil
	case err != nil:
		return "", err
	case book.MissingSince != 0:
		return OutcomeMissing, nil
	}
	files, err := c.repo.AudioFiles(ctx, bookID)
	if err != nil {
		return "", err
	}
	cand, err := c.pick(ctx, book, files)
	if err != nil {
		return "", err
	}
	if cand == nil {
		return OutcomeNone, nil
	}

	sum := sha256.Sum256(cand.data)
	sha := hex.EncodeToString(sum[:])
	rel := path.Join("covers", sha+"."+cand.ext)
	coverFile := filepath.Join(CoversDir(c.data), sha+"."+cand.ext)
	if !exists(coverFile) {
		if err := writeAtomic(CoversDir(c.data), sha+"."+cand.ext, cand.data); err != nil {
			return "", err
		}
	}
	for _, box := range ThumbSizes {
		if err := c.thumbnail(ctx, coverFile, sha, box); err != nil {
			return "", err
		}
	}

	cv := library.Cover{BookID: bookID, Source: cand.source, Mime: cand.mime, Width: cand.width, Height: cand.height,
		SHA256: sha, Path: rel}
	if err := c.colours(&cv, sha); err != nil {
		return "", err
	}

	outcome := OutcomeUnchanged
	var stale []library.Cover
	err = c.repo.UpdateGuarded(ctx, guard, func(tx *library.Tx) error {
		cur, err := tx.Book(bookID)
		switch {
		case errors.Is(err, library.ErrNotFound):
			outcome = OutcomeGone
			return nil
		case err != nil:
			return err
		case cur.MissingSince != 0:
			outcome = OutcomeMissing
			return nil
		}
		rows, err := tx.Covers(bookID)
		if err != nil {
			return err
		}
		var same, current *library.Cover
		for i := range rows {
			if rows[i].SHA256 == sha {
				same = &rows[i]
			}
			if rows[i].ID == cur.CoverID {
				current = &rows[i]
			}
		}
		if same != nil && !ours(same.Source) {
			return nil // the owner uploaded or chose this very picture; their row stays as it is
		}
		if same == nil || !coverEqual(*same, cv) {
			outcome = OutcomeSet
		}
		if err := tx.PutCover(&cv); err != nil {
			return err
		}
		if (cur.CoverID == "" || current == nil || ours(current.Source)) && cur.CoverID != cv.ID {
			if err := tx.SetBookCover(bookID, cv.ID); err != nil {
				return err
			}
			outcome = OutcomeSet
		}
		for _, r := range rows {
			if r.ID != cv.ID && ours(r.Source) {
				if err := tx.DeleteCover(r.ID); err != nil {
					return err
				}
				stale = append(stale, r)
				outcome = OutcomeSet
			}
		}
		return nil
	})
	if err != nil {
		c.removeIfUnreferenced(context.WithoutCancel(ctx), library.Cover{SHA256: sha, Path: rel})
		return "", err
	}
	if outcome == OutcomeGone || outcome == OutcomeMissing {
		stale = append(stale, library.Cover{SHA256: sha, Path: rel})
	}
	for _, r := range stale {
		c.removeIfUnreferenced(ctx, r)
	}
	return outcome, nil
}

// thumbnail makes one thumbnail if it is not there yet.
func (c *Covers) thumbnail(ctx context.Context, coverFile, sha string, box int) error {
	dst := ThumbPath(c.data, sha, box)
	if exists(dst) {
		return nil
	}
	if err := os.MkdirAll(ThumbsDir(c.data), dirMode); err != nil {
		return err
	}
	tmp := filepath.Join(ThumbsDir(c.data), tmpPrefix+sha[:16]+"-"+strconv.Itoa(box)+".jpg")
	if err := c.tools.Thumbnail(ctx, coverFile, tmp, box); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, fileMode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// colours fills the tint and accent of cv from its 160 px thumbnail.
func (c *Covers) colours(cv *library.Cover, sha string) error {
	f, err := os.Open(ThumbPath(c.data, sha, ThumbSizes[0]))
	if err != nil {
		return err
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		c.log.Warn("thumbnail does not decode; the cover gets no accent", "sha256", sha, "err", err)
		return nil
	}
	tint, ok := media.Tint(img)
	if !ok {
		return nil
	}
	cv.Tint = tint
	if acc, ok := media.Accents(tint); ok {
		cv.Accent, err = json.Marshal(acc) // map keys are sorted, so equal colours give equal bytes
		return err
	}
	return nil
}

// removeIfUnreferenced deletes the stored file and thumbnails of a cover row that was just removed,
// unless another row (of any book) still uses the same picture.
func (c *Covers) removeIfUnreferenced(ctx context.Context, r library.Cover) {
	refs, err := c.repo.CoversBySHA(ctx, r.SHA256)
	if err != nil {
		c.log.Warn("cover files kept", "sha256", r.SHA256, "err", err)
		return
	}
	if len(refs) > 0 || len(r.SHA256) != 64 {
		return
	}
	if p := filepath.Join(c.data, filepath.FromSlash(r.Path)); filepath.Dir(p) == CoversDir(c.data) {
		os.Remove(p)
	}
	for _, box := range ThumbSizes {
		os.Remove(ThumbPath(c.data, r.SHA256, box))
	}
}

// Follower returns the scan follower: it queues a media.cover job for each settled book that has
// none waiting.
func (c *Covers) Follower() func(context.Context, []string) {
	return func(ctx context.Context, bookIDs []string) {
		active, err := c.jobs.ActiveKeys(ctx, KindCover, "book_id")
		if err != nil {
			c.log.Error("cover jobs: cannot list waiting jobs", "err", err)
			return
		}
		for _, id := range bookIDs {
			if active[id] {
				continue
			}
			if _, err := c.jobs.Enqueue(ctx, KindCover, CoverPayload{BookID: id}, jobs.EnqueueOptions{}); err != nil {
				c.log.Error("cover jobs: cannot queue", "book", id, "err", err)
			}
		}
	}
}
