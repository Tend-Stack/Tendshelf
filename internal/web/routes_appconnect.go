package web

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The Connect page of the phone app (decision 0026). The app opens it inside its own window after the
// person signed in on the server's page. One tap makes a one-time code bound to the app's PKCE
// challenge; the answer is a same-origin 303 that the app's window takes and cancels, so the code
// reaches no script, no history entry and no other app. These routes sit behind the sign-in gate and
// take a browser session only: a device token is refused before it gets here (bearer.go). The window
// mark ts_shell=android is not a credential; without it the page shows words and makes no code.

// appDonePath is where the 303 sends the app's window.
const appDonePath = "/app/connect/done"

func (s *server) registerAppConnect(mux *http.ServeMux) {
	mux.HandleFunc("GET /app/connect", s.getAppConnect)
	mux.HandleFunc("POST /app/connect", s.postAppConnect)
	mux.HandleFunc("GET /app/connect/done", s.getAppConnectDone)
}

// appConnectPage renders one state of the page. Every answer is no-store (frame sets it).
func (s *server) appConnectPage(w http.ResponseWriter, r *http.Request, status int, v pages.AppConnectView) {
	v.CSRF = s.csrf.TokenFrom(r.Context())
	if v.Title == "" {
		v.Title = connectTitle
	}
	s.frame(w, r, status, v.Title, pages.AppConnect(v))
}

func (s *server) appConnectNotInApp(w http.ResponseWriter, r *http.Request) {
	s.appConnectPage(w, r, http.StatusForbidden, pages.AppConnectView{State: pages.AppConnectNotInApp, Body: connectNotInApp})
}

func (s *server) getAppConnect(w http.ResponseWriter, r *http.Request) {
	if shellFrom(r) != "android" {
		s.appConnectNotInApp(w, r)
		return
	}
	challenge := r.URL.Query().Get("challenge")
	if _, ok := auth.ParseChallenge(challenge); !ok {
		s.errorPage(w, r, http.StatusNotFound)
		return
	}
	s.appConnectPage(w, r, http.StatusOK, pages.AppConnectView{
		State: pages.AppConnectAsk, Challenge: challenge, Insecure: !s.proxies.Secure(r),
		Body: connectBody, Button: connectButton, Warning: connectInsecure,
	})
}

func (s *server) postAppConnect(w http.ResponseWriter, r *http.Request) {
	if shellFrom(r) != "android" {
		s.appConnectNotInApp(w, r)
		return
	}
	sess, ok := auth.SessionFrom(r.Context())
	if !ok {
		s.redirect(w, r, loginURL(r))
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	challenge, ok := auth.ParseChallenge(r.PostForm.Get("challenge"))
	if !ok {
		s.errorPage(w, r, http.StatusNotFound)
		return
	}
	code, err := s.auth.IssueDeviceCode(r.Context(), sess, challenge, s.proxies.ClientIP(r))
	if err != nil {
		slog.Error("issue device code", "err", err) // never the code
		s.errorPage(w, r, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
	http.Redirect(w, r, appDonePath+"?code="+url.QueryEscape(code), http.StatusSeeOther)
}

// getAppConnectDone is where the 303 lands if nothing took it. It never reads its query: the code in
// it is for the app's window alone and nothing on this page may touch it.
func (s *server) getAppConnectDone(w http.ResponseWriter, r *http.Request) {
	if shellFrom(r) != "android" {
		s.appConnectNotInApp(w, r)
		return
	}
	w.Header().Set("Referrer-Policy", "same-origin")
	s.appConnectPage(w, r, http.StatusOK, pages.AppConnectView{State: pages.AppConnectDone, Body: connectDone})
}
