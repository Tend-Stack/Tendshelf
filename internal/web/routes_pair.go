package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Adding a phone by QR code (decision 0028, plan docs/plans/invite-and-qr.md). The signed-in device
// shows a QR holding /pair/{token}; the phone's camera opens it, the page hands the address to the
// app, the phone claims the token and shows a 4-digit number, the person types it on the signed-in
// device, and the phone then taps Connect. The token alone claims and nothing more. /pair/* is public
// (gate.go), but a GET never changes anything, every state change is a CSRF-protected POST, and a
// dead token, a used one and one that never existed all give the same page. Tokens, claims, codes and
// numbers are never logged and never in a Location header (the one 303 the app takes carries the
// ordinary device code, as /app/connect does).

// pairCookie holds the phone's claim secret. HttpOnly, SameSite=Strict, only for /pair, 3 minutes.
const pairCookie = "ts_pair"

func (s *server) registerPair(mux *http.ServeMux) {
	mux.HandleFunc("GET /me/devices/add", s.getAddPhone)
	mux.HandleFunc("POST /me/devices/add", s.postAddPhone)
	mux.HandleFunc("GET /me/devices/add/{id}", s.getAddPhoneStatus)
	mux.HandleFunc("POST /me/devices/add/{id}/approve", s.postAddPhoneApprove)
	mux.HandleFunc("POST /me/devices/add/{id}/cancel", s.postAddPhoneCancel)
	mux.HandleFunc("GET /pair/wait", s.getPairWait)
	mux.HandleFunc("POST /pair/finish", s.postPairFinish)
	mux.HandleFunc("GET /pair/{token}", s.getPair)
	mux.HandleFunc("POST /pair/{token}", s.postPair)
}

// pairURL is the address a QR for a pairing holds.
func (s *server) pairURL(r *http.Request, tok string) string {
	scheme := "http://"
	if s.proxies.Secure(r) {
		scheme = "https://"
	}
	return scheme + r.Host + "/pair/" + tok
}

// ---- the signed-in device ----

// addPhoneView starts a view of the owner's page.
func (s *server) addPhoneView(r *http.Request, state string) pages.AddPhoneView {
	return pages.AddPhoneView{
		CSRF: s.csrf.TokenFrom(r.Context()), State: state, Title: addPhoneTitle, Lead: addPhoneIntro, Button: addPhoneShow,
		BackLabel: addPhoneBack, Insecure: !s.proxies.Secure(r), InsecureWords: connectInsecure,
	}
}

func (s *server) addPhonePage(w http.ResponseWriter, r *http.Request, status int, v pages.AddPhoneView) {
	inviteHeaders(w)
	s.prefsPage(w, r, status, addPhoneTitle, "profile", "devices", false, pages.AddPhone(v), nil, append(append([]string{}, pages.MeStyles...), pages.PairStyles...)...)
}

// addPhoneWords is the page that only says something: no form.
func (s *server) addPhoneWords(w http.ResponseWriter, r *http.Request, status int, words string) {
	v := s.addPhoneView(r, pages.AddPhoneWords)
	v.Lead = words
	v.Insecure = false
	s.addPhonePage(w, r, status, v)
}

// ownSession returns the session of the request. A person signed in by a proxy in front of Tendshelf
// has none, and gets the 403 page.
func (s *server) ownSession(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	sess, ok := auth.SessionFrom(r.Context())
	if !ok {
		s.addPhoneWords(w, r, http.StatusForbidden, addPhoneNoSession)
		return auth.Session{}, false
	}
	return sess, true
}

func (s *server) getAddPhone(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.ownSession(w, r); !ok {
		return
	}
	if !reachableHost(r.Host) {
		s.addPhoneWords(w, r, http.StatusOK, addPhoneLoopback)
		return
	}
	s.addPhonePage(w, r, http.StatusOK, s.addPhoneView(r, pages.AddPhoneIntro))
}

func (s *server) postAddPhone(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.ownSession(w, r)
	if !ok {
		return
	}
	if !reachableHost(r.Host) {
		s.errorPage(w, r, http.StatusNotFound)
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	user := currentUser(r)
	if ok, wait := s.pairLim.Allow(user.ID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		s.addPhoneWords(w, r, http.StatusTooManyRequests, addPhoneBusy)
		return
	}
	s.pairLim.Fail(user.ID)
	p, tok, err := s.auth.StartPairing(r.Context(), sess, s.proxies.ClientIP(r))
	if err != nil {
		s.serverError(w, r, "start pairing", err)
		return
	}
	link := s.pairURL(r, tok)
	v := s.addPhoneView(r, pages.AddPhoneWaiting)
	v.ID, v.Lead, v.Link, v.LinkLabel, v.CopyLabel = p.ID, addPhoneScan, link, addPhoneLinkLabel, addPhoneCopy
	v.Waiting, v.CheckLabel, v.Poll = addPhoneWaiting, addPhoneCheck, true
	if q, ok := qrView(link, addPhoneQRLabel); ok {
		v.QR = &q
	}
	s.addPhonePage(w, r, http.StatusOK, v) // a page, never a redirect: the token stays out of Location
}

// pairEndedOwner is the one 404 page for a pairing the person cannot see: expired, gone, spent, or
// another session's, even the same person's.
func (s *server) pairEndedOwner(w http.ResponseWriter, r *http.Request, words string) {
	v := s.addPhoneView(r, pages.AddPhoneEnded)
	v.Lead, v.Button, v.Insecure = words, addPhoneNewCode, false
	s.addPhonePage(w, r, http.StatusNotFound, v)
}

func (s *server) getAddPhoneStatus(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.ownSession(w, r)
	if !ok {
		return
	}
	p, err := s.auth.OwnerPairing(r.Context(), r.PathValue("id"), sess)
	if errors.Is(err, auth.ErrPairEnded) {
		s.pairEndedOwner(w, r, addPhoneEnded)
		return
	}
	if err != nil {
		s.serverError(w, r, "pairing", err)
		return
	}
	polling := r.Header.Get("HX-Request") == "true"
	v := s.addPhoneView(r, pages.AddPhoneWaiting)
	v.ID = p.ID
	switch p.State {
	case auth.PairWaiting:
		if polling {
			inviteHeaders(w)
			w.WriteHeader(http.StatusNoContent) // nothing changed: the QR stays, a screen reader is not told again
			return
		}
		// The token is not stored, so the code cannot be drawn again; the person can show a new one.
		v.Lead, v.Waiting, v.CheckLabel = addPhoneScan, addPhoneWaiting, addPhoneCheck
	case auth.PairClaimed:
		v.State = pages.AddPhoneClaimed
		v.Lead = ""
		v.Claimed = fmt.Sprintf(addPhoneClaimed, p.PhoneName, p.PhoneIP)
		v.NumberLabel, v.Approve, v.NotMine = addPhoneNumber, addPhoneApprove, addPhoneNotMine
	default:
		v.State, v.Claimed, v.Lead = pages.AddPhoneApproved, addPhoneApproved, ""
	}
	s.addPhonePage(w, r, http.StatusOK, v)
}

func (s *server) postAddPhoneApprove(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.ownSession(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	err := s.auth.ApprovePairing(r.Context(), r.PathValue("id"), sess, r.PostForm.Get("match"), s.proxies.ClientIP(r))
	switch {
	case err == nil:
		v := s.addPhoneView(r, pages.AddPhoneApproved)
		v.Insecure, v.Claimed, v.Lead = false, addPhoneApproved, ""
		s.addPhonePage(w, r, http.StatusOK, v)
	case errors.Is(err, auth.ErrPairMismatch):
		v := s.addPhoneView(r, pages.AddPhoneEnded)
		v.Lead, v.Button, v.Insecure = addPhoneWrong, addPhoneNewCode, false
		s.addPhonePage(w, r, http.StatusUnprocessableEntity, v)
	case errors.Is(err, auth.ErrPairEnded):
		s.pairEndedOwner(w, r, addPhoneEnded)
	default:
		s.serverError(w, r, "approve pairing", err)
	}
}

func (s *server) postAddPhoneCancel(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.ownSession(w, r)
	if !ok {
		return
	}
	if err := s.auth.CancelPairing(r.Context(), r.PathValue("id"), sess, s.proxies.ClientIP(r)); err != nil {
		s.serverError(w, r, "cancel pairing", err)
		return
	}
	s.answerPage(w, r, "/me/devices")
}

// ---- the phone ----

func (s *server) pairPage(w http.ResponseWriter, r *http.Request, status int, title string, body templ.Component) {
	inviteHeaders(w)
	s.frame(w, r, status, title, body, pages.PairStyles...)
}

// pairEnded is the one answer to every pairing that does not work, whatever the reason.
func (s *server) pairEnded(w http.ResponseWriter, r *http.Request) {
	s.pairPage(w, r, http.StatusNotFound, pairEndedTitle, pages.PairEnded(pages.PairEndedView{
		Title: pairEndedTitle, Body: pairEnded, Link: "/login", LinkText: pairPassword,
	}))
}

// pairGuard checks the failure limit of the caller's address before anything is read. It answers
// the 429 page itself.
func (s *server) pairGuard(w http.ResponseWriter, r *http.Request) bool {
	err := s.auth.PairAllowed(s.proxies.ClientIP(r))
	var rl *auth.RateLimitedError
	if errors.As(err, &rl) {
		body := tooMany(w, rl)
		s.pairPage(w, r, http.StatusTooManyRequests, "Please wait a little", pages.PairEnded(pages.PairEndedView{Title: "Please wait a little", Body: body}))
		return false
	}
	return true
}

func (s *server) notInApp(w http.ResponseWriter, r *http.Request) {
	s.pairPage(w, r, http.StatusForbidden, pairHandoverTitle, pages.PairEnded(pages.PairEndedView{Title: pairHandoverTitle, Body: pairHandoverOthers}))
}

func (s *server) getPair(w http.ResponseWriter, r *http.Request) {
	if !s.pairGuard(w, r) {
		return
	}
	tok := r.PathValue("token")
	if err := s.auth.PairingPending(r.Context(), tok); err != nil {
		if !errors.Is(err, auth.ErrPairEnded) {
			s.serverError(w, r, "pairing", err)
			return
		}
		s.auth.PairFailed(r.Context(), s.proxies.ClientIP(r), "unknown")
		s.pairEnded(w, r)
		return
	}
	if shellFrom(r) != "android" {
		v := pages.PairHandoverView{Title: pairHandoverTitle, Lead: pairHandoverOthers}
		if androidBrowser(r) {
			if r.URL.Query().Get("noapp") == "1" {
				v.Note = pairNoApp
			} else {
				v.AppLink, v.AppLabel = appOpenURL(s.pairURL(r, tok)), pairHandoverOpen
				v.Lead = ""
			}
		}
		s.pairPage(w, r, http.StatusOK, pairHandoverTitle, pages.PairHandover(v))
		return
	}
	challenge := r.URL.Query().Get("challenge")
	if _, ok := auth.ParseChallenge(challenge); !ok {
		s.errorPage(w, r, http.StatusNotFound)
		return
	}
	server := s.serverName(r.Context())
	s.pairPage(w, r, http.StatusOK, pairAskButton, pages.PairConfirm(pages.PairConfirmView{
		CSRF: s.csrf.TokenFrom(r.Context()), Token: tok, Challenge: challenge, Name: auth.CleanPhoneName(r.URL.Query().Get("name")),
		Title: fmt.Sprintf(pairAsk, server), Body: pairAskBody, Address: fmt.Sprintf(pairAddress, r.Host), Button: pairAskButton,
		Insecure: !s.proxies.Secure(r), InsecureWords: pairInsecure,
	}))
}

func (s *server) postPair(w http.ResponseWriter, r *http.Request) {
	if shellFrom(r) != "android" {
		s.notInApp(w, r)
		return
	}
	if !s.pairGuard(w, r) || !s.parseForm(w, r) {
		return
	}
	ip := s.proxies.ClientIP(r)
	challenge, ok := auth.ParseChallenge(r.PostForm.Get("challenge"))
	if !ok {
		s.auth.PairFailed(r.Context(), ip, "challenge")
		s.pairEnded(w, r)
		return
	}
	if sess, ok := auth.SessionFrom(r.Context()); ok && sess.DeviceID != "" {
		s.pairBound(w, r)
		return
	}
	claim, err := s.auth.ClaimPairing(r.Context(), r.PathValue("token"), challenge, r.PostForm.Get("name"), ip)
	if errors.Is(err, auth.ErrPairEnded) {
		s.auth.PairFailed(r.Context(), ip, "unknown")
		s.pairEnded(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "claim pairing", err)
		return
	}
	http.SetCookie(w, s.pairCookieFor(r, claim, int(auth.PairTTL.Seconds())))
	inviteHeaders(w)
	http.Redirect(w, r, "/pair/wait", http.StatusSeeOther)
}

func (s *server) pairCookieFor(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: pairCookie, Value: value, Path: "/pair", HttpOnly: true, Secure: s.proxies.Secure(r),
		SameSite: http.SameSiteStrictMode, MaxAge: maxAge}
}

func (s *server) pairBound(w http.ResponseWriter, r *http.Request) {
	s.pairPage(w, r, http.StatusConflict, inviteBoundNotice, pages.PairEnded(pages.PairEndedView{Title: inviteBoundNotice}))
}

func (s *server) getPairWait(w http.ResponseWriter, r *http.Request) {
	if !s.pairGuard(w, r) {
		return
	}
	c, err := r.Cookie(pairCookie)
	if err != nil {
		s.pairEnded(w, r)
		return
	}
	p, err := s.auth.ClaimedPairing(r.Context(), c.Value)
	if errors.Is(err, auth.ErrPairEnded) {
		s.auth.PairFailed(r.Context(), s.proxies.ClientIP(r), "claim")
		s.pairEnded(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "pairing", err)
		return
	}
	v := pages.PairWaitView{CSRF: s.csrf.TokenFrom(r.Context()), Title: pairWaitTitle, CheckLabel: addPhoneCheck}
	switch p.State {
	case auth.PairClaimed:
		if r.Header.Get("HX-Request") == "true" {
			inviteHeaders(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		v.State, v.Lead, v.Number, v.Poll = auth.PairClaimed, pairNumberLead, p.Number, true
	default:
		v.State, v.Lead, v.Button = auth.PairApproved, pairApprovedIs, fmt.Sprintf(pairConnectAs, p.PersonName)
	}
	s.pairPage(w, r, http.StatusOK, pairWaitTitle, pages.PairWait(v))
}

func (s *server) postPairFinish(w http.ResponseWriter, r *http.Request) {
	if shellFrom(r) != "android" {
		s.notInApp(w, r)
		return
	}
	if !s.pairGuard(w, r) || !s.parseForm(w, r) {
		return
	}
	ip := s.proxies.ClientIP(r)
	c, err := r.Cookie(pairCookie)
	if err != nil {
		s.auth.PairFailed(r.Context(), ip, "no_cookie")
		s.pairEnded(w, r)
		return
	}
	var prev *auth.Session
	if sess, ok := auth.SessionFrom(r.Context()); ok {
		prev = &sess
	}
	login, code, err := s.auth.FinishPairing(r.Context(), c.Value, r.UserAgent(), ip, prev)
	switch {
	case err == nil:
	case errors.Is(err, auth.ErrPairBound):
		s.pairBound(w, r)
		return
	case errors.Is(err, auth.ErrPairEnded):
		s.auth.PairFailed(r.Context(), ip, "finish")
		http.SetCookie(w, s.pairCookieFor(r, "", -1))
		s.pairEnded(w, r)
		return
	default:
		slog.Error("finish pairing", "err", err) // never the claim or the code
		s.errorPage(w, r, http.StatusInternalServerError)
		return
	}
	s.startSession(w, r, login.Token)
	http.SetCookie(w, s.pairCookieFor(r, "", -1))
	inviteHeaders(w)
	http.Redirect(w, r, appDonePath+"?code="+url.QueryEscape(code), http.StatusSeeOther)
}
