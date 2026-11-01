package scan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// ProbeOutcome says what ProbeFile did with one file.
type ProbeOutcome string

// Outcomes that are not a media error code. A media error code (drm, not_audio, file_unreadable,
// file_missing, probe_timeout, ...) is also an outcome.
const (
	OutcomeProbed  ProbeOutcome = "probed"  // read fine and recorded
	OutcomeGone    ProbeOutcome = "gone"    // the row no longer exists
	OutcomeMissing ProbeOutcome = "missing" // the file is marked missing; nothing to read
	OutcomeSettled ProbeOutcome = "settled" // already has an answer
	OutcomeStale   ProbeOutcome = "stale"   // the file changed under the probe; the next scan handles it
)

// changedOnDisk reports whether the file's size or modification time differs from the row. A file
// that cannot be statted counts as unchanged: the probe then reports why.
func changedOnDisk(row *library.AudioFile) bool {
	fi, err := os.Stat(row.Path)
	return err == nil && (fi.Size() != row.Size || fi.ModTime().UnixNano() != row.MtimeNs)
}

// ProbeFile reads one audio file with ffprobe and records the answer, and settles the book when this
// was its last unread file. ffprobe runs outside any transaction; the write is one guarded
// transaction that first re-reads the row, so a file that changed meanwhile is discarded (stale).
// Errors that a retry could fix are retryable *jobs.Error values; a file that will never read is a
// permanent one, after its error code is recorded.
func (s *Scanner) ProbeFile(ctx context.Context, fileID string, guard func(context.Context, *sql.Tx) error) (ProbeOutcome, error) {
	row, err := s.repo.AudioFile(ctx, fileID)
	switch {
	case errors.Is(err, library.ErrNotFound):
		return OutcomeGone, nil
	case err != nil:
		return "", err
	case row.MissingSince != 0:
		return OutcomeMissing, nil
	case settled(&row):
		return OutcomeSettled, nil
	}
	if changedOnDisk(&row) {
		return OutcomeStale, nil
	}

	p, perr := s.tools.Probe(ctx, row.Path)
	if s.afterProbe != nil {
		s.afterProbe()
	}
	var me *media.Error
	if perr != nil && !errors.As(perr, &me) {
		return "", perr // the parent context ended, or something unexpected
	}
	if changedOnDisk(&row) {
		return OutcomeStale, nil
	}

	var apply func(f *library.AudioFile, now int64)
	outcome := OutcomeProbed
	switch {
	case perr == nil:
		apply = func(f *library.AudioFile, _ int64) { recordProbe(f, p) }
	case me.Code == media.CodeDRM:
		outcome = ProbeOutcome(me.Code)
		apply = func(f *library.AudioFile, _ int64) {
			if p.V != 0 {
				f.ProbeJSON, _ = json.Marshal(p)
			}
			f.Encrypted, f.ProbeError = true, media.CodeDRM
		}
	case me.Code == media.CodeNotAudio, me.Code == media.CodeFileUnreadable:
		outcome = ProbeOutcome(me.Code)
		apply = func(f *library.AudioFile, _ int64) { f.ProbeError = me.Code }
	case me.Code == media.CodeFileMissing:
		outcome = ProbeOutcome(me.Code)
		apply = func(f *library.AudioFile, now int64) { f.MissingSince = now }
	}

	if apply != nil {
		now := s.now().UnixMilli()
		wrote := false
		err = s.repo.UpdateGuarded(ctx, guard, func(tx *library.Tx) error {
			cur, err := tx.AudioFile(fileID)
			switch {
			case errors.Is(err, library.ErrNotFound):
				outcome = OutcomeGone
				return nil
			case err != nil:
				return err
			case cur.QuickHash != row.QuickHash || cur.Size != row.Size || cur.MtimeNs != row.MtimeNs || cur.MissingSince != 0:
				outcome = OutcomeStale
				return nil
			case settled(&cur):
				outcome = OutcomeSettled
				return nil
			}
			apply(&cur, now)
			if err := tx.PutAudioFile(&cur); err != nil {
				return err
			}
			wrote = true
			files, err := tx.AudioFiles(cur.BookID)
			if err != nil {
				return err
			}
			for i := range files {
				if files[i].MissingSince == 0 && !settled(&files[i]) {
					return nil // more files wait; the last one to settle finalises the book
				}
			}
			return finalize(tx, cur.BookID, now)
		})
		if err != nil {
			return "", err
		}
		if !wrote {
			return outcome, nil
		}
	} else {
		s.log.Warn("probe failed", "file", fileID, "path", row.Path, "code", me.Code, "detail", me.Detail)
	}

	switch {
	case perr == nil, me.Code == media.CodeDRM, me.Code == media.CodeFileMissing:
		return outcome, nil
	}
	return ProbeOutcome(me.Code), mediaError(me)
}

// mediaError turns a media failure into the job error that carries its plain-words text.
// Timeouts and other transient failures can be retried; a file that cannot be read cannot.
func mediaError(me *media.Error) error {
	permanent := false
	switch me.Code {
	case media.CodeNotAudio, media.CodeFileUnreadable, media.CodeFFprobeMissing:
		permanent = true
	}
	return &jobs.Error{Code: me.Code, Text: me.Text, Permanent: permanent}
}

// recordProbe copies a successful probe onto the row.
func recordProbe(f *library.AudioFile, p media.Probe) {
	b, _ := json.Marshal(p)
	f.ProbeJSON, f.ProbeError, f.Encrypted = b, "", false
	f.Container = p.Container
	if len(p.Audio) > 0 {
		a := p.Audio[0]
		f.Codec, f.Channels, f.SampleRate = a.Codec, a.Channels, a.SampleRate
		f.Bitrate = a.BitRate
		f.DurationMs = a.DurationMs
	}
	if f.Bitrate == 0 {
		f.Bitrate = p.BitRate
	}
	if f.DurationMs == 0 {
		f.DurationMs = p.DurationMs
	}
}
