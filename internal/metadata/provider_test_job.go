package metadata

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// KindProviderTest is the job that runs a test search against one added lookup site (decision 0046).
const KindProviderTest = "metadata.provider_test"

// ProviderTestPayload is the payload of a metadata.provider_test job. It names the site by id only.
type ProviderTestPayload struct {
	ProviderID string `json:"provider_id"`
}

// ProviderTestResult is the result of a test: whether the site answered, how many books, and the
// online.Error code when it did not. It carries no address and no header.
type ProviderTestResult struct {
	OK    bool   `json:"ok"`
	Found int    `json:"found"`
	Code  string `json:"code"`
}

// activeFinder is the part of jobs.Runner that finds a job already waiting or running.
type activeFinder interface {
	ActiveFor(ctx context.Context, kind, field, value string) (jobs.Job, bool, error)
}

// StartProviderTest queues a test of one site and returns the job id. A test of the same site that
// is already queued or running is returned instead of starting another.
func (s *Service) StartProviderTest(ctx context.Context, id, userID string) (string, error) {
	name, err := s.providerName(ctx, id)
	if err != nil {
		return "", err
	}
	if af, ok := s.jobs.(activeFinder); ok {
		if j, found, err := af.ActiveFor(ctx, KindProviderTest, "provider_id", id); err != nil {
			return "", err
		} else if found {
			return j.ID, nil
		}
	}
	job, err := s.jobs.Enqueue(ctx, KindProviderTest, ProviderTestPayload{ProviderID: id}, jobs.EnqueueOptions{CreatedBy: userID})
	if err != nil {
		return "", err
	}
	err = db.Audit(ctx, s.repo.Store().Writer(), db.AuditEntry{At: s.now().UnixMilli(), UserID: userID, Action: "custom_provider.test",
		Target: "server", Detail: map[string]any{"name": name, "job": job}})
	return job, err
}

func (s *Service) handleProviderTest(ctx context.Context, t *jobs.Task) (any, error) {
	var p ProviderTestPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.ProviderID == "" {
		return nil, &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
	}
	if s.online == nil {
		return ProviderTestResult{Code: "refused"}, nil
	}
	found, err := s.online.TestCustom(ctx, p.ProviderID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		code := "server"
		var oe *online.Error
		if errors.As(err, &oe) {
			code = oe.Code
		}
		return ProviderTestResult{Code: code}, nil
	}
	return ProviderTestResult{OK: true, Found: found}, nil
}
