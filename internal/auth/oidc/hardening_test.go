package oidc_test

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth/oidc/oidctest"
)

// A provider that sends email_verified as the string "true" is not saying the address is verified.
func TestEmailVerifiedStringIsUnverified(t *testing.T) {
	r := newRig(t, 0)
	r.p.Update(func(p *oidctest.Provider) { p.Quirk = "verified-string" })
	c, err := r.signIn()
	if err != nil {
		t.Fatal(err)
	}
	if c.EmailVerified {
		t.Error("string \"true\" was treated as verified")
	}
	if c.Email == "" {
		t.Error("the address itself should still arrive")
	}
}
