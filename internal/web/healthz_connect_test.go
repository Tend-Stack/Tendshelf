package web

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestA2HealthzAdvertisesConnect: the Android app refuses a server whose /healthz lacks "connect"
// ("needs an update first"), and the older keys stay, so the desktop shell's check still passes.
func TestA2HealthzAdvertisesConnect(t *testing.T) {
	a := newTestApp(t)
	rec := a.do(http.MethodGet, "/healthz", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz: %d", rec.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"status": "ok", "app": "tendshelf", "shell": "1", "connect": "1"} {
		if got[k] != want {
			t.Errorf("healthz[%q] = %q, want %q", k, got[k], want)
		}
	}
	if len(got["id"]) != 32 {
		t.Errorf("healthz id = %q, want 32 hex characters", got["id"])
	}
	again := a.do(http.MethodGet, "/healthz", nil, nil)
	var second map[string]string
	_ = json.Unmarshal(again.Body.Bytes(), &second)
	if second["id"] != got["id"] {
		t.Error("healthz id changes between requests")
	}
	if _, ok := got["version"]; !ok {
		t.Error("healthz has no version")
	}
}
