package oidc_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth/oidc"
	"github.com/Tend-Stack/tendshelf/internal/auth/oidc/oidctest"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type rig struct {
	t   *testing.T
	p   *oidctest.Provider
	c   *oidc.Client
	clk *clock
	cfg oidc.Config
}

func newRig(t *testing.T, timeout time.Duration) *rig {
	t.Helper()
	clk := &clock{t: time.Now()}
	p := oidctest.New(t)
	p.Update(func(p *oidctest.Provider) { p.Now = clk.Now })
	return &rig{t: t, p: p, clk: clk, c: oidc.NewClient(oidc.ClientOptions{Now: clk.Now, Timeout: timeout}),
		cfg: oidc.Config{Enabled: true, Name: "Fake", Issuer: p.Issuer(), ClientID: "tendshelf", ServerAddress: "http://127.0.0.1:8095"}}
}

// signIn runs discovery, the authorize hop and the exchange the way the service does.
func (r *rig) signIn() (oidc.Claims, error) {
	r.t.Helper()
	prov, err := r.c.Discover(context.Background(), r.cfg.Issuer)
	if err != nil {
		return oidc.Claims{}, err
	}
	return r.exchange(prov)
}

func (r *rig) exchange(prov oidc.Provider) (oidc.Claims, error) {
	r.t.Helper()
	state, nonce, verifier := oidc.RandomToken(), oidc.RandomToken(), oidc.RandomToken()
	code, gotState := r.p.Authorize(oidc.AuthorizeURL(prov, r.cfg, state, nonce, verifier))
	if gotState != state {
		r.t.Fatalf("state came back as %q", gotState)
	}
	return r.c.Exchange(context.Background(), prov, r.cfg, code, verifier, nonce)
}

func reason(err error) string {
	var te *oidc.TokenError
	if errors.As(err, &te) {
		return te.Reason
	}
	return ""
}

func TestHappyPathAlgorithms(t *testing.T) {
	for _, alg := range []string{"RS256", "RS384", "RS512", "PS256", "ES256", "EdDSA"} {
		t.Run(alg, func(t *testing.T) {
			r := newRig(t, 0)
			r.p.Update(func(p *oidctest.Provider) { p.Alg = alg; p.SigningAlgs = []string{alg} })
			cl, err := r.signIn()
			if err != nil {
				t.Fatal(err)
			}
			if cl.Subject != "sub-1" || cl.Email != "ana@example.com" || !cl.EmailVerified || cl.Name != "Ana Lopez" {
				t.Fatalf("claims = %+v", cl)
			}
		})
	}
}

func TestClaimsFromUserinfoWin(t *testing.T) {
	r := newRig(t, 0)
	r.p.Update(func(p *oidctest.Provider) { p.EmailInUserinfo = true; p.PreferredName = "ana" })
	cl, err := r.signIn()
	if err != nil {
		t.Fatal(err)
	}
	if cl.Email != "ana@example.com" || !cl.EmailVerified || cl.PreferredUsername != "ana" {
		t.Fatalf("claims = %+v", cl)
	}
}

func TestClientAuthMethods(t *testing.T) {
	for name, methods := range map[string][]string{
		"basic": {"client_secret_basic"}, "post": {"client_secret_post"}, "both": nil,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, 0)
			r.cfg.ClientSecret = "s3cret/with +odd&chars"
			r.p.Update(func(p *oidctest.Provider) { p.ClientSecret = r.cfg.ClientSecret; p.AuthMethods = methods })
			if _, err := r.signIn(); err != nil {
				t.Fatal(err)
			}
		})
	}
	r := newRig(t, 0)
	r.cfg.ClientSecret = "wrong"
	r.p.Update(func(p *oidctest.Provider) { p.ClientSecret = "right" })
	if _, err := r.signIn(); !errors.Is(err, oidc.ErrProviderRefused) {
		t.Fatalf("wrong secret err = %v", err)
	}
}

func TestIDTokenRefusals(t *testing.T) {
	min := time.Minute
	for _, tc := range []struct {
		name string
		set  func(*oidctest.Provider)
		want string
	}{
		{"alg none", func(p *oidctest.Provider) { p.Alg = "none" }, "alg"},
		{"alg HS256", func(p *oidctest.Provider) { p.Alg = "HS256" }, "alg"},
		{"unknown kid after refetch", func(p *oidctest.Provider) { p.Kid = "nope" }, "kid"},
		{"wrong key RSA", func(p *oidctest.Provider) { p.WrongKey = true }, "signature"},
		{"wrong key EC", func(p *oidctest.Provider) { p.Alg = "ES256"; p.WrongKey = true }, "signature"},
		{"wrong key EdDSA", func(p *oidctest.Provider) { p.Alg = "EdDSA"; p.WrongKey = true }, "signature"},
		{"crit header", func(p *oidctest.Provider) { p.Crit = true }, "malformed"},
		{"rsa 1024", func(p *oidctest.Provider) { p.WeakKey = true }, "kid"},
		{"jwk alg mismatch", func(p *oidctest.Provider) { p.JWKAlg = "RS384" }, "alg"},
		{"key type does not fit alg", func(p *oidctest.Provider) { p.Alg = "ES256"; p.Kid = "rsa1" }, "alg"},
		{"encryption key", func(p *oidctest.Provider) { p.Kid = "enc1" }, "kid"},
		{"iss wrong", func(p *oidctest.Provider) { p.IssClaim = "https://evil.example" }, "issuer"},
		{"aud lacks client", func(p *oidctest.Provider) { p.Aud = []string{"someone-else"} }, "audience"},
		{"multi aud no azp", func(p *oidctest.Provider) { p.Aud = []string{"tendshelf", "other"} }, "azp"},
		{"wrong azp", func(p *oidctest.Provider) { p.Aud = []string{"tendshelf", "other"}; p.Azp = "other" }, "azp"},
		{"exp 3 min past", func(p *oidctest.Provider) { p.ExpIn = -3 * min }, "expired"},
		{"nbf in the future", func(p *oidctest.Provider) { p.Nbf = 5 * min }, "not_yet"},
		{"iat 11 min old", func(p *oidctest.Provider) { p.IatAgo = 11 * min }, "issued_at"},
		{"iat in the future", func(p *oidctest.Provider) { p.IatAgo = -5 * min }, "issued_at"},
		{"ES256 signature in DER", func(p *oidctest.Provider) { p.Alg = "ES256"; p.Quirk = "der-sig" }, "signature"},
		{"ES256 signature one byte short", func(p *oidctest.Provider) { p.Alg = "ES256"; p.Quirk = "short-sig" }, "signature"},
		{"exp missing", func(p *oidctest.Provider) { p.Quirk = "no-exp" }, "expired"},
		{"iat missing", func(p *oidctest.Provider) { p.Quirk = "no-iat" }, "issued_at"},
		{"rsa over 8192 bits", func(p *oidctest.Provider) { p.Quirk = "huge-rsa" }, "kid"},
		{"nonce wrong", func(p *oidctest.Provider) { p.NonceOverride = "another-nonce" }, "nonce"},
		{"userinfo sub differs", func(p *oidctest.Provider) { p.UserinfoSub = "someone-else" }, "userinfo_sub"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, 0)
			r.p.Update(tc.set)
			_, err := r.signIn()
			if got := reason(err); got != tc.want {
				t.Fatalf("reason = %q (%v), want %q", got, err, tc.want)
			}
		})
	}
}

func TestIDTokenAccepted(t *testing.T) {
	for name, set := range map[string]func(*oidctest.Provider){
		"exp 1 min past (leeway)":  func(p *oidctest.Provider) { p.ExpIn = -time.Minute },
		"multi aud with right azp": func(p *oidctest.Provider) { p.Aud = []string{"tendshelf", "other"}; p.Azp = "tendshelf" },
		"iat 9 min old":            func(p *oidctest.Provider) { p.IatAgo = 9 * time.Minute },
		"nbf 1 min ahead (leeway)": func(p *oidctest.Provider) { p.Nbf = time.Minute },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, 0)
			r.p.Update(set)
			if _, err := r.signIn(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProviderTrouble(t *testing.T) {
	t.Run("discovery issuer differs", func(t *testing.T) {
		r := newRig(t, 0)
		r.p.Update(func(p *oidctest.Provider) { p.DiscoveryIssuer = "https://other.example" })
		_, err := r.signIn()
		var im *oidc.IssuerMismatchError
		if !errors.As(err, &im) || im.Got != "https://other.example" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no accepted signing algorithm", func(t *testing.T) {
		r := newRig(t, 0)
		r.p.Update(func(p *oidctest.Provider) { p.SigningAlgs = []string{"HS256", "none"} })
		_, err := r.signIn()
		var ae *oidc.AlgError
		if !errors.As(err, &ae) || len(ae.Offered) != 2 {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("token endpoint 400", func(t *testing.T) {
		r := newRig(t, 0)
		r.p.Update(func(p *oidctest.Provider) { p.TokenStatus = 400 })
		if _, err := r.signIn(); !errors.Is(err, oidc.ErrProviderRefused) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("token endpoint 503", func(t *testing.T) {
		r := newRig(t, 0)
		r.p.Update(func(p *oidctest.Provider) { p.TokenStatus = 503 })
		if _, err := r.signIn(); !errors.Is(err, oidc.ErrProviderDown) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("provider down", func(t *testing.T) {
		r := newRig(t, 0)
		r.p.Update(func(p *oidctest.Provider) { p.Down = true })
		if _, err := r.signIn(); !errors.Is(err, oidc.ErrProviderDown) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("slow past the timeout", func(t *testing.T) {
		r := newRig(t, 150*time.Millisecond)
		r.p.Update(func(p *oidctest.Provider) { p.Slow = 600 * time.Millisecond })
		start := time.Now()
		if _, err := r.signIn(); !errors.Is(err, oidc.ErrProviderDown) {
			t.Fatalf("err = %v", err)
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("the timeout did not cut the request short")
		}
	})
	t.Run("body over the cap", func(t *testing.T) {
		r := newRig(t, 0)
		r.p.Update(func(p *oidctest.Provider) { p.HugeBody = true })
		if _, err := r.signIn(); !errors.Is(err, oidc.ErrProviderConfig) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("token body over the cap", func(t *testing.T) {
		r := newRig(t, 0)
		prov, err := r.c.Discover(context.Background(), r.cfg.Issuer)
		if err != nil {
			t.Fatal(err)
		}
		r.p.Update(func(p *oidctest.Provider) { p.HugeBody = true })
		if _, err := r.exchange(prov); err == nil || (!errors.Is(err, oidc.ErrProviderConfig) && reason(err) == "") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("discovery redirect not followed", func(t *testing.T) {
		r := newRig(t, 0)
		r.p.Update(func(p *oidctest.Provider) { p.RedirectDiscovery = true })
		if _, err := r.signIn(); !errors.Is(err, oidc.ErrProviderDown) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("endpoint on another origin refused before any request", func(t *testing.T) {
		r := newRig(t, 0)
		other := oidctest.New(t)
		r.p.Update(func(p *oidctest.Provider) { p.TokenURL = other.Issuer() + "/token" })
		if _, err := r.signIn(); !errors.Is(err, oidc.ErrProviderConfig) {
			t.Fatalf("err = %v", err)
		}
		if other.TokenHits.Load() != 0 {
			t.Fatal("the other origin was contacted")
		}
	})
	t.Run("JWKS on another origin refused", func(t *testing.T) {
		r := newRig(t, 0)
		other := oidctest.New(t)
		r.p.Update(func(p *oidctest.Provider) { p.JWKSURL = other.Issuer() + "/jwks" })
		if _, err := r.signIn(); !errors.Is(err, oidc.ErrProviderConfig) {
			t.Fatalf("err = %v", err)
		}
		if other.JWKSHits.Load() != 0 {
			t.Fatal("the other origin was contacted")
		}
	})
	t.Run("userinfo failure only means fewer claims", func(t *testing.T) {
		r := newRig(t, 0)
		r.p.Update(func(p *oidctest.Provider) { p.EmailInUserinfo = true })
		prov, err := r.c.Discover(context.Background(), r.cfg.Issuer)
		if err != nil {
			t.Fatal(err)
		}
		prov.UserinfoEndpoint = r.p.Issuer() + "/missing"
		cl, err := r.exchange(prov)
		if err != nil || cl.Email != "" || cl.EmailVerified {
			t.Fatalf("claims = %+v, %v", cl, err)
		}
	})
}

func TestCaches(t *testing.T) {
	r := newRig(t, 0)
	for range 2 {
		if _, err := r.signIn(); err != nil {
			t.Fatal(err)
		}
	}
	if n := r.p.DiscoveryHits.Load(); n != 1 {
		t.Errorf("discovery fetches = %d, want 1", n)
	}
	if n := r.p.JWKSHits.Load(); n != 1 {
		t.Errorf("JWKS fetches for two sign-ins = %d, want 1", n)
	}
	// An unknown kid refetches at most once per five minutes.
	r.p.Update(func(p *oidctest.Provider) { p.Kid = "nope" })
	before := r.p.JWKSHits.Load()
	for range 3 {
		if _, err := r.signIn(); reason(err) != "kid" {
			t.Fatalf("err = %v", err)
		}
	}
	if n := r.p.JWKSHits.Load() - before; n != 0 {
		t.Errorf("refetches within five minutes = %d, want 0", n)
	}
	r.clk.Advance(6 * time.Minute)
	for range 3 {
		if _, err := r.signIn(); reason(err) != "kid" {
			t.Fatalf("err = %v", err)
		}
	}
	if n := r.p.JWKSHits.Load() - before; n != 1 {
		t.Errorf("refetches after five minutes = %d, want 1", n)
	}
	// Discovery expires after an hour.
	r.clk.Advance(2 * time.Hour)
	r.p.Update(func(p *oidctest.Provider) { p.Kid = "" })
	if _, err := r.signIn(); err != nil {
		t.Fatal(err)
	}
	if n := r.p.DiscoveryHits.Load(); n != 2 {
		t.Errorf("discovery fetches after an hour = %d, want 2", n)
	}
}

func TestAuthorizeURLAndOrigins(t *testing.T) {
	r := newRig(t, 0)
	prov, err := r.c.Discover(context.Background(), r.cfg.Issuer)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(oidc.AuthorizeURL(prov, r.cfg, "st", "no", "verifier"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for k, want := range map[string]string{"response_type": "code", "client_id": "tendshelf", "scope": "openid email profile",
		"state": "st", "nonce": "no", "code_challenge_method": "S256", "redirect_uri": "http://127.0.0.1:8095/login/oidc/callback",
		"code_challenge": oidc.Challenge("verifier")} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if o := oidc.Origins(prov); len(o) != 1 || o[0] != r.p.Issuer() {
		t.Errorf("origins = %v", o)
	}
	if len(oidc.RandomToken()) != 43 {
		t.Error("random tokens are not 43 characters")
	}
}

func TestParseRules(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://auth.example.com": true, "https://auth.example.com/": true, "https://auth.example.com/realms/x": true,
		"http://127.0.0.1:9000": true, "http://localhost:9000": true, "http://auth.example.com": false,
		"https://u:p@auth.example.com": false, "https://auth.example.com?x=1": false, "https://auth.example.com#f": false,
		"": false, "ftp://auth.example.com": false, "https://": false, "https://a b.example.com": false,
	} {
		if got := oidc.ParseIssuer(raw); got != want {
			t.Errorf("ParseIssuer(%q) = %v, want %v", raw, got, want)
		}
	}
	for raw, want := range map[string]string{
		"https://Music.Example.com:443": "https://music.example.com", "http://127.0.0.1:8095/": "http://127.0.0.1:8095",
		"https://music.example.com/path": "", "http://music.example.com": "",
	} {
		got, ok := oidc.ParseServerAddress(raw)
		if got != want || ok != (want != "") {
			t.Errorf("ParseServerAddress(%q) = %q, %v; want %q", raw, got, ok, want)
		}
	}
	if !strings.HasSuffix((oidc.Config{ServerAddress: "https://x"}).RedirectURL(), "/login/oidc/callback") {
		t.Error("redirect url")
	}
}
