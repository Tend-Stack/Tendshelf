package web

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/auth/passkey"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Passkeys (decision 0041, plan docs/plans/passkeys.md). The ceremonies themselves are in
// internal/auth (PasskeySignIn, PasskeyRegister and friends); this file is the HTTP side: two
// JSON POSTs a signed-out browser may reach, the Passkeys page and its JSON and form routes, and the
// admin's removal. No challenge, signature or credential id is ever logged.

const (
	passkeyLoginOptionsPath = "/login/passkey/options"
	passkeyLoginPath        = "/login/passkey"
	passkeyBodyLimit        = 64 << 10
	passkeyTimeoutMS        = 120000
	passkeyServerError      = "Something went wrong on your server."
)

func (s *server) registerPasskeys(mux *http.ServeMux) {
	mux.HandleFunc("POST "+passkeyLoginOptionsPath, s.postPasskeyLoginOptions)
	mux.HandleFunc("POST "+passkeyLoginPath, s.postPasskeyLogin)
	mux.HandleFunc("GET /me/passkeys", s.getMePasskeys)
	mux.HandleFunc("POST /me/passkeys/options", s.postMePasskeyOptions)
	mux.HandleFunc("POST /me/passkeys", s.postMePasskeyAdd)
	mux.HandleFunc("POST /me/passkeys/{pid}/remove", s.postMePasskeyRemove)
	mux.HandleFunc("POST /admin/users/{id}/passkeys/remove", s.postPersonPasskeysRemove)
}

// isPasskeyPublicPath reports whether p is one of the two sign-in ceremony routes a signed-out
// browser may reach (exact paths).
func isPasskeyPublicPath(p string) bool {
	return p == passkeyLoginOptionsPath || p == passkeyLoginPath
}

// Why passkeys are not offered for a request.
const (
	passkeyOK       = ""
	passkeyShell    = "shell"
	passkeyIPHost   = "ip"
	passkeyInsecure = "http"
)

// passkeyHere decides whether passkeys are offered for r and, when they are, returns the
// relying-party id (the host name) and the origin. Otherwise reason says why (decision 0041,
// points 4, 5 and 13).
func (s *server) passkeyHere(r *http.Request) (rpID, origin, reason string) {
	if shellFrom(r) != "" {
		return "", "", passkeyShell
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if _, err := netip.ParseAddr(host); err == nil || !dnsName(host) {
		return "", "", passkeyIPHost
	}
	if !s.proxies.Secure(r) && host != "localhost" && !strings.HasSuffix(host, ".localhost") {
		return "", "", passkeyInsecure
	}
	return host, s.requestOrigin(r), passkeyOK
}

// dnsName reports whether h is a plausible host name: dot-separated labels of letters, digits and hyphens.
func dnsName(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// passkeyReasonText is the plain-words line for a reason passkeys are not offered.
func passkeyReasonText(reason string) string {
	switch reason {
	case passkeyShell:
		return passkeyInShell
	case passkeyIPHost:
		return passkeyNeedName
	case passkeyInsecure:
		return passkeyNeedHTTPS
	}
	return ""
}

// passkeyError answers a JSON error in plain words.
func passkeyError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// passkeyBody checks the content type and reads a small JSON body into v. It has answered when it
// returns false.
func passkeyBody(w http.ResponseWriter, r *http.Request, v any) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		passkeyError(w, http.StatusUnsupportedMediaType, passkeyBadRequest)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, passkeyBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		passkeyError(w, http.StatusBadRequest, passkeyBadRequest)
		return false
	}
	return true
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// unb64 decodes base64url without padding (padding is tolerated).
func unb64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// ---- Signing in ----

func (s *server) postPasskeyLoginOptions(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !passkeyBody(w, r, &body) {
		return
	}
	rp, origin, reason := s.passkeyHere(r)
	if reason != passkeyOK {
		passkeyError(w, http.StatusNotFound, passkeyUnavailable)
		return
	}
	ch, err := s.auth.PasskeySignInBegin(r.Context(), rp, origin, s.proxies.ClientIP(r))
	var rl *auth.RateLimitedError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"publicKey": map[string]any{
			"challenge": b64(ch), "rpId": rp, "timeout": passkeyTimeoutMS,
			"userVerification": "required", "allowCredentials": []any{},
		}})
	case errors.As(err, &rl):
		passkeyError(w, http.StatusTooManyRequests, tooMany(w, rl))
	case errors.Is(err, auth.ErrPasskeyBusy):
		passkeyError(w, http.StatusServiceUnavailable, passkeyBusy)
	default:
		slog.Error("passkey sign-in options", "err", err)
		passkeyError(w, http.StatusInternalServerError, passkeyServerError)
	}
}

func (s *server) postPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID                string `json:"id"`
		Type              string `json:"type"`
		ClientDataJSON    string `json:"clientDataJSON"`
		AuthenticatorData string `json:"authenticatorData"`
		Signature         string `json:"signature"`
		UserHandle        string `json:"userHandle"`
		Next              string `json:"next"`
	}
	if !passkeyBody(w, r, &body) {
		return
	}
	rp, origin, reason := s.passkeyHere(r)
	if reason != passkeyOK {
		passkeyError(w, http.StatusNotFound, passkeyUnavailable)
		return
	}
	var in auth.PasskeyAssertion
	var errs [5]error
	in.RawID, errs[0] = unb64(body.ID)
	in.ClientDataJSON, errs[1] = unb64(body.ClientDataJSON)
	in.AuthenticatorData, errs[2] = unb64(body.AuthenticatorData)
	in.Signature, errs[3] = unb64(body.Signature)
	in.UserHandle, errs[4] = unb64(body.UserHandle)
	if body.Type != "public-key" || errors.Join(errs[:]...) != nil || len(in.RawID) == 0 {
		passkeyError(w, http.StatusBadRequest, passkeyLoginFailed)
		return
	}
	var prev *auth.Session
	if sess, ok := auth.SessionFrom(r.Context()); ok {
		prev = &sess
	}
	l, err := s.auth.PasskeySignIn(r.Context(), in, rp, origin, s.proxies.ClientIP(r), r.UserAgent(), prev)
	var rl *auth.RateLimitedError
	switch {
	case err == nil:
		s.startSession(w, r, l.Token)
		writeJSON(w, http.StatusOK, map[string]string{"redirect": auth.SafeNext(body.Next)})
	case errors.As(err, &rl):
		passkeyError(w, http.StatusTooManyRequests, tooMany(w, rl))
	case errors.Is(err, auth.ErrBadCredentials):
		passkeyError(w, http.StatusUnauthorized, passkeyLoginFailed)
	default:
		slog.Error("passkey sign-in", "err", err)
		passkeyError(w, http.StatusInternalServerError, passkeyServerError)
	}
}

// ---- The Passkeys page ----

// passkeyMessages are the words the page script shows, sent as data-ts-msg-* attributes.
func passkeyMessages() []pages.UploadMsg {
	return []pages.UploadMsg{
		{Key: "waiting", Text: passkeyWaiting},
		{Key: "cancelled", Text: passkeyAddCancelled},
		{Key: "failed", Text: passkeyAddFail},
		{Key: "unsupported", Text: passkeyNoSupport},
	}
}

func (s *server) getMePasskeys(w http.ResponseWriter, r *http.Request) {
	v := pages.MePasskeysView{}
	switch {
	case r.URL.Query().Get("added") == "1":
		v.Banner = passkeyAdded
	case r.URL.Query().Get("removed") == "1":
		v.Banner = passkeyRemoved
	}
	s.mePasskeysPage(w, r, http.StatusOK, v)
}

func (s *server) mePasskeysPage(w http.ResponseWriter, r *http.Request, status int, v pages.MePasskeysView) {
	ctx := r.Context()
	u := currentUser(r)
	rows, err := s.auth.Passkeys(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "passkeys page", err)
		return
	}
	has, err := s.users.HasPassword(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "passkeys page", err)
		return
	}
	now := s.now()
	v.CSRF, v.Title, v.Lead, v.HasPassword = s.csrf.TokenFrom(ctx), passkeyTitle, passkeyLead, has
	for _, p := range rows {
		row := pages.PasskeyRowView{ID: p.ID, Name: p.Name, Remove: passkeyRemove, RemoveAria: fmt.Sprintf(passkeyRemoveAria, p.Name)}
		added := time.UnixMilli(p.CreatedAt).In(now.Location()).Format("2 Jan 2006")
		if p.LastUsedAt > 0 {
			row.Caption = fmt.Sprintf(passkeyAddedOn, added, agoText(now, time.UnixMilli(p.LastUsedAt)))
		} else {
			row.Caption = fmt.Sprintf(passkeyNotUsed, added)
		}
		row.Kind = passkeyOneDevice
		if p.BackupEligible {
			row.Kind = passkeySynced
		}
		if p.CopiedAt > 0 {
			row.Warning = passkeyCopied
		}
		v.Rows = append(v.Rows, row)
	}
	if _, _, reason := s.passkeyHere(r); reason != passkeyOK {
		v.Unavailable = passkeyReasonText(reason)
	} else {
		v.Module, v.Messages = s.assets.URL("js/passkey.js"), passkeyMessages()
	}
	s.prefsPage(w, r, status, passkeyTitle, "profile", "account", false, pages.MePasskeys(v), nil, pages.PasskeyStyles...)
}

// ---- Adding ----

func (s *server) postMePasskeyOptions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !passkeyBody(w, r, &body) {
		return
	}
	rp, origin, reason := s.passkeyHere(r)
	sess, ok := auth.SessionFrom(r.Context())
	if reason != passkeyOK || !ok {
		passkeyError(w, http.StatusNotFound, passkeyUnavailable)
		return
	}
	opts, err := s.auth.PasskeyRegisterBegin(r.Context(), sess, currentUser(r), body.Password, rp, origin, s.proxies.ClientIP(r))
	var rl *auth.RateLimitedError
	switch {
	case err == nil:
		exclude := make([]map[string]string, 0, len(opts.ExcludeIDs))
		for _, id := range opts.ExcludeIDs {
			exclude = append(exclude, map[string]string{"type": "public-key", "id": b64(id)})
		}
		params := []map[string]any{{"type": "public-key", "alg": -8}, {"type": "public-key", "alg": -7}, {"type": "public-key", "alg": -257}}
		writeJSON(w, http.StatusOK, map[string]any{"publicKey": map[string]any{
			"challenge": b64(opts.Challenge), "rp": map[string]string{"id": rp, "name": "Tendshelf"},
			"user":             map[string]string{"id": b64(opts.UserHandle), "name": opts.Username, "displayName": opts.DisplayName},
			"pubKeyCredParams": params, "timeout": passkeyTimeoutMS, "attestation": "none",
			"authenticatorSelection": map[string]any{"residentKey": "required", "requireResidentKey": true, "userVerification": "required"},
			"excludeCredentials":     exclude,
		}})
	case errors.Is(err, auth.ErrBadCredentials):
		passkeyError(w, http.StatusUnprocessableEntity, passkeyBadPass)
	case errors.As(err, &rl):
		passkeyError(w, http.StatusTooManyRequests, tooMany(w, rl))
	case errors.Is(err, auth.ErrPasskeyStale):
		passkeyError(w, http.StatusConflict, passkeyStale)
	case errors.Is(err, auth.ErrPasskeyLimit):
		passkeyError(w, http.StatusConflict, passkeyLimit)
	default:
		slog.Error("passkey register options", "err", err)
		passkeyError(w, http.StatusInternalServerError, passkeyServerError)
	}
}

func (s *server) postMePasskeyAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name           string   `json:"name"`
		Type           string   `json:"type"`
		ClientDataJSON string   `json:"clientDataJSON"`
		Attestation    string   `json:"attestationObject"`
		Transports     []string `json:"transports"`
	}
	if !passkeyBody(w, r, &body) {
		return
	}
	rp, origin, reason := s.passkeyHere(r)
	sess, ok := auth.SessionFrom(r.Context())
	if reason != passkeyOK || !ok {
		passkeyError(w, http.StatusNotFound, passkeyUnavailable)
		return
	}
	cd, err1 := unb64(body.ClientDataJSON)
	att, err2 := unb64(body.Attestation)
	if body.Type != "public-key" || err1 != nil || err2 != nil || len(body.Transports) > 8 {
		passkeyError(w, http.StatusBadRequest, passkeyAddFail)
		return
	}
	in := passkey.RegistrationInput{ClientDataJSON: cd, AttestationObject: att, Origin: origin, RPID: rp, Transports: body.Transports}
	_, err := s.auth.PasskeyRegister(r.Context(), sess, currentUser(r), body.Name, in)
	var fe users.FieldErrors
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"redirect": "/me/passkeys?added=1"})
	case errors.As(err, &fe):
		passkeyError(w, http.StatusUnprocessableEntity, passkeyNameLong)
	case errors.Is(err, auth.ErrPasskeyTaken):
		passkeyError(w, http.StatusConflict, passkeyTaken)
	case errors.Is(err, auth.ErrPasskeyLimit):
		passkeyError(w, http.StatusConflict, passkeyLimit)
	case errors.Is(err, auth.ErrPasskeyFailed):
		passkeyError(w, http.StatusUnauthorized, passkeyAddFail)
	default:
		slog.Error("passkey register", "err", err)
		passkeyError(w, http.StatusInternalServerError, passkeyServerError)
	}
}

// ---- Removing ----

func (s *server) postMePasskeyRemove(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	err := s.auth.PasskeyRemove(r.Context(), currentUser(r).ID, r.PathValue("pid"))
	switch {
	case err == nil:
		s.redirect(w, r, "/me/passkeys?removed=1")
	case errors.Is(err, users.ErrNotFound):
		s.notFound(w, r)
	case errors.Is(err, auth.ErrPasskeyLast):
		s.mePasskeysPage(w, r, http.StatusConflict, pages.MePasskeysView{Error: passkeyLast})
	default:
		s.serverError(w, r, "passkey remove", err)
	}
}

// postPersonPasskeysRemove is the admin's "Remove their passkeys": the role check before, and
// recheckManage (share.Can ManageUsers again) inside the transaction.
func (s *server) postPersonPasskeysRemove(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	done := s.changePerson(w, r, id, "", func(ctx context.Context, tx *sql.Tx, actor, t users.User) error {
		_, err := s.auth.PasskeysRemoveAll(ctx, tx, actor.ID, t.ID)
		return err
	})
	if done {
		s.answerPage(w, r, "/admin/users/"+id+"?passkeys=removed")
	}
}

// passkeyCaption is the Profile menu caption ("None yet", "1 passkey", "3 passkeys").
func passkeyCaption(n int) string {
	switch n {
	case 0:
		return passkeyNone
	case 1:
		return passkeyOne
	}
	return fmt.Sprintf(passkeyMany, n)
}
