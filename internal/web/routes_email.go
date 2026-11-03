package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Email settings: the optional relay that lets Tendshelf send invitations itself. Owner only (the
// server's settings); everyone else gets a 404. The password is written but never read back into a
// page, a log or a job.

func (s *server) registerEmail(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/email", s.getEmail)
	mux.HandleFunc("POST /admin/email", s.postEmail)
	mux.HandleFunc("POST /admin/email/test", s.postEmailTest)
	mux.HandleFunc("GET /hx/admin/email/test/{id}", s.getEmailTest)
	mux.HandleFunc("POST /admin/email/off", s.postEmailOff)
}

func (s *server) emailGate(w http.ResponseWriter, r *http.Request) bool {
	return s.allow(w, r, share.ManageServer, share.Target{Kind: share.TargetServer})
}

var emailTLSChoices = []pages.Option{
	{Value: share.TLSStartTLS, Label: "STARTTLS (port 587)"},
	{Value: share.TLSImplicit, Label: "TLS (port 465)"},
	{Value: share.TLSNone, Label: "None (this computer only)"},
}

// emailRelayReady reports whether "Send it for me" is on: settings saved and a test that worked.
func (s *server) emailRelayReady(r *http.Request) bool {
	cfg, err := share.LoadMailConfig(r.Context(), s.lib.Store().Reader())
	if err != nil || !cfg.Ready() {
		return false
	}
	ok, err := share.MailTested(r.Context(), s.lib.Store().Reader())
	return err == nil && ok
}

// emailView builds the page. A POST that failed shows what was typed; a GET shows what is saved, with
// a provider preset (?preset=&region=) filling the server, port and security.
func (s *server) emailView(r *http.Request) (pages.AdminEmailView, error) {
	ctx := r.Context()
	cfg, err := share.LoadMailConfig(ctx, s.lib.Store().Reader())
	if err != nil {
		return pages.AdminEmailView{}, err
	}
	tested, err := share.MailTested(ctx, s.lib.Store().Reader())
	if err != nil {
		return pages.AdminEmailView{}, err
	}
	q := r.URL.Query()
	v := pages.AdminEmailView{
		CSRF: s.csrf.TokenFrom(ctx), Host: cfg.Host, Port: strconv.Itoa(cfg.Port), TLS: cfg.TLS, Username: cfg.Username,
		FromName: cfg.FromName, FromAddress: cfg.FromAddress, PasswordSaved: cfg.Password != "", Ready: cfg.Ready(), Tested: tested && cfg.Ready(),
		Intro: emailIntro, SESHelp: emailSESHelp, SavedNotice: emailSaved, DropNotice: emailDropped, TestHint: emailTestHint,
		OffLabel: emailOff, OffHint: emailOffHint, TLSChoices: emailTLSChoices, Regions: share.SESRegions, Region: "eu-west-1",
		Saved: q.Get("saved") == "1", Dropped: q.Get("dropped") == "1", TestTo: currentUser(r).Email, Errors: map[string]string{},
	}
	for _, p := range share.MailPresets {
		v.Providers = append(v.Providers, pages.Option{Value: p.ID, Label: p.Name})
	}
	if reg := q.Get("region"); reg != "" {
		v.Region = reg
	}
	if p, ok := share.MailPresetByID(q.Get("preset")); ok && r.Method == http.MethodGet {
		v.Provider, v.ProviderHint = p.ID, p.Hint
		if host := p.Filled(v.Region); host != "" || p.ID == "own" {
			if p.ID != "own" {
				v.Host = host
			}
			v.Port, v.TLS = strconv.Itoa(p.Port), p.TLS
			if p.Username != "" {
				v.Username = p.Username
			}
		}
	}
	switch {
	case !cfg.Ready():
		v.StatusLine = emailStatusOff
	case !v.Tested:
		v.StatusLine = emailStatusNoTest
	default:
		v.StatusLine = emailStatusOn
	}
	return v, nil
}

func (s *server) renderEmail(w http.ResponseWriter, r *http.Request, status int, v pages.AdminEmailView) {
	s.pageStyled(w, r, status, emailTitle, "admin-email", pages.AdminEmail(v), nil, pages.EmailStyles...)
}

func (s *server) getEmail(w http.ResponseWriter, r *http.Request) {
	if !s.emailGate(w, r) {
		return
	}
	v, err := s.emailView(r)
	if err != nil {
		s.serverError(w, r, "email page", err)
		return
	}
	if id := r.URL.Query().Get("test"); id != "" {
		if tv, ok := s.emailTestView(r, id, r.URL.Query().Get("to")); ok {
			v.Test = &tv
		}
	}
	s.renderEmail(w, r, http.StatusOK, v)
}

func emailFieldMessages(fe users.FieldErrors) map[string]string {
	out := map[string]string{}
	for field, code := range fe {
		switch {
		case field == "host":
			out[field] = emailErrHost
		case field == "port":
			out[field] = emailErrPort
		case code == "insecure":
			out[field] = emailErrInsecure
		case field == "from_address":
			out[field] = emailErrAddress
		default:
			out[field] = emailErrOther
		}
	}
	return out
}

func (s *server) postEmail(w http.ResponseWriter, r *http.Request) {
	if !s.emailGate(w, r) || !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	f := r.PostForm
	port, _ := strconv.Atoi(strings.TrimSpace(f.Get("port")))
	in := share.MailInput{Host: f.Get("host"), Port: port, TLS: f.Get("tls"), Username: f.Get("username"),
		FromName: f.Get("from_name"), FromAddress: f.Get("from_address")}
	var dropped bool
	err := s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		_, dropped, err = share.SaveMailConfig(ctx, tx, tx, currentUser(r), in, f.Get("password"), s.now())
		return err
	})
	var fe users.FieldErrors
	if errors.As(err, &fe) {
		v, verr := s.emailView(r)
		if verr != nil {
			s.serverError(w, r, "email page", verr)
			return
		}
		v.Host, v.Port, v.TLS, v.Username = strings.TrimSpace(in.Host), strings.TrimSpace(f.Get("port")), in.TLS, in.Username
		v.FromName, v.FromAddress, v.Errors = in.FromName, in.FromAddress, emailFieldMessages(fe)
		s.renderEmail(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	if err != nil {
		s.serverError(w, r, "save email settings", err)
		return
	}
	loc := "/admin/email?saved=1"
	if dropped {
		loc += "&dropped=1"
	}
	s.answerPage(w, r, loc)
}

func (s *server) postEmailTest(w http.ResponseWriter, r *http.Request) {
	if !s.emailGate(w, r) || !s.parseForm(w, r) {
		return
	}
	user := currentUser(r)
	to := strings.TrimSpace(r.PostForm.Get("to"))
	if to == "" {
		to = user.Email
	}
	v, err := s.emailView(r)
	if err != nil {
		s.serverError(w, r, "email page", err)
		return
	}
	v.TestTo = to
	refuse := func(status int, field, msg string) {
		v.Errors[field] = msg
		s.renderEmail(w, r, status, v)
	}
	switch {
	case !v.Ready:
		refuse(http.StatusUnprocessableEntity, "to", emailErrNotReady)
		return
	case !share.ValidMailAddress(to):
		refuse(http.StatusUnprocessableEntity, "to", emailErrAddress)
		return
	}
	if ok, wait := s.mailLim.test.Allow(user.ID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		refuse(http.StatusTooManyRequests, "to", emailErrBusy)
		return
	}
	s.mailLim.test.Fail(user.ID)
	id, err := s.mail.QueueTest(r.Context(), user, to)
	if err != nil {
		s.serverError(w, r, "queue test email", err)
		return
	}
	v.Test = &pages.EmailTestView{JobID: id, To: to, Line: fmt.Sprintf(emailTestSending, to), Pending: true}
	s.renderEmail(w, r, http.StatusOK, v)
}

// emailTestView reads a test job for the signed-in person. ok is false for a job that is not a test of
// theirs, so the fragment route and the page both answer as if it did not exist.
func (s *server) emailTestView(r *http.Request, id, to string) (pages.EmailTestView, bool) {
	j, err := s.jobs.Get(r.Context(), id)
	if err != nil || j.Kind != share.KindSMTPTest || j.CreatedBy != currentUser(r).ID {
		return pages.EmailTestView{}, false
	}
	cfg, _ := share.LoadMailConfig(r.Context(), s.lib.Store().Reader())
	v := pages.EmailTestView{JobID: id, To: to}
	switch j.State {
	case jobs.StateQueued, jobs.StateRunning:
		v.Pending, v.Line = true, fmt.Sprintf(emailTestSending, to)
	case jobs.StateDone:
		v.Line = fmt.Sprintf(emailTestDone, to)
	default:
		v.Failed = true
		switch j.ErrorCode {
		case "mail_auth":
			v.Line = emailTestAuth
		case "mail_sender":
			v.Line = fmt.Sprintf(emailTestSender, cfg.FromAddress)
		case "mail_recipient":
			v.Line = fmt.Sprintf(emailTestRecipient, to)
		case "mail_no_tls":
			v.Line = emailTestNoTLS
		case "mail_tls":
			v.Line = emailTestTLS
		case "mail_unreachable", "mail_busy":
			v.Line = fmt.Sprintf(emailTestReach, cfg.Host, cfg.Port)
		default:
			v.Line = emailTestOther
		}
	}
	return v, true
}

func (s *server) getEmailTest(w http.ResponseWriter, r *http.Request) {
	if !s.emailGate(w, r) {
		return
	}
	v, ok := s.emailTestView(r, r.PathValue("id"), r.URL.Query().Get("to"))
	if !ok {
		s.notFound(w, r)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.EmailTestPanel(v))
}

func (s *server) postEmailOff(w http.ResponseWriter, r *http.Request) {
	if !s.emailGate(w, r) || !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	err := s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		return share.ClearMailConfig(ctx, tx, currentUser(r), s.now())
	})
	if err != nil {
		s.serverError(w, r, "turn off email", err)
		return
	}
	s.answerPage(w, r, "/admin/email")
}
