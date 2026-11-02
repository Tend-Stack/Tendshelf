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
}

type tokenResponse struct {
	Token  string    `json:"token"`
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
