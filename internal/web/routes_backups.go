package web

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/Tend-Stack/tendshelf/internal/backups"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Backups: the owner's page for scheduled copies of the database. Owner only (the server's settings);
// everyone else, including admins, gets a 404 on every route and on the job status.

func (s *server) registerBackups(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/backups", s.getBackups)
	mux.HandleFunc("POST /admin/backups/schedule", s.postBackupsSchedule)
	mux.HandleFunc("POST /admin/backups/now", s.postBackupsNow)
}

func (s *server) backupsGate(w http.ResponseWriter, r *http.Request) bool {
	return s.allow(w, r, share.ManageServer, share.Target{Kind: share.TargetServer})
}

var backupsChoices = []pages.Option{
	{Value: backups.Off, Label: backupsOff},
	{Value: backups.Daily, Label: backupsDaily},
	{Value: backups.Weekly, Label: backupsWeekly},
}

// backupsLastLine is "Last backup {when} · {size}" or "No backup yet", for the page and for Profile.
func (s *server) backupsLastLine() (string, error) {
	c, ok, err := s.backups.Latest()
	if err != nil {
		return "", err
	}
	if !ok {
		return backupsNone, nil
	}
	return fmt.Sprintf(backupsLast, agoText(s.now(), c.At), sizeText(c.Bytes)), nil
}

func (s *server) backupsView(r *http.Request) (pages.AdminBackupsView, error) {
	ctx := r.Context()
	copies, err := db.ListCopies(s.dataDir)
	if err != nil {
		return pages.AdminBackupsView{}, err
	}
	last, err := s.backupsLastLine()
	if err != nil {
		return pages.AdminBackupsView{}, err
	}
	schedule := s.backups.Schedule(ctx)
	v := pages.AdminBackupsView{
		CSRF: s.csrf.TokenFrom(ctx), Schedule: schedule, Choices: backupsChoices,
		Title: backupsTitle, Intro: backupsIntro, Heading: backupsHeading, Save: backupsSave, SavedNotice: backupsSaved,
		LastLine: last, DirLine: fmt.Sprintf(backupsDir, filepath.Join(s.dataDir, "backups")), NowLabel: backupsNow,
		Keeps: backupsKeeps, CopiesH: backupsCopies, RestoreTitle: backupsRestoreTitle,
		RestoreSteps: []string{backupsRestore1, backupsRestore2, backupsRestore3},
		Saved:        r.URL.Query().Get("saved") == "1",
	}
	for _, c := range copies {
		v.Copies = append(v.Copies, pages.BackupCopyRow{When: c.At.Local().Format("2 Jan, 15:04"), Size: sizeText(c.Bytes), Name: c.Name})
	}
	if next, ok := s.backups.Next(ctx); ok {
		when := "soon"
		if next.After(s.now()) {
			when = next.Local().Format("2 Jan, 15:04")
		}
		v.NextLine = fmt.Sprintf(backupsNext, when)
	}
	// A failure shows until a newer copy exists.
	if j, ok, err := s.jobs.LatestFor(ctx, backups.Kind, "target", "database"); err == nil && ok && j.State == jobs.StateFailed &&
		(len(copies) == 0 || j.UpdatedAt.After(copies[0].At)) {
		v.Failure = backupsFailed
	}
	return v, nil
}

func (s *server) renderBackups(w http.ResponseWriter, r *http.Request, status int, v pages.AdminBackupsView) {
	s.prefsPage(w, r, status, backupsTitle, "admin-backups", "backups", false, pages.AdminBackups(v), nil, pages.EmailStyles...)
}

func (s *server) getBackups(w http.ResponseWriter, r *http.Request) {
	if !s.backupsGate(w, r) {
		return
	}
	v, err := s.backupsView(r)
	if err != nil {
		s.serverError(w, r, "backups page", err)
		return
	}
	if id := r.URL.Query().Get("job"); id != "" {
		if j, err := s.jobs.Get(r.Context(), id); err == nil && j.Kind == backups.Kind {
			sv := s.backupStatus(j)
			v.Status = &sv
		}
	}
	s.renderBackups(w, r, http.StatusOK, v)
}

func (s *server) postBackupsSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.backupsGate(w, r) || !s.parseForm(w, r) {
		return
	}
	err := s.backups.SetSchedule(r.Context(), r.PostForm.Get("schedule"))
	if errors.Is(err, backups.ErrBadSchedule) {
		if r.Header.Get("HX-Request") == "true" {
			s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(backupsErrValue))
			return
		}
		v, verr := s.backupsView(r)
		if verr != nil {
			s.serverError(w, r, "backups page", verr)
			return
		}
		v.Error = backupsErrValue
		s.renderBackups(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	if err != nil {
		s.serverError(w, r, "save backup schedule", err)
		return
	}
	s.answerPage(w, r, "/admin/backups?saved=1")
}

func (s *server) postBackupsNow(w http.ResponseWriter, r *http.Request) {
	if !s.backupsGate(w, r) || !s.parseForm(w, r) {
		return
	}
	id, err := s.backups.Start(r.Context(), currentUser(r).ID, "manual")
	if err != nil {
		s.serverError(w, r, "queue backup", err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, http.StatusOK, pages.BackupStatusPanel(pages.BackupStatusView{JobID: id, Line: backupsRunning, Pending: true}))
		return
	}
	s.redirect(w, r, "/admin/backups?job="+id)
}

// backupStatus is the status line of one backup job.
func (s *server) backupStatus(j jobs.Job) pages.BackupStatusView {
	v := pages.BackupStatusView{JobID: j.ID}
	switch j.State {
	case jobs.StateQueued, jobs.StateRunning, jobs.StateHeld:
		v.Pending, v.Line = true, backupsRunning
	case jobs.StateDone:
		v.Line = fmt.Sprintf(backupsDone, agoText(s.now(), j.UpdatedAt))
	default:
		v.Failed, v.Line = true, backupsFailed
	}
	return v
}

// getBackupJob is GET /hx/jobs/{id} for a backup job: the owner's alone, else "no such job".
func (s *server) getBackupJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	if !s.backupsGate(w, r) {
		return
	}
	s.fragment(w, r, http.StatusOK, pages.BackupStatusPanel(s.backupStatus(j)))
}
