package mediajobs

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// KindCoverAdopt is the job that stores a picture the owner uploaded or chose online as a book's cover.
const KindCoverAdopt = "media.cover_adopt"

// Codes of the plain refusals of a picture that is not usable as a cover.
const (
	CodeNotPicture = "not_picture"
	CodeTooLarge   = "too_large"
)

const (
	// maxCoverBytes is the largest picture accepted as an uploaded or online cover.
	maxCoverBytes = 5 << 20
	// maxChosenCovers is how many uploaded or online covers a book keeps.
	maxChosenCovers = 6
	// stagedMaxAge is how long a staged upload may wait for its job before it is swept.
	stagedMaxAge = 24 * time.Hour
)

var stagedName = regexp.MustCompile(`^[0-9a-f]{16}\.img$`)

// AdoptPayload is the payload of a media.cover_adopt job. Source is "upload" (Staged names a file
// from Covers.Stage) or "online" (URL is the picture to fetch).
type AdoptPayload struct {
	BookID string `json:"book_id"`
	Source string `json:"source"`
	Staged string `json:"staged,omitempty"`
	URL    string `json:"url,omitempty"`
	// IfNoCover makes the job give way to any cover the book has: automatic lookups use it, since
	// they never replace a picture.
	IfNoCover bool `json:"if_no_cover,omitempty"`
}

// errCoverPresent is what the guard of an IfNoCover adoption returns when the book got a cover
// meanwhile; the job then ends as done and kept.
var errCoverPresent = errors.New("mediajobs: the book already has a cover")

// FillOnline queues the adoption of an online picture for a book that has no cover. It does
// nothing when the book has a cover or a media.cover job for it is waiting or running, since the
// scan's own picture would win. The adoption job checks again inside its transaction.
func (c *Covers) FillOnline(ctx context.Context, bookID, url string) error {
	if url == "" {
		return nil
	}
	b, err := c.repo.Book(ctx, bookID)
	if err != nil {
		return err
	}
	if b.CoverID != "" {
		return nil
	}
	active, err := c.jobs.ActiveKeys(ctx, KindCover, "book_id")
	if err != nil {
		return err
	}
	if active[bookID] {
		return nil
	}
	_, err = c.jobs.Enqueue(ctx, KindCoverAdopt, AdoptPayload{BookID: bookID, Source: library.CoverOnline, URL: url, IfNoCover: true}, jobs.EnqueueOptions{})
	return err
}

// ifNoCoverGuard runs fence, then refuses with errCoverPresent when the book has a cover.
func ifNoCoverGuard(fence func(context.Context, *sql.Tx) error, bookID string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		if err := fence(ctx, tx); err != nil {
			return err
		}
		if has, err := bookHasCover(ctx, tx, bookID); err != nil {
			return err
		} else if has {
			return errCoverPresent
		}
		return nil
	}
}

// bookHasCover reads cover_id inside tx.
func bookHasCover(ctx context.Context, tx *sql.Tx, bookID string) (bool, error) {
	var id sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT cover_id FROM books WHERE id = ?`, bookID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return id.Valid && id.String != "", err
}

// IncomingDir is where uploaded pictures wait for their job.
func IncomingDir(data string) string { return filepath.Join(CoversDir(data), "incoming") }

// sweepStaged removes files older than a day from the staging directory: uploads whose job never ran.
func sweepStaged(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() && now.Sub(fi.ModTime()) > stagedMaxAge {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func coverError(code, text string) error { return &media.Error{Code: code, Text: text} }

const notPictureText = "This file is not a JPEG, PNG or WebP picture."

// imageKind says what the first bytes of data are: "jpeg", "png", "webp" or "" for anything else.
func imageKind(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return "jpeg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "webp"
	}
	return ""
}

// precheck refuses a picture by its size and its first bytes.
func precheck(data []byte) (string, error) {
	if len(data) > maxCoverBytes {
		return "", coverError(CodeTooLarge, "This picture is larger than 5 MB, so Tendshelf will not use it as a cover.")
	}
	kind := imageKind(data)
	if kind == "" {
		return "", coverError(CodeNotPicture, notPictureText)
	}
	return kind, nil
}

// SetFetcher sets how an online adoption downloads its picture (url in; bytes and MIME type out).
// Without one, online adoptions fail with online_off.
func (c *Covers) SetFetcher(f func(ctx context.Context, url string) ([]byte, string, error)) {
	c.fetch = f
}

// Stage keeps an uploaded picture until its job runs and returns the staged name for AdoptPayload.
// The picture is checked by its first bytes and its size only; the job checks it fully.
func (c *Covers) Stage(data []byte) (string, error) {
	if _, err := precheck(data); err != nil {
		return "", err
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	name := hex.EncodeToString(b[:]) + ".img"
	if err := writeAtomic(IncomingDir(c.data), name, data); err != nil {
		return "", err
	}
	return name, nil
}

// Choices returns the book's own cover rows, oldest first.
func (c *Covers) Choices(ctx context.Context, bookID string) ([]library.Cover, error) {
	return c.repo.Covers(ctx, bookID)
}

// Choose makes one of the book's own covers its current cover. Any other cover id gives
// library.ErrNotFound.
func (c *Covers) Choose(ctx context.Context, bookID, coverID string) error {
	return c.repo.UpdateGuarded(ctx, nil, func(tx *library.Tx) error {
		cv, err := tx.Cover(coverID)
		if err != nil {
			return err
		}
		if cv.BookID != bookID {
			return library.ErrNotFound
		}
		return tx.SetBookCover(bookID, coverID)
	})
}

// Adopt checks a picture, stores it with its thumbnails and colours, and makes it the book's cover.
// source is "upload" or "online". Past six uploaded or online covers the oldest one that is not
// current is deleted with its files. It returns the cover id. Nothing is left behind on a refusal.
func (c *Covers) Adopt(ctx context.Context, bookID, source string, data []byte, guard func(context.Context, *sql.Tx) error) (string, error) {
	if source != library.CoverUpload && source != library.CoverOnline {
		return "", errors.New("cover source must be upload or online")
	}
	kind, err := precheck(data)
	if err != nil {
		return "", err
	}
	if kind == "webp" {
		if data, err = c.webpToPNG(ctx, data); err != nil {
			return "", err
		}
	}
	cand, err := check(data, source)
	if err != nil {
		var me *media.Error
		if errors.As(err, &me) {
			return "", err
		}
		return "", coverError(CodeNotPicture, notPictureText)
	}

	sum := sha256.Sum256(cand.data)
	sha := hex.EncodeToString(sum[:])
	rel := path.Join("covers", sha+"."+cand.ext)
	coverFile := filepath.Join(CoversDir(c.data), sha+"."+cand.ext)
	// A picture that was already stored belongs to someone else's row; only new files are cleaned up.
	fresh := !exists(coverFile)
	if fresh {
		if err := writeAtomic(CoversDir(c.data), sha+"."+cand.ext, cand.data); err != nil {
			return "", err
		}
	}
	cleanup := func() { c.removeIfUnreferenced(context.WithoutCancel(ctx), library.Cover{SHA256: sha, Path: rel}) }
	for _, box := range ThumbSizes {
		if err := c.thumbnail(ctx, coverFile, sha, box); err != nil {
			cleanup()
			return "", err
		}
	}
	cv := library.Cover{BookID: bookID, Source: source, Mime: cand.mime, Width: cand.width, Height: cand.height,
		SHA256: sha, Path: rel}
	if err := c.colours(&cv, sha); err != nil {
		cleanup()
		return "", err
	}

	var pruned []library.Cover
	err = c.repo.UpdateGuarded(ctx, guard, func(tx *library.Tx) error {
		if err := tx.PutCover(&cv); err != nil {
			return err
		}
		if err := tx.SetBookCover(bookID, cv.ID); err != nil {
			return err
		}
		var err error
		pruned, err = tx.PruneChosenCovers(bookID, maxChosenCovers)
		return err
	})
	if err != nil {
		cleanup()
		return "", err
	}
	for _, r := range pruned {
		c.removeIfUnreferenced(ctx, r)
	}
	return cv.ID, nil
}

// webpToPNG converts a WebP picture with the same ffmpeg path folder pictures use. ffmpeg reads a
// file, so the bytes wait in a temporary file in the covers directory meanwhile.
func (c *Covers) webpToPNG(ctx context.Context, data []byte) ([]byte, error) {
	if err := os.MkdirAll(CoversDir(c.data), dirMode); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(CoversDir(c.data), tmpPrefix+"adopt-*.webp")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	out, err := c.tools.ImageToPNG(ctx, f.Name())
	if err != nil {
		if fatal(ctx, err) {
			return nil, err
		}
		c.log.Warn("cover picture cannot be converted", "err", err)
		return nil, coverError(CodeNotPicture, notPictureText)
	}
	return out, nil
}

func (c *Covers) adoptHandler(ctx context.Context, t *jobs.Task) (any, error) {
	var p AdoptPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.BookID == "" {
		return nil, badPayload(err)
	}
	if p.IfNoCover {
		if b, err := c.repo.Book(ctx, p.BookID); err == nil && b.CoverID != "" {
			return map[string]bool{"kept": true}, nil
		}
	}
	var data []byte
	var staged string
	switch p.Source {
	case library.CoverUpload:
		if !stagedName.MatchString(p.Staged) {
			return nil, badPayload(nil)
		}
		staged = filepath.Join(IncomingDir(c.data), p.Staged)
		var err error
		if data, err = os.ReadFile(staged); err != nil {
			return nil, &jobs.Error{Code: "upload_gone", Text: "The uploaded picture is no longer there. Please upload it again.", Permanent: true, Err: err}
		}
	case library.CoverOnline:
		if p.URL == "" {
			return nil, badPayload(nil)
		}
		if c.fetch == nil {
			return nil, &jobs.Error{Code: "online_off", Text: "Online lookups are turned off.", Permanent: true}
		}
		var err error
		if data, _, err = c.fetch(ctx, p.URL); err != nil {
			return nil, err
		}
	default:
		return nil, badPayload(nil)
	}

	guard := t.Fence
	if p.IfNoCover {
		guard = ifNoCoverGuard(t.Fence, p.BookID)
	}
	id, err := c.Adopt(ctx, p.BookID, p.Source, data, guard)
	if errors.Is(err, errCoverPresent) {
		return map[string]bool{"kept": true}, nil
	}
	if err != nil {
		err = adoptError(err)
		var je *jobs.Error
		if staged != "" && errors.As(err, &je) && je.Permanent {
			os.Remove(staged)
		}
		return nil, err
	}
	if staged != "" {
		os.Remove(staged)
	}
	return map[string]string{"cover_id": id}, nil
}

// adoptError makes the refusals of a picture permanent job errors and leaves the rest to jobError.
func adoptError(err error) error {
	var me *media.Error
	if errors.As(err, &me) {
		switch me.Code {
		case CodeNotPicture, CodeTooLarge, media.CodeImageTooLarge:
			return &jobs.Error{Code: me.Code, Text: me.Text, Permanent: true, Err: err}
		}
	}
	if errors.Is(err, library.ErrNotFound) {
		return &jobs.Error{Code: "book_gone", Text: "This book is no longer in the library.", Permanent: true, Err: err}
	}
	return jobError(err)
}
