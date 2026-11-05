package web

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// trickle sends a request whose body arrives one byte at a time over total, and returns the status
// the server answered (0 when the connection was cut).
func trickle(t *testing.T, base, method, path string, total time.Duration) int {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, method+" "+path+" HTTP/1.1\r\nHost: x\r\nContent-Length: 4\r\nContent-Type: application/octet-stream\r\n\r\n")
	for i := 0; i < 4; i++ {
		time.Sleep(total / 4)
		_, _ = conn.Write([]byte("a"))
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 12)
	n, _ := io.ReadFull(conn, buf)
	if n < 12 {
		return 0
	}
	var code int
	for _, c := range buf[9:12] {
		code = code*10 + int(c-'0')
	}
	return code
}

func TestBodyDeadlineCutsStalledPostsButSparesUploadsAndGets(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			// The chunk handler's own, longer deadline wins over the default.
			_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
		}
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "slow", http.StatusRequestTimeout)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(withBodyDeadline(read, 300*time.Millisecond, 5*time.Second))
	defer srv.Close()
	if got := trickle(t, srv.URL, "POST", "/login", 1200*time.Millisecond); got == http.StatusNoContent {
		t.Errorf("a stalled form POST finished with %d", got)
	}
	if got := trickle(t, srv.URL, "PUT", "/uploads/u1/files/f1", 1200*time.Millisecond); got != http.StatusNoContent {
		t.Errorf("a slow chunk upload got %d, want 204", got)
	}
	if got := trickle(t, srv.URL, "POST", "/books/b1/text", 1200*time.Millisecond); got != http.StatusNoContent {
		t.Errorf("a slow ebook upload got %d, want 204", got)
	}
	if got := trickle(t, srv.URL, "POST", "/login", 100*time.Millisecond); got != http.StatusNoContent {
		t.Errorf("a quick POST got %d, want 204", got)
	}
}
