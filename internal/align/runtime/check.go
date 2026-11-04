package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// KindCheck is the job kind of a runtime check: hashing 1.7 GB and loading torch outlast a request.
const KindCheck = "align.check"

// CheckPayload is the payload of an align.check job.
type CheckPayload struct {
	Language string `json:"language"`
}

// CheckResult is the result of an align.check job.
type CheckResult struct {
	Code string `json:"code"`
}

// Check verifies lang's model against its pinned manifest, runs the worker's --describe and records
// the outcome in comps. A failed check is not an error: it is the returned component's CheckError
// (also stored). The error is for things Check cannot decide (the database, cancellation).
//
// Mechanical deviation from the plan's Check(ctx, cfg, lang): the component store is a parameter,
// because "upsert the row in every case" needs it.
func Check(ctx context.Context, cfg Config, comps *Components, lang string) (Component, error) {
	cfg = cfg.withDefaults()
	man, ok := cfg.Manifests[lang]
	if !ok {
		return Component{}, &Error{CodeUnsupportedLang}
	}
	filesJSON, _ := json.Marshal(man.Files)
	c := Component{
		Kind: KindEmissionsModel, Name: man.Repository, Language: lang, Revision: man.Revision,
		ModelSHA256: ModelSHA(man.Files), FilesJSON: string(filesJSON), IdentityJSON: "{}",
		WorkerSHA256: cfg.workerSHA(), LicenceSPDX: man.LicenceSPDX, LicenceURL: man.LicenceURL,
		SourceURL: man.SourceURL,
	}
	if prev, found, err := comps.Get(ctx, lang); err != nil {
		return Component{}, err
	} else if found {
		c.ID, c.Disabled = prev.ID, prev.Disabled
	}
	identity, code, err := runChecks(ctx, cfg, man, lang, &c)
	if err != nil {
		return Component{}, err
	}
	c.CheckError = code
	if code == "" {
		c.IdentityJSON = identity
	}
	return comps.Upsert(ctx, c)
}

// runChecks returns the describe output and a failure code ("" when everything passed).
func runChecks(ctx context.Context, cfg Config, man Manifest, lang string, c *Component) (string, string, error) {
	python, err := locate(cfg)
	if err != nil {
		var ce *Error
		if errors.As(err, &ce) {
			return "", ce.Code, nil
		}
		return "", "", err
	}
	c.PythonPath = python
	dir := filepath.Join(cfg.ModelsDir, lang)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", CodeModelsMissing, nil
	}
	for _, f := range man.Files {
		code, err := checkFile(ctx, dir, f)
		if err != nil {
			return "", "", err
		}
		if code != "" {
			return "", code, nil
		}
	}
	script, err := writeScript(cfg)
	if err != nil {
		return "", "", err
	}
	dctx, cancel := context.WithTimeout(ctx, cfg.DescribeTimeout)
	defer cancel()
	out, _, capped, runErr := runOnce(dctx, cfg, python, workerArgs(cfg, script, "--describe", lang))
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if runErr != nil || capped {
		return "", CodeDescribeFailed, nil
	}
	var id Identity
	if json.Unmarshal(out, &id) != nil {
		return "", CodeDescribeFailed, nil
	}
	if id.Protocol != Protocol || id.Model != man.Repository || id.Revision != man.Revision ||
		id.Language != man.Language || id.ModelSHA256 != c.ModelSHA256 {
		return "", CodeIdentityMismatch, nil
	}
	return string(out), "", nil
}

// checkFile compares one model file with its manifest entry. Only regular files count (no links).
func checkFile(ctx context.Context, dir string, f ManifestFile) (string, error) {
	path := filepath.Join(dir, f.Name)
	st, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return CodeModelsMissing, nil
	case err != nil:
		return "", err
	case !st.Mode().IsRegular() || st.Size() != f.Size:
		return CodeModelMismatch, nil
	}
	h := sha256.New()
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	if _, err := io.Copy(h, &ctxReader{ctx: ctx, r: src}); err != nil {
		return "", err
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return CodeModelMismatch, nil
	}
	return "", nil
}

// ctxReader stops a long hash when the job is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Register adds the align.check job kind to r.
func Register(r *jobs.Runner, cfg Config, comps *Components) error {
	return r.Register(jobs.Kind{
		Name: KindCheck, Concurrency: 1, MaxAttempts: 1,
		Handler: func(ctx context.Context, t *jobs.Task) (any, error) {
			var p CheckPayload
			if err := json.Unmarshal(t.Payload, &p); err != nil || p.Language == "" {
				return nil, &jobs.Error{Code: "bad_payload", Text: "The check request is malformed.", Permanent: true}
			}
			c, err := Check(ctx, cfg, comps, p.Language)
			if err != nil {
				var ce *Error
				if errors.As(err, &ce) {
					return CheckResult{Code: ce.Code}, nil
				}
				return nil, err
			}
			return CheckResult{Code: c.CheckError}, nil
		},
	})
}
