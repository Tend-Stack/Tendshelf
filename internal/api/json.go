// Package api holds the JSON helpers shared by the /api/v1 routes: one success writer and one
// error shape, so every client sees the same envelope. It is a leaf package.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Error codes: the stable, machine-readable part of an error answer.
const (
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeInvalid      = "invalid"
	CodeConflict     = "conflict"
	CodeTooLarge     = "too_large"
	CodeRateLimited  = "rate_limited"
	CodeUnsupported  = "unsupported"
	CodeUnavailable  = "unavailable"
)

// Write sends v as JSON with the given status. Answers are never cached: they carry credentials
// or per-person data.
func Write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("api: write json", "err", err)
	}
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error sends {"error":{"code","message"}}. The message is plain words for a person, never a
// stack trace or a raw library error.
func Error(w http.ResponseWriter, status int, code, msg string) {
	Write(w, status, errorBody{Error: errorDetail{Code: code, Message: msg}})
}

// Unauthorized sends 401 with the Bearer challenge.
func Unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="Tendshelf"`)
	Error(w, http.StatusUnauthorized, CodeUnauthorized, msg)
}
