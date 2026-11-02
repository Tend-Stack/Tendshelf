package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestErrorFieldShape(t *testing.T) {
	rec := httptest.NewRecorder()
	ErrorField(rec, 400, CodeInvalid, "limit", "Bad limit.")
	if got := rec.Body.String(); got != `{"error":{"code":"invalid","message":"Bad limit.","field":"limit"}}`+"\n" {
		t.Fatalf("body: %s", got)
	}
	rec = httptest.NewRecorder()
	Error(rec, 400, CodeInvalid, "x")
	if strings.Contains(rec.Body.String(), "field") {
		t.Fatalf("field leaked: %s", rec.Body)
	}
}

func TestNewList(t *testing.T) {
	b, _ := json.Marshal(NewList[int](nil, "", nil))
	if string(b) != `{"items":[],"next":null}` {
		t.Fatalf("empty: %s", b)
	}
	n := 7
	b, _ = json.Marshal(NewList([]int{1}, "abc", &n))
	if string(b) != `{"items":[1],"next":"abc","total":7}` {
		t.Fatalf("full: %s", b)
	}
}

func TestWriteCached(t *testing.T) {
	get := func(inm string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/x", nil)
		if inm != "" {
			r.Header.Set("If-None-Match", inm)
		}
		rec := httptest.NewRecorder()
		WriteCached(rec, r, 200, map[string]int{"a": 1})
		return rec
	}
	first := get("")
	etag := first.Header().Get("ETag")
	if first.Code != 200 || len(etag) != 34 || first.Header().Get("Cache-Control") != "private, no-cache" ||
		first.Header().Get("Vary") != "Authorization, Cookie" {
		t.Fatalf("first: %d %v", first.Code, first.Header())
	}
	for _, inm := range []string{etag, "W/" + etag, `"zzz", ` + etag, "*"} {
		if rec := get(inm); rec.Code != 304 || rec.Body.Len() != 0 {
			t.Fatalf("inm %q: %d", inm, rec.Code)
		}
	}
	if rec := get(`"other"`); rec.Code != 200 {
		t.Fatalf("mismatch: %d", rec.Code)
	}
}

func TestStatusError(t *testing.T) {
	cases := map[int]string{400: "invalid", 401: "unauthorized", 403: "forbidden", 404: "not_found", 405: "invalid",
		413: "too_large", 415: "unsupported", 429: "rate_limited", 500: "unavailable", 502: "unavailable"}
	for status, code := range cases {
		rec := httptest.NewRecorder()
		StatusError(rec, status)
		var got struct{ Error struct{ Code string } }
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		want := status
		if status >= 500 {
			want = 503
		}
		if rec.Code != want || got.Error.Code != code || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%d: %d %s", status, rec.Code, rec.Body)
		}
	}
}
