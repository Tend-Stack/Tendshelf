// Package api holds the JSON helpers shared by the /api/v1 routes: one success writer and one
// error shape, so every client sees the same envelope. It is a leaf package.
package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
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
	Field   string `json:"field,omitempty"`
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

// ErrorField is Error naming the request field that was wrong (a query parameter or a body key).
func ErrorField(w http.ResponseWriter, status int, code, field, msg string) {
	Write(w, status, errorBody{Error: errorDetail{Code: code, Message: msg, Field: field}})
}

// List is the body of every paged list: next is null on the last page, total appears only where the
// repository counts it, and items is always an array.
type List[T any] struct {
	Items []T     `json:"items"`
	Next  *string `json:"next"`
	Total *int    `json:"total,omitempty"`
}

// NewList builds a List: nil items become [] and an empty next becomes null.
func NewList[T any](items []T, next string, total *int) List[T] {
	if items == nil {
		items = []T{}
	}
	l := List[T]{Items: items, Total: total}
	if next != "" {
		l.Next = &next
	}
	return l
}

// WriteCached sends v like Write but with a validator: the ETag is a hash of the body, and a request
// whose If-None-Match names it gets 304 with no body. The answer is per person, so it is private and
// must be revalidated every time.
func WriteCached(w http.ResponseWriter, r *http.Request, status int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		slog.Error("api: encode json", "err", err)
		Error(w, http.StatusServiceUnavailable, CodeUnavailable, "Something went wrong on our side. Try again in a moment.")
		return
	}
	sum := sha256.Sum256(buf.Bytes())
	etag := `"` + hex.EncodeToString(sum[:])[:32] + `"`
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "private, no-cache")
	h.Set("Vary", "Authorization, Cookie")
	if status == http.StatusOK && etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// etagMatches compares each comma-separated entry (weak marker ignored); * matches anything.
func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if part == "*" || part == etag {
			return true
		}
	}
	return false
}

// StatusError answers a bare HTTP status in the JSON shape, for code that only knows a status
// (the pages' error helpers, reused under /api/).
func StatusError(w http.ResponseWriter, status int) {
	switch {
	case status == http.StatusBadRequest:
		Error(w, status, CodeInvalid, "That request is not valid.")
	case status == http.StatusUnauthorized:
		Unauthorized(w, "Sign in first.")
	case status == http.StatusForbidden:
		Error(w, status, CodeForbidden, "That is not allowed.")
	case status == http.StatusNotFound:
		Error(w, status, CodeNotFound, "There is nothing at this address.")
	case status == http.StatusMethodNotAllowed:
		Error(w, status, CodeInvalid, "This address does not take that method.")
	case status == http.StatusRequestEntityTooLarge:
		Error(w, status, CodeTooLarge, "That request is too large.")
	case status == http.StatusUnsupportedMediaType:
		Error(w, status, CodeUnsupported, "That kind of content is not supported here.")
	case status == http.StatusTooManyRequests:
		Error(w, status, CodeRateLimited, "Too many requests. Wait a little and try again.")
	case status >= 500:
		Error(w, http.StatusServiceUnavailable, CodeUnavailable, "Something went wrong on our side. Try again in a moment.")
	default:
		Error(w, status, CodeInvalid, "That request could not be completed.")
	}
}
