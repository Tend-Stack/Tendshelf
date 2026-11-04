package runtime

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// KindEmissionsModel is the only runtime_components.kind.
const KindEmissionsModel = "emissions-model"

// Component is one row of runtime_components: one checked model per language.
type Component struct {
	ID, Kind, Name, Language, Revision string
	ModelSHA256                        string
	FilesJSON, IdentityJSON            string
	PythonPath, WorkerSHA256           string
	LicenceSPDX, LicenceURL, SourceURL string
	CheckError                         string // empty when the check passed
	CheckedAt                          time.Time
	Disabled                           bool
}

// Components stores runtime_components rows.
type Components struct {
	st  *db.Store
	now func() time.Time
}

// NewComponents returns the store over st.
func NewComponents(st *db.Store) *Components {
	return &Components{st: st, now: time.Now}
}

const componentCols = `id, kind, name, language, revision, model_sha256, files_json, identity_json, python_path,
worker_sha256, licence_spdx, licence_url, source_url, check_error, checked_at, disabled`

func scanComponent(row interface{ Scan(...any) error }) (Component, error) {
	var c Component
	var at int64
	var dis int
	err := row.Scan(&c.ID, &c.Kind, &c.Name, &c.Language, &c.Revision, &c.ModelSHA256, &c.FilesJSON, &c.IdentityJSON,
		&c.PythonPath, &c.WorkerSHA256, &c.LicenceSPDX, &c.LicenceURL, &c.SourceURL, &c.CheckError, &at, &dis)
	c.CheckedAt = time.UnixMilli(at).UTC()
	c.Disabled = dis != 0
	return c, err
}

// Get returns the component for a language, if one was ever checked.
func (s *Components) Get(ctx context.Context, lang string) (Component, bool, error) {
	c, err := scanComponent(s.st.Reader().QueryRowContext(ctx,
		`SELECT `+componentCols+` FROM runtime_components WHERE kind = ? AND language = ?`, KindEmissionsModel, lang))
	if errors.Is(err, sql.ErrNoRows) {
		return Component{}, false, nil
	}
	return c, err == nil, err
}

// Upsert writes the checked state of a (kind, language). The id and the disabled flag of an existing
// row are kept; Disabled in c is used only for a new row.
func (s *Components) Upsert(ctx context.Context, c Component) (Component, error) {
	if c.Kind == "" {
		c.Kind = KindEmissionsModel
	}
	if c.ID == "" {
		c.ID = db.NewID("rc")
	}
	if c.CheckedAt.IsZero() {
		c.CheckedAt = s.now()
	}
	if c.IdentityJSON == "" {
		c.IdentityJSON = "{}"
	}
	dis := 0
	if c.Disabled {
		dis = 1
	}
	_, err := s.st.Writer().ExecContext(ctx, `INSERT INTO runtime_components(`+componentCols+`)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(kind, language) DO UPDATE SET name=excluded.name, revision=excluded.revision,
 model_sha256=excluded.model_sha256, files_json=excluded.files_json, identity_json=excluded.identity_json,
 python_path=excluded.python_path, worker_sha256=excluded.worker_sha256, licence_spdx=excluded.licence_spdx,
 licence_url=excluded.licence_url, source_url=excluded.source_url, check_error=excluded.check_error,
 checked_at=excluded.checked_at`,
		c.ID, c.Kind, c.Name, c.Language, c.Revision, c.ModelSHA256, c.FilesJSON, c.IdentityJSON, c.PythonPath,
		c.WorkerSHA256, c.LicenceSPDX, c.LicenceURL, c.SourceURL, c.CheckError, c.CheckedAt.UnixMilli(), dis)
	if err != nil {
		return Component{}, err
	}
	got, ok, err := s.getWriter(ctx, c.Language)
	if err == nil && !ok {
		err = sql.ErrNoRows
	}
	return got, err
}

// getWriter reads through the writer connection so a row just written is always visible.
func (s *Components) getWriter(ctx context.Context, lang string) (Component, bool, error) {
	c, err := scanComponent(s.st.Writer().QueryRowContext(ctx,
		`SELECT `+componentCols+` FROM runtime_components WHERE kind = ? AND language = ?`, KindEmissionsModel, lang))
	if errors.Is(err, sql.ErrNoRows) {
		return Component{}, false, nil
	}
	return c, err == nil, err
}

// Usable returns the component when it may be used now: checked clean, not disabled, and checked
// against this interpreter and this worker script.
func (s *Components) Usable(ctx context.Context, lang, pythonPath, workerSHA string) (Component, bool, error) {
	c, ok, err := s.Get(ctx, lang)
	if err != nil || !ok {
		return Component{}, false, err
	}
	if c.CheckError != "" || c.Disabled || c.PythonPath != pythonPath || c.WorkerSHA256 != workerSHA {
		return c, false, nil
	}
	return c, true, nil
}

// SetDisabled switches a checked component off or on (the owner's switch on /admin/runtimes).
func (s *Components) SetDisabled(ctx context.Context, lang string, disabled bool) error {
	v := 0
	if disabled {
		v = 1
	}
	_, err := s.st.Writer().ExecContext(ctx, `UPDATE runtime_components SET disabled = ? WHERE kind = ? AND language = ?`,
		v, KindEmissionsModel, lang)
	return err
}
