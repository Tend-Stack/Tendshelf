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
)

// CoverPayload is the payload of a media.cover job.
type CoverPayload struct {
	BookID string `json:"book_id"`
}

// CompatPayload is the payload of a media.compat job.
type CompatPayload struct {
	AudioFileID string `json:"audio_file_id"`
}

// Register adds the cover and compatibility-copy job kinds to the runner. Both run one at a time:
// ffmpeg work is heavy, and a cover or copy is never urgent. Each handler's writes begin with the
// job's fence check.
func Register(r *jobs.Runner, covers *Covers, compat *Compat) error {
	if err := r.Register(jobs.Kind{Name: KindCover, Concurrency: 1, MaxAttempts: 2, Handler: covers.handle}); err != nil {
		return err
	}
	return r.Register(jobs.Kind{Name: KindCompat, Concurrency: 1, MaxAttempts: 2, Handler: compat.handle})
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
