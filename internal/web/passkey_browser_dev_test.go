//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestBrowserPasskeyAddAndSignIn drives the real page script against Chromium's virtual authenticator
// (CDP WebAuthn domain, resident keys and user verification on, presence simulated): add a passkey
// on the Passkeys page with the password, then sign in with it from a browser that holds no
// session. The host must be localhost: an IP address is no relying-party id (decision 0041).
func TestBrowserPasskeyAddAndSignIn(t *testing.T) {
	b := chromium(t)
	a := newTestApp(t)
	own := a.owner(t)

	// Until the sign-in step the owner's cookie rides along on every request, as if the browser held it.
	var signedIn atomic.Bool
	signedIn.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if signedIn.Load() {
			r.AddCookie(&http.Cookie{Name: own.Name, Value: own.Value})
		}
		a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	base := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	tab := b.NewTab(1024, 800, false, true)
	if err := tab.Call("WebAuthn.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := tab.Call("WebAuthn.addVirtualAuthenticator", map[string]any{"options": map[string]any{
		"protocol": "ctap2", "transport": "internal", "hasResidentKey": true, "hasUserVerification": true,
		"isUserVerified": true, "automaticPresenceSimulation": true,
	}}, nil); err != nil {
		t.Fatal(err)
	}

	// Add: the form is hidden until the script has run, then shown.
	tab.Navigate(base + "/me/passkeys")
	tab.WaitFor(`document.querySelector('[data-ts-passkey-form]') && !document.querySelector('[data-ts-passkey-form]').hidden`, 5*time.Second)
	tab.Eval(`(() => {
		document.querySelector('#pk-name').value = 'Virtual key';
		document.querySelector('#pk-password').value = `+fmt.Sprintf("%q", testPassword)+`;
		document.querySelector('[data-ts-passkey-button]').click();
	})()`, nil)
	tab.WaitFor(`location.search === '?added=1' && document.readyState === 'complete' && document.body.textContent.includes('Passkey added.')`, 15*time.Second)
	var listed bool
	tab.Eval(`document.body.textContent.includes('Virtual key') && document.body.textContent.includes('Not used yet')`, &listed)
	if !listed {
		t.Fatal("the new passkey is not listed")
	}
	if n := a.count(t, `SELECT COUNT(*) FROM passkeys`); n != 1 {
		t.Fatalf("%d passkeys stored", n)
	}

	// Sign in: no session, the button shows, and one click lands on the home page.
	signedIn.Store(false)
	if err := tab.Call("Network.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := tab.Call("Network.clearBrowserCookies", nil, nil); err != nil {
		t.Fatal(err)
	}
	tab.Navigate(base + "/login")
	tab.WaitFor(`document.querySelector('.ts-passkey-login') && !document.querySelector('.ts-passkey-login').hidden`, 5*time.Second)
	tab.Eval(`document.querySelector('.ts-passkey-login [data-ts-passkey-button]').click()`, nil)
	tab.WaitFor(`location.pathname === '/' && document.readyState === 'complete' && document.querySelector('#main')`, 15*time.Second)
	if n := a.count(t, `SELECT COUNT(*) FROM passkeys WHERE last_used_at IS NOT NULL`); n != 1 {
		t.Fatalf("last_used_at not set: %d", n)
	}
	if n := a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.login' AND detail_json LIKE '%passkey%'`); n != 1 {
		t.Fatalf("passkey sign-ins audited: %d", n)
	}
}
