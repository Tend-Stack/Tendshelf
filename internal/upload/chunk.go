package upload

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
)

// blockSize is how much of a chunk is read and written at a time, so a chunk never sits in memory.
const blockSize = 256 << 10

func noSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// WriteChunk stores one chunk of one file at offset, which must be the server's current offset.
// sum, when not empty, is the hex SHA-256 of the chunk. The offset moves forward only after the
// bytes are written and synced, by a conditional update. It returns the new offset.
//
// A failure of any kind (an interrupted body, a bad checksum, a full disk) truncates the part file
// back to offset, so the bytes after the server's offset never count.
func (s *Service) WriteChunk(ctx context.Context, userID, uploadID, fileID string, offset int64, sum string, body io.Reader) (int64, error) {
	u, err := s.loadOwn(ctx, userID, uploadID)
	if err != nil {
		return 0, err
	}
	l := s.lock(uploadID)
	l.Lock()
	defer l.Unlock()
	u, err = s.loadOwn(ctx, userID, uploadID) // state may have changed while waiting
	if err != nil {
		return 0, err
	}
	switch u.State {
	case StateReceiving:
	case StateExpired, StateCancelled:
		return 0, fail(410, "expired", "That upload has ended. Start it again.")
	default:
		return 0, fail(409, "not_receiving", "That upload is not waiting for files.")
	}
	files, err := s.loadFiles(ctx, uploadID)
	if err != nil {
		return 0, err
	}
	var f *fileRow
	for i := range files {
		if files[i].ID == fileID {
			f = &files[i]
		}
	}
	if f == nil {
		return 0, errNotFound
	}
	if offset != f.BytesReceived {
		return 0, &Error{Status: 409, Code: "wrong_offset", Message: "The server is at a different place in this file.", Offset: f.BytesReceived}
	}
	want := f.Size - offset
	if want > s.cfg.ChunkSize {
		want = s.cfg.ChunkSize
	}
	part := s.partPath(uploadID, fileID)
	if part == "" {
		return 0, errNotFound
	}
	fh, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, s.diskError(err)
	}
	defer fh.Close()
	// Drop anything after the server's offset: a chunk that was cut off earlier left bytes there.
	if err := fh.Truncate(offset); err != nil {
		return 0, s.diskError(err)
	}
	back := func() {
		_ = fh.Truncate(offset)
		if offset == 0 {
			_ = os.Remove(part) // nothing was ever accepted: leave no empty file behind
		}
	}

	hasher := sha256.New()
	buf := make([]byte, blockSize)
	var got int64
	limited := io.LimitReader(body, s.cfg.ChunkSize+1)
	for {
		n, rerr := limited.Read(buf)
		if n > 0 {
			got += int64(n)
			if got > s.cfg.ChunkSize || got > want {
				back()
				if got > s.cfg.ChunkSize {
					return 0, fail(413, "too_large", "That piece is bigger than the server accepts.")
				}
				return 0, fail(422, "short_chunk", "That piece is not the size the server expected.")
			}
			hasher.Write(buf[:n])
			if werr := s.writeAt(fh, buf[:n], offset+got-int64(n)); werr != nil {
				back()
				return 0, s.diskError(werr)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			back()
			return 0, fail(400, "interrupted", "The upload was interrupted. Try that piece again.")
		}
	}
	if got != want {
		back()
		return 0, fail(422, "short_chunk", "That piece is not the size the server expected.")
	}
	if sum != "" && !strings.EqualFold(sum, hex.EncodeToString(hasher.Sum(nil))) {
		back()
		return 0, fail(422, "checksum", "That piece arrived damaged. Try it again.")
	}
	if err := fh.Sync(); err != nil {
		back()
		return 0, s.diskError(err)
	}
	newOff := offset + got
	err = s.write(ctx, nil, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE upload_files SET bytes_received = ? WHERE id = ? AND upload_id = ? AND bytes_received = ?`,
			newOff, fileID, uploadID, offset)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return &Error{Status: 409, Code: "wrong_offset", Message: "The server is at a different place in this file.", Offset: f.BytesReceived}
		}
		_, err = tx.ExecContext(ctx, `UPDATE uploads SET bytes_received = bytes_received + ?, updated_at = ? WHERE id = ? AND state = 'receiving'`,
			got, s.nowMs(), uploadID)
		return err
	})
	if err != nil {
		back()
		return 0, err
	}
	return newOff, nil
}

// diskError maps a file system error to the refusal the client sees.
func (s *Service) diskError(err error) error {
	if noSpace(err) {
		return fail(507, "no_space", "The server's data folder is full.")
	}
	s.log.Error("upload: staging write failed", "err", err)
	return fail(500, "disk", "Tendshelf could not save that piece. Details are in the server log.")
}
