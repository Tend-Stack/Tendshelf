package auth

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestUntrustedForwardedWarnsOnce(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	warnUntrustedForwarded = sync.Once{}

	var none Proxies
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	if got := none.ClientIP(req); got.String() != "10.0.0.5" {
		t.Fatalf("ClientIP = %s", got)
	}
	plain := httptest.NewRequest("GET", "/", nil)
	plain.RemoteAddr = "10.0.0.5:1234"
	none.ClientIP(plain)
	if buf.Len() != 0 {
		t.Fatalf("a request without the header warned: %s", buf.String())
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	none.ClientIP(req)
	none.ClientIP(req)
	if n := strings.Count(buf.String(), "X-Forwarded-For"); n != 1 {
		t.Errorf("warned %d times, want 1: %s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "trusted-proxy") {
		t.Errorf("warning does not name the flag: %s", buf.String())
	}
}
