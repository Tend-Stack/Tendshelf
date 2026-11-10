package podnet

import (
	"context"
	"io"
	"net/http"
	"testing"
)

// TestOpenLiveMetaintOutOfRange: a station that names a title-block size outside the bound, or one that is
// not a number, is a bad answer rather than audio passed through with title blocks inside.
func TestOpenLiveMetaintOutOfRange(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		if v := q.URL.Query().Get("m"); v != "" {
			w.Header().Set("Icy-Metaint", v)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, "audio")
	})
	for _, tc := range []struct {
		m    string
		want string
		n    int
	}{{"", "", 0}, {"0", "", 0}, {"16000", "", 16000}, {"1048576", "", 1048576}, {"1048577", "bad_answer", 0}, {"-5", "bad_answer", 0}, {"abc", "bad_answer", 0}} {
		up, n, err := r.c.OpenLive(context.Background(), r.url("radio.example", "/live?m="+tc.m))
		got := ""
		if err != nil {
			got = code(err)
		} else {
			up.Body.Close()
		}
		if got != tc.want || n != tc.n {
			t.Errorf("metaint %q: code %q n %d, want %q %d", tc.m, got, n, tc.want, tc.n)
		}
	}
}
