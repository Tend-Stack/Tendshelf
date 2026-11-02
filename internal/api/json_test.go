package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteAndError(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, http.StatusCreated, map[string]int{"n": 1})
	if rec.Code != 201 || rec.Header().Get("Cache-Control") != "no-store" ||
		rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("write: %d %v", rec.Code, rec.Header())
	}

	rec = httptest.NewRecorder()
	Error(rec, http.StatusConflict, CodeConflict, "Too many.")
	var got struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 409 || got.Error.Code != "conflict" || got.Error.Message != "Too many." {
		t.Fatalf("error: %d %s", rec.Code, rec.Body)
	}
}

func TestUnauthorizedChallenge(t *testing.T) {
	rec := httptest.NewRecorder()
	Unauthorized(rec, "Sign in first.")
	if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != `Bearer realm="Tendshelf"` {
		t.Fatalf("unauthorized: %d %v", rec.Code, rec.Header())
	}
}
