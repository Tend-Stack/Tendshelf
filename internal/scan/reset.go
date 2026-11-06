package scan

import (
	"context"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// ErrChaptersBusy means some files of the book are still waiting for their probe, so the file
// chapters cannot be rebuilt yet.
var ErrChaptersBusy = errors.New("scan: files are still being read")

// ResetChapters drops a book's edited chapters and rebuilds the list from its files. When any live
// file has not been read yet, nothing changes and ErrChaptersBusy comes back.
func ResetChapters(ctx context.Context, repo *library.Repo, bookID string) error {
	return repo.Update(ctx, func(tx *library.Tx) error {
		if err := tx.ReplaceChapters(bookID, nil); err != nil {
			return err
		}
		settled, err := finalize(tx, bookID, 0)
		if err != nil {
			return err
		}
		if !settled {
			return ErrChaptersBusy // rolls the transaction back, the old list stays
		}
		return nil
	})
}
