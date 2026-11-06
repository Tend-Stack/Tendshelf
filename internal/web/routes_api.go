package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// apiTokenPath issues (POST) and revokes (DELETE) device tokens.
const apiTokenPath = "/api/v1/auth/token"

// maxTokenBody bounds the sign-in body: four short strings.
const maxTokenBody = 4 << 10

func (s *server) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/token", s.postAPIToken)
	mux.HandleFunc("DELETE /api/v1/auth/token", s.deleteAPIToken)
	mux.HandleFunc("GET /api/v1/me", s.getAPIMe)
	s.registerAPIRead(mux)
	s.registerAPISync(mux)
	s.registerAPIQueue(mux)
	s.registerAPISettings(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		api.Error(w, http.StatusNotFound, api.CodeNotFound, "There is nothing at this address.")
	})
}

type apiUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

// apiDevice times are Unix milliseconds UTC; lastSeenAt is 0 until the device is first used.
type apiDevice struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	CreatedAt  int64  `json:"createdAt"`
	LastSeenAt int64  `json:"lastSeenAt,omitempty"`
}

func apiUserOf(u users.User) apiUser {
	return apiUser{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: string(u.Role)}
}

func apiDeviceOf(d auth.Device) apiDevice {
	return apiDevice{ID: d.ID, Name: d.Name, Kind: string(d.Kind), CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt}
}

type tokenRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	// Code and Verifier are the other way to sign in: a one-time code from /app/connect and the
	// PKCE verifier of the app that started it (decision 0026). They exclude Username and Password.
	Code     string `json:"code"`
	Verifier string `json:"verifier"`
}

type tokenResponse struct {
	// Token is empty, and so left out, when a code only reconnected an app that already had a token.
	Token  string    `json:"token,omitempty"`
	Device apiDevice `json:"device"`
	User   apiUser   `json:"user"`
}

// postAPIToken signs an app in with username and password and answers with a device token, shown
// once. It uses the same limits and the same failure path as the sign-in form.
func (s *server) postAPIToken(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		api.Error(w, http.StatusUnsupportedMediaType, api.CodeUnsupported, "Send the sign-in as JSON.")
		return
	}
	var in tokenRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTokenBody)).Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			api.Error(w, http.StatusRequestEntityTooLarge, api.CodeTooLarge, "That request is too large.")
			return
		}
		api.Error(w, http.StatusBadRequest, api.CodeInvalid, "The request is not valid JSON.")
		return
	}
	if in.Code != "" {
		s.postAPITokenCode(w, r, in)
		return
	}
	kind, ok := auth.ParseDeviceKind(in.Kind)
	if !ok {
		api.Error(w, http.StatusBadRequest, api.CodeInvalid, `The kind must be "web", "pwa", "desktop" or "mobile".`)
		return
	}
	l, err := s.auth.IssueDevice(r.Context(), in.Username, in.Password, in.Name, kind, s.proxies.ClientIP(r))
	var rl *auth.RateLimitedError
	switch {
	case errors.As(err, &rl):
		w.Header().Set("Retry-After", strconv.Itoa(int((rl.RetryAfter+time.Second-1)/time.Second)))
		api.Error(w, http.StatusTooManyRequests, api.CodeRateLimited, "Too many attempts. Wait a little and try again.")
	case errors.Is(err, auth.ErrBadCredentials):
		api.Unauthorized(w, "Wrong username or password.")
	case errors.Is(err, auth.ErrTooManyDevices):
		api.Error(w, http.StatusConflict, api.CodeConflict,
			"This account has reached the limit of "+strconv.Itoa(auth.MaxLiveDevices)+" signed-in apps. Sign one out first.")
	case err != nil:
		slog.Error("issue device token", "err", err)
		api.Error(w, http.StatusServiceUnavailable, api.CodeUnavailable, "Something went wrong on our side. Try again in a moment.")
	default:
		api.Write(w, http.StatusCreated, tokenResponse{Token: l.Token, Device: apiDeviceOf(l.Device), User: apiUserOf(l.User)})
	}
}

// msgCodeExpired is the one answer to every code that does not work, so a guess learns nothing.
const msgCodeExpired = "This sign-in has expired. Start again from the app."

// postAPITokenCode trades a one-time code and its PKCE verifier for a mobile device token (201), or,
// when the caller already holds a live token of the same person, rebinds the new web session to that
// device (200, no token). A wrong code counts against the address on the same limiter as app keys
// and is checked before any database read.
func (s *server) postAPITokenCode(w http.ResponseWriter, r *http.Request, in tokenRequest) {
	ip := s.proxies.ClientIP(r)
	key := ip.String()
	if ok, wait := s.keyLimiter.Allow(key); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int((wait+time.Second-1)/time.Second))))
		api.Error(w, http.StatusTooManyRequests, api.CodeRateLimited, "Too many attempts. Wait a little and try again.")
		return
	}
	if in.Username != "" || in.Password != "" {
		apiBadField(w, "code", "Send a code or a username and password, not both.")
		return
	}
	if kind, ok := auth.ParseDeviceKind(in.Kind); !ok || kind != auth.KindMobile {
		apiBadField(w, "kind", `A code signs in a "mobile" app only.`)
		return
	}
	var caller *auth.Device
	if d, ok := auth.DeviceFrom(r.Context()); ok {
		caller = &d
	}
	l, err := s.auth.SwapDeviceCode(r.Context(), in.Code, in.Verifier, in.Name, caller, ip)
	switch {
	case errors.Is(err, auth.ErrBadCode):
		s.keyLimiter.Fail(key)
		api.Unauthorized(w, msgCodeExpired)
	case errors.Is(err, auth.ErrCodeOtherPerson):
		api.Error(w, http.StatusConflict, api.CodeConflict, "This sign-in belongs to someone else. Start again from the app.")
	case errors.Is(err, auth.ErrTooManyDevices):
		api.Error(w, http.StatusConflict, api.CodeConflict,
			"This account has reached the limit of "+strconv.Itoa(auth.MaxLiveDevices)+" signed-in apps. Sign one out first.")
	case err != nil:
		slog.Error("swap device code", "err", err) // never the code
		api.Error(w, http.StatusServiceUnavailable, api.CodeUnavailable, "Something went wrong on our side. Try again in a moment.")
	case l.Token == "":
		w.Header().Set("Cache-Control", "no-store")
		api.Write(w, http.StatusOK, tokenResponse{Device: apiDeviceOf(l.Device), User: apiUserOf(l.User)})
	default:
		w.Header().Set("Cache-Control", "no-store")
		api.Write(w, http.StatusCreated, tokenResponse{Token: l.Token, Device: apiDeviceOf(l.Device), User: apiUserOf(l.User)})
	}
}

// deleteAPIToken signs the calling device out. Only a device token can: a browser session signs
// out through /logout.
func (s *server) deleteAPIToken(w http.ResponseWriter, r *http.Request) {
	dev, ok := auth.DeviceFrom(r.Context())
	if !ok {
		api.Error(w, http.StatusBadRequest, api.CodeInvalid, "Only a device token can sign itself out.")
		return
	}
	if _, err := s.auth.RevokeDevice(r.Context(), dev.UserID, dev.ID, dev.UserID); err != nil {
		slog.Error("revoke device token", "err", err)
		api.Error(w, http.StatusServiceUnavailable, api.CodeUnavailable, "Something went wrong on our side. Try again in a moment.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// getAPIMe answers who is asking: the person, and the device when the request used a token.
func (s *server) getAPIMe(w http.ResponseWriter, r *http.Request) {
	out := struct {
		User   apiUser    `json:"user"`
		Device *apiDevice `json:"device"`
	}{User: apiUserOf(currentUser(r))}
	if d, ok := auth.DeviceFrom(r.Context()); ok {
		ad := apiDeviceOf(d)
		out.Device = &ad
	}
	api.Write(w, http.StatusOK, out)
}
