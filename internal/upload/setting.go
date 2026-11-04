package upload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// checkUploadID is the pseudo upload that owns the ledger rows of write checks.
const checkUploadID = "writecheck"

// ErrNotWritable means Tendshelf could not create a file in the folder.
var ErrNotWritable = errors.New("upload: the folder is not writable")

// Writable proves the library folder can take uploads: it creates a file named
// .tendshelf-write-check-<random> with O_EXCL and removes it again. The name is recorded in the
// ledger before it is created, like every path this package makes, and it is the only path this
// function ever deletes: one it has just created itself through the handle it holds.
func (s *Service) Writable(ctx context.Context, folderID string) error {
	folder, err := s.repo.Folder(ctx, folderID)
	if err != nil {
		return err
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	p := filepath.Join(folder.Path, ".tendshelf-write-check-"+hex.EncodeToString(rnd[:]))
	if !within(folder.Path, p) {
		return ErrNotWritable
	}
	fi, err := os.Stat(folder.Path)
	if err != nil || !fi.IsDir() {
		return ErrNotWritable
	}
	lr, err := openLroot(folder.Path)
	if err != nil {
		return errors.Join(ErrNotWritable, err)
	}
	defer lr.Close()
	id, err := s.plan(ctx, nil, checkUploadID, folder.ID, p, kindFile, pathPlanned)
	if err != nil {
		return err
	}
	f, err := lr.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = s.dropPath(ctx, nil, id)
		return errors.Join(ErrNotWritable, err)
	}
	cerr := f.Close()
	rerr := lr.Remove(p)
	if rerr != nil {
		s.log.Warn("upload: cannot remove the write-check file", "path", p, "err", rerr)
		_ = s.markPath(ctx, nil, id, pathKept)
		return errors.Join(ErrNotWritable, rerr)
	}
	_ = s.markPath(ctx, nil, id, pathRemoved)
	if cerr != nil {
		return errors.Join(ErrNotWritable, cerr)
	}
	return nil
}
