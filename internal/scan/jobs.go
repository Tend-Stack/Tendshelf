package scan

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

// Job kinds registered by Register.
const (
	KindScan  = "scan.library"
	KindProbe = "media.probe"
)

// ScanPayload is the payload of a scan.library job.
type ScanPayload struct {
	LibraryID string `json:"library_id"`
}

// ProbePayload is the payload of a media.probe job.
type ProbePayload struct {
	AudioFileID string `json:"audio_file_id"`
}

// Register adds the scan and probe job kinds to the runner. Their handlers write only inside a
// transaction that begins with the job's fence check.
func (s *Scanner) Register(r *jobs.Runner) error {
	if err := r.Register(jobs.Kind{Name: KindScan, Concurrency: 1, MaxAttempts: 1, Handler: s.handleScan}); err != nil {
		return err
	}
	return r.Register(jobs.Kind{Name: KindProbe, Concurrency: 2, MaxAttempts: 3, Handler: s.handleProbe})
}

func badPayload(err error) error {
	return &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
}

func (s *Scanner) handleScan(ctx context.Context, t *jobs.Task) (any, error) {
	var p ScanPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.LibraryID == "" {
		return nil, badPayload(err)
	}
	res, err := s.ScanLibrary(ctx, p.LibraryID, t.Fence)
	if errors.Is(err, library.ErrNotFound) {
		return nil, &jobs.Error{Code: "not_found", Text: "This library no longer exists.", Permanent: true, Err: err}
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Scanner) handleProbe(ctx context.Context, t *jobs.Task) (any, error) {
	var p ProbePayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.AudioFileID == "" {
		return nil, badPayload(err)
	}
	out, err := s.ProbeFile(ctx, p.AudioFileID, t.Fence)
	if err != nil {
		return nil, err
	}
	return map[string]string{"outcome": string(out)}, nil
}
