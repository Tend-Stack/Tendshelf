package mediajobs

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// Job kinds registered by Register.
const (
	KindCover  = "media.cover"
	KindCompat = "media.compat"
	KindPeaks  = "media.peaks"
)

// CoverPayload is the payload of a media.cover job.
type CoverPayload struct {
	BookID string `json:"book_id"`
}

// CompatPayload is the payload of a media.compat job.
type CompatPayload struct {
	AudioFileID string `json:"audio_file_id"`
}

// PeaksPayload is the payload of a media.peaks job.
type PeaksPayload struct {
	AudioFileID string `json:"audio_file_id"`
}

// Register adds the cover, compatibility-copy, waveform and write-details job kinds (the last only when writes is not nil) to the runner. Each runs one at
// a time: ffmpeg work is heavy, and a cover, copy or waveform is never urgent. Each handler's
// writes begin with the job's fence check.
func Register(r *jobs.Runner, covers *Covers, compat *Compat, peaks *Peaks, writes *Writes) error {
	if err := r.Register(jobs.Kind{Name: KindCover, Concurrency: 1, MaxAttempts: 2, Handler: covers.handle}); err != nil {
		return err
	}
	if err := r.Register(jobs.Kind{Name: KindCompat, Concurrency: 1, MaxAttempts: 2, Handler: compat.handle}); err != nil {
		return err
	}
	if err := r.Register(jobs.Kind{Name: KindCoverAdopt, Concurrency: 1, MaxAttempts: 2, Handler: covers.adoptHandler}); err != nil {
		return err
	}
	if err := r.Register(jobs.Kind{Name: KindPeaks, Concurrency: 1, MaxAttempts: 2, Handler: peaks.handle}); err != nil {
		return err
	}
	if writes == nil {
		return nil
	}
	// A write that was running when the process died is held, not retried: Writes.Recover settles
	// its ledger rows and then cancels it. One attempt only, because a retry must never replay a
	// half-done swap.
	return r.Register(jobs.Kind{Name: KindWriteDetails, Concurrency: 1, MaxAttempts: 1, HoldOnRecovery: true, Handler: writes.handle})
}

func badPayload(err error) error {
	return &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
}

func (c *Covers) handle(ctx context.Context, t *jobs.Task) (any, error) {
	var p CoverPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.BookID == "" {
		return nil, badPayload(err)
	}
	out, err := c.Run(ctx, p.BookID, t.Fence)
	if err != nil {
		return nil, jobError(err)
	}
	return map[string]string{"outcome": out}, nil
}

func (c *Compat) handle(ctx context.Context, t *jobs.Task) (any, error) {
	var p CompatPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.AudioFileID == "" {
		return nil, badPayload(err)
	}
	out, err := c.Run(ctx, p.AudioFileID, t.Fence)
	if err != nil {
		return nil, jobError(err)
	}
	return map[string]string{"outcome": out}, nil
}

func (p *Peaks) handle(ctx context.Context, t *jobs.Task) (any, error) {
	var pl PeaksPayload
	if err := json.Unmarshal(t.Payload, &pl); err != nil || pl.AudioFileID == "" {
		return nil, badPayload(err)
	}
	if !p.Enabled() {
		return nil, &jobs.Error{Code: "disabled", Text: "Waveforms are turned off.", Permanent: true}
	}
	out, err := p.Run(ctx, pl.AudioFileID, t.Fence)
	if err != nil {
		return nil, jobError(err)
	}
	return map[string]string{"outcome": out}, nil
}

// jobError turns a media failure into the job error that carries its plain-words text. A missing
// ffmpeg, a copy-protected file and a file that cannot be converted at all fail at once; a timeout
// or a failed conversion is tried again. Other errors (a cancelled context, a lost lease, the
// database) pass through for the runner to judge.
func jobError(err error) error {
	var me *media.Error
	if !errors.As(err, &me) {
		return err
	}
	permanent := false
	switch me.Code {
	case media.CodeFFmpegMissing, media.CodeDRM, media.CodeCompatNotNeeded, media.CodeNotAudio,
		media.CodeFileMissing, media.CodeFileUnreadable, media.CodeProbeFailed:
		permanent = true
	}
	return &jobs.Error{Code: me.Code, Text: me.Text, Permanent: permanent, Err: err}
}
