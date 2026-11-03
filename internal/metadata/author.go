package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

// KindAuthor is the job that looks one author up on Audnexus.
const KindAuthor = "metadata.online_author"

const (
	authorThumbBox = 320
	peopleDirMode  = 0o750
	peopleFileMode = 0o640
)

// AuthorPayload is the payload of a metadata.online_author job.
type AuthorPayload struct {
	PersonID string `json:"person_id"`
}

// PeopleDir is where author photos and their thumbnails live under the data directory.
func PeopleDir(dataDir string) string { return filepath.Join(dataDir, "people") }

// StartAuthorLookup queues an author lookup. It returns ErrOnlineOff, or ErrNotFound for an
// unknown person. The caller checks that the person may manage libraries.
func (s *Service) StartAuthorLookup(ctx context.Context, personID, userID string) (string, error) {
	if s.online == nil {
		return "", ErrOnlineOff
	}
	if on, err := OnlineEnabled(ctx, s.repo.Store().Reader()); err != nil {
		return "", err
	} else if !on {
		return "", ErrOnlineOff
	}
	if _, err := s.repo.PersonByID(ctx, personID); err != nil {
		return "", err
	}
	return s.jobs.Enqueue(ctx, KindAuthor, AuthorPayload{PersonID: personID}, jobs.EnqueueOptions{CreatedBy: userID})
}

func (s *Service) handleAuthor(ctx context.Context, t *jobs.Task) (any, error) {
	var p AuthorPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.PersonID == "" {
		return nil, &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
	}
	if s.online == nil {
		return nil, offError()
	}
	if on, err := OnlineEnabled(ctx, s.repo.Store().Reader()); err != nil {
		return nil, err
	} else if !on {
		return nil, offError()
	}
	person, err := s.repo.PersonByID(ctx, p.PersonID)
	if errors.Is(err, library.ErrNotFound) {
		return nil, &jobs.Error{Code: "gone", Text: "This person is no longer in the library.", Permanent: true, Err: err}
	}
	if err != nil {
		return nil, err
	}
	a, err := s.online.Author(ctx, person.ASIN, person.Name)
	if err != nil {
		return nil, err
	}
	if a.ASIN == "" && a.Description == "" && a.ImageURL == "" {
		return map[string]bool{"found": false}, nil
	}
	imageID := person.ImageID
	if a.ImageURL != "" {
		if id, err := s.storeAuthorImage(ctx, a.ImageURL); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			s.log.Warn("author photo", "person", person.ID, "err", err) // the text still counts
		} else {
			imageID = id
		}
	}
	desc, asin := person.Description, person.ASIN
	if d := cleanText(a.Description, 0); d != "" {
		desc = d
	}
	if a.ASIN != "" {
		asin = a.ASIN
	}
	err = s.repo.UpdateGuarded(ctx, t.Fence, func(tx *library.Tx) error {
		return tx.UpdatePersonDetails(person.ID, desc, asin, imageID)
	})
	if err != nil {
		return nil, err
	}
	if person.ImageID != "" && person.ImageID != imageID {
		s.removePersonImage(ctx, person.ImageID)
	}
	return map[string]any{"found": true, "image": imageID != ""}, nil
}

// storeAuthorImage fetches the photo, keeps it as <data>/people/<sha>.<ext>, makes the 320 px
// thumbnail <sha>-320.jpg and returns the image id (the file name of the photo).
func (s *Service) storeAuthorImage(ctx context.Context, url string) (string, error) {
	data, kind, err := s.online.FetchCover(ctx, url)
	if err != nil {
		return "", err
	}
	ext := map[string]string{"jpeg": "jpg", "png": "png", "webp": "webp"}[kind]
	if ext == "" {
		return "", errors.New("unsupported picture kind " + kind)
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	dir := PeopleDir(s.dataDir)
	if err := os.MkdirAll(dir, peopleDirMode); err != nil {
		return "", err
	}
	id := sha + "." + ext
	photo := filepath.Join(dir, id)
	if _, err := os.Stat(photo); err != nil {
		if err := writeFileAtomic(photo, data); err != nil {
			return "", err
		}
	}
	thumb := filepath.Join(dir, sha+"-320.jpg")
	if _, err := os.Stat(thumb); err != nil {
		tmp := filepath.Join(dir, ".tmp-"+sha[:16]+"-320.jpg")
		if err := s.tools.Thumbnail(ctx, photo, tmp, authorThumbBox); err != nil {
			os.Remove(tmp)
			return "", err
		}
		if err := os.Chmod(tmp, peopleFileMode); err != nil {
			os.Remove(tmp)
			return "", err
		}
		if err := os.Rename(tmp, thumb); err != nil {
			os.Remove(tmp)
			return "", err
		}
	}
	return id, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := filepath.Join(filepath.Dir(path), ".tmp-"+filepath.Base(path))
	if err := os.WriteFile(tmp, data, peopleFileMode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// removePersonImage deletes an old photo and its thumbnail when no person uses them any more.
func (s *Service) removePersonImage(ctx context.Context, imageID string) {
	if used, err := s.repo.PersonImageInUse(ctx, imageID); err != nil || used {
		return
	}
	dir := PeopleDir(s.dataDir)
	sha := strings.TrimSuffix(imageID, filepath.Ext(imageID))
	for _, f := range []string{imageID, sha + "-320.jpg"} {
		if filepath.Base(f) == f { // an id from the database never leaves the folder
			os.Remove(filepath.Join(dir, f))
		}
	}
}
