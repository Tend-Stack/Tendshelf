// Package oidctest is a fake OpenID Connect provider on a loopback httptest server, for tests only
// (decision 0037). It is a non-test package so that internal/auth and internal/web tests can import
// it; no production code does. Change its knobs with Update so a running handler never races a test.
package oidctest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	keysOnce sync.Once
	keys     struct {
		rsaKey, otherRSA, weakRSA *rsa.PrivateKey
		ecKey, otherEC            *ecdsa.PrivateKey
		edPub                     ed25519.PublicKey
		edKey                     ed25519.PrivateKey
	}
)

func loadKeys() {
	keysOnce.Do(func() {
		var err error
		must := func(e error) {
			if e != nil {
				panic(e)
			}
		}
		keys.rsaKey, err = rsa.GenerateKey(rand.Reader, 2048)
		must(err)
		keys.otherRSA, err = rsa.GenerateKey(rand.Reader, 2048)
		must(err)
		keys.weakRSA, err = rsa.GenerateKey(rand.Reader, 1024)
		must(err)
		keys.ecKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		must(err)
		keys.otherEC, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		must(err)
		keys.edPub, keys.edKey, err = ed25519.GenerateKey(rand.Reader)
		must(err)
	})
}

// Provider is the fake. Zero knobs mean a well-behaved provider.
type Provider struct {
	// Identity and client.
	ClientID, ClientSecret string // defaults "tendshelf" and "" (a public client)
	Sub, Email             string
	EmailVerified          bool
	Name, PreferredName    string
	EmailInUserinfo        bool // email, email_verified and names only in userinfo (Authelia 4.39)
	NoUserinfo             bool

	// How the ID token is made.
	Alg           string // RS256 (default), PS256, ES256, EdDSA, HS256, none
	Kid           string // "" picks the key's own kid
	IssClaim      string // "" is the issuer
	Aud           []string
	Azp           string
	NonceOverride string
	ExpIn         time.Duration // 0 is +5 minutes; a negative value is already expired
	IatAgo        time.Duration
	Nbf           time.Duration // when not zero, nbf = now + Nbf
	WrongKey      bool          // sign with a key the key set does not list
	WeakKey       bool          // sign with, and list, a 1024-bit RSA key (kid "weak")
	Crit          bool          // add a crit header
	JWKAlg        string        // alg written on the default key in the key set
	UserinfoSub   string

	// How the provider misbehaves.
	Down              bool          // every endpoint answers 503
	Slow              time.Duration // every endpoint waits this long first
	HugeBody          bool          // every JSON answer is far over any cap
	RedirectDiscovery bool          // discovery answers 302
	DiscoveryIssuer   string        // the issuer the discovery document claims
	TokenURL          string        // token_endpoint override (another origin)
	JWKSURL           string
	SigningAlgs       []string // id_token_signing_alg_values_supported override
	AuthMethods       []string // token_endpoint_auth_methods_supported override
	TokenStatus       int      // token endpoint answers this status instead of working
	Now               func() time.Time

	mu       sync.Mutex
	srv      *httptest.Server
	codes    map[string]*codeRec
	lastAuth url.Values

	DiscoveryHits, JWKSHits, TokenHits, UserinfoHits, AuthorizeHits atomic.Int64
}

type codeRec struct {
	nonce, challenge, redirect, client string
	used                               bool
	at                                 string // access token
}

// New starts the fake and stops it when the test ends.
func New(t testing.TB) *Provider {
	t.Helper()
	loadKeys()
	p := &Provider{ClientID: "tendshelf", Sub: "sub-1", Email: "ana@example.com", EmailVerified: true, Name: "Ana Lopez",
		codes: map[string]*codeRec{}, Now: time.Now}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.wrap(&p.DiscoveryHits, p.discovery))
	mux.HandleFunc("/authorize", p.authorize)
	mux.HandleFunc("/token", p.wrap(&p.TokenHits, p.token))
	mux.HandleFunc("/jwks", p.wrap(&p.JWKSHits, p.jwks))
	mux.HandleFunc("/userinfo", p.wrap(&p.UserinfoHits, p.userinfo))
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// Issuer is the provider's address, without a trailing slash.
func (p *Provider) Issuer() string { return p.srv.URL }

// Update changes knobs while the server runs.
func (p *Provider) Update(fn func(*Provider)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(p)
}

// LastAuthorize returns the query of the latest /authorize request.
func (p *Provider) LastAuthorize() url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastAuth
}

// snapshot copies the knobs under the lock.
func (p *Provider) snapshot() *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	return &Provider{ClientID: p.ClientID, ClientSecret: p.ClientSecret, Sub: p.Sub, Email: p.Email, EmailVerified: p.EmailVerified,
		Name: p.Name, PreferredName: p.PreferredName, EmailInUserinfo: p.EmailInUserinfo, NoUserinfo: p.NoUserinfo, Alg: p.Alg,
		Kid: p.Kid, IssClaim: p.IssClaim, Aud: p.Aud, Azp: p.Azp, NonceOverride: p.NonceOverride, ExpIn: p.ExpIn, IatAgo: p.IatAgo,
		Nbf: p.Nbf, WrongKey: p.WrongKey, WeakKey: p.WeakKey, Crit: p.Crit, JWKAlg: p.JWKAlg, UserinfoSub: p.UserinfoSub,
		Down: p.Down, Slow: p.Slow, HugeBody: p.HugeBody, RedirectDiscovery: p.RedirectDiscovery, DiscoveryIssuer: p.DiscoveryIssuer,
		TokenURL: p.TokenURL, JWKSURL: p.JWKSURL, SigningAlgs: p.SigningAlgs, AuthMethods: p.AuthMethods, TokenStatus: p.TokenStatus, Now: p.Now}
}

// wrap counts a hit and applies Down, Slow and HugeBody.
func (p *Provider) wrap(hits *atomic.Int64, h func(w http.ResponseWriter, r *http.Request, k *Provider)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		k := p.snapshot()
		if k.Slow > 0 {
			select {
			case <-time.After(k.Slow):
			case <-r.Context().Done():
				return
			}
		}
		if k.Down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		if k.HugeBody {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"pad":"%s"}`, strings.Repeat("x", 200<<10))
			return
		}
		h(w, r, k)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (p *Provider) discovery(w http.ResponseWriter, r *http.Request, k *Provider) {
	if k.RedirectDiscovery {
		http.Redirect(w, r, p.srv.URL+"/elsewhere", http.StatusFound)
		return
	}
	iss := p.srv.URL
	if k.DiscoveryIssuer != "" {
		iss = k.DiscoveryIssuer
	}
	tokenURL, jwksURL := iss+"/token", iss+"/jwks"
	if k.DiscoveryIssuer == "" {
		tokenURL, jwksURL = p.srv.URL+"/token", p.srv.URL+"/jwks"
	}
	if k.TokenURL != "" {
		tokenURL = k.TokenURL
	}
	if k.JWKSURL != "" {
		jwksURL = k.JWKSURL
	}
	algs := k.SigningAlgs
	if algs == nil {
		algs = []string{"RS256", "PS256", "ES256", "EdDSA"}
	}
	methods := k.AuthMethods
	if methods == nil {
		methods = []string{"client_secret_basic", "client_secret_post", "none"}
	}
	doc := map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                p.srv.URL + "/authorize",
		"token_endpoint":                        tokenURL,
		"jwks_uri":                              jwksURL,
		"response_types_supported":              []string{"code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"id_token_signing_alg_values_supported": algs,
		"token_endpoint_auth_methods_supported": methods,
	}
	if !k.NoUserinfo {
		doc["userinfo_endpoint"] = p.srv.URL + "/userinfo"
	}
	writeJSON(w, doc)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func padded(n *big.Int, size int) []byte { return n.FillBytes(make([]byte, size)) }

func rsaJWK(kid string, k *rsa.PublicKey, alg string) map[string]any {
	m := map[string]any{"kty": "RSA", "use": "sig", "kid": kid, "n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes())}
	if alg != "" {
		m["alg"] = alg
	}
	return m
}

func (p *Provider) jwks(w http.ResponseWriter, _ *http.Request, k *Provider) {
	set := []map[string]any{
		rsaJWK("rsa1", &keys.rsaKey.PublicKey, k.JWKAlg),
		{"kty": "EC", "use": "sig", "kid": "ec1", "crv": "P-256", "x": b64(padded(keys.ecKey.X, 32)), "y": b64(padded(keys.ecKey.Y, 32))},
		{"kty": "OKP", "use": "sig", "kid": "ed1", "crv": "Ed25519", "x": b64(keys.edPub)},
		// An encryption key that must never be used to verify.
		{"kty": "RSA", "use": "enc", "kid": "enc1", "n": b64(keys.otherRSA.N.Bytes()), "e": "AQAB"},
	}
	if k.WeakKey {
		set = append(set, rsaJWK("weak", &keys.weakRSA.PublicKey, ""))
	}
	writeJSON(w, map[string]any{"keys": set})
}

// authorize records the request and answers like a browser's last hop: 302 to redirect_uri with a code.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	p.AuthorizeHits.Add(1)
	q := r.URL.Query()
	p.mu.Lock()
	p.lastAuth = q
	client := p.ClientID
	p.mu.Unlock()
	if q.Get("response_type") != "code" || q.Get("client_id") != client || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("redirect_uri") == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	code := b64(randBytes(16))
	p.mu.Lock()
	p.codes[code] = &codeRec{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), client: q.Get("client_id")}
	p.mu.Unlock()
	u, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "bad redirect", http.StatusBadRequest)
		return
	}
	rq := u.Query()
	rq.Set("code", code)
	rq.Set("state", q.Get("state"))
	u.RawQuery = rq.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// Authorize drives /authorize the way a browser would and returns the code and state it sends back.
func (p *Provider) Authorize(authURL string) (code, state string) {
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Get(authURL)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusFound {
		panic(fmt.Sprintf("oidctest: /authorize answered %d", resp.StatusCode))
	}
	return loc.Query().Get("code"), loc.Query().Get("state")
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request, k *Provider) {
	if k.TokenStatus != 0 {
		http.Error(w, "no", k.TokenStatus)
		return
	}
	if r.Method != http.MethodPost || r.ParseForm() != nil {
		http.Error(w, "bad", http.StatusBadRequest)
		return
	}
	id, secret, basic := r.BasicAuth()
	if basic {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != k.ClientID || secret != k.ClientSecret {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
		return
	}
	bad := func() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}
	p.mu.Lock()
	rec := p.codes[r.PostForm.Get("code")]
	if rec == nil || rec.used {
		p.mu.Unlock()
		bad()
		return
	}
	rec.used = true
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if r.PostForm.Get("grant_type") != "authorization_code" || b64(sum[:]) != rec.challenge || r.PostForm.Get("redirect_uri") != rec.redirect {
		bad()
		return
	}
	at := "at-" + b64(randBytes(12))
	p.mu.Lock()
	rec.at = at
	p.mu.Unlock()
	idt := p.IDToken(k, rec.nonce)
	writeJSON(w, map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": 60, "id_token": idt})
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request, k *Provider) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	ok := false
	p.mu.Lock()
	for _, c := range p.codes {
		if c.at != "" && c.at == tok {
			ok = true
		}
	}
	p.mu.Unlock()
	if !ok {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	sub := k.Sub
	if k.UserinfoSub != "" {
		sub = k.UserinfoSub
	}
	m := map[string]any{"sub": sub}
	if k.EmailInUserinfo {
		m["email"], m["email_verified"], m["name"], m["preferred_username"] = k.Email, k.EmailVerified, k.Name, k.PreferredName
	}
	writeJSON(w, m)
}

// IDToken makes an ID token as the knobs say. The nonce is the one the sign-in sent.
func (p *Provider) IDToken(k *Provider, nonce string) string {
	now := k.Now()
	if k.NonceOverride != "" {
		nonce = k.NonceOverride
	}
	iss := p.srv.URL
	if k.IssClaim != "" {
		iss = k.IssClaim
	}
	aud := k.Aud
	if aud == nil {
		aud = []string{k.ClientID}
	}
	exp := 5 * time.Minute
	if k.ExpIn != 0 {
		exp = k.ExpIn
	}
	claims := map[string]any{"iss": iss, "sub": k.Sub, "nonce": nonce, "iat": now.Add(-k.IatAgo).Unix(), "exp": now.Add(exp).Unix()}
	if len(aud) == 1 {
		claims["aud"] = aud[0]
	} else {
		claims["aud"] = aud
	}
	if k.Azp != "" {
		claims["azp"] = k.Azp
	}
	if k.Nbf != 0 {
		claims["nbf"] = now.Add(k.Nbf).Unix()
	}
	if !k.EmailInUserinfo {
		claims["email"], claims["email_verified"], claims["name"], claims["preferred_username"] = k.Email, k.EmailVerified, k.Name, k.PreferredName
	}
	return sign(k, claims)
}

func sign(k *Provider, claims map[string]any) string {
	alg := k.Alg
	if alg == "" {
		alg = "RS256"
	}
	kid := k.Kid
	if kid == "" {
		switch {
		case k.WeakKey:
			kid = "weak"
		case strings.HasPrefix(alg, "ES"):
			kid = "ec1"
		case alg == "EdDSA":
			kid = "ed1"
		default:
			kid = "rsa1"
		}
	}
	hdr := map[string]any{"alg": alg, "kid": kid, "typ": "JWT"}
	if alg == "none" || alg == "HS256" {
		delete(hdr, "kid")
	}
	if k.Crit {
		hdr["crit"] = []string{"exp"}
	}
	hb, _ := json.Marshal(hdr)
	cb, _ := json.Marshal(claims)
	input := b64(hb) + "." + b64(cb)
	var sig []byte
	switch alg {
	case "none":
	case "HS256":
		mac := hmac.New(sha256.New, []byte("tendshelf-hs256-secret"))
		mac.Write([]byte(input))
		sig = mac.Sum(nil)
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512":
		priv := keys.rsaKey
		switch {
		case k.WeakKey:
			priv = keys.weakRSA
		case k.WrongKey:
			priv = keys.otherRSA
		}
		h := crypto.SHA256
		sum := sha256.Sum256([]byte(input))
		digest := sum[:]
		if strings.HasSuffix(alg, "384") {
			h = crypto.SHA384
			s := sha512.Sum384([]byte(input))
			digest = s[:]
		} else if strings.HasSuffix(alg, "512") {
			h = crypto.SHA512
			s := sha512.Sum512([]byte(input))
			digest = s[:]
		}
		var err error
		if strings.HasPrefix(alg, "PS") {
			sig, err = rsa.SignPSS(rand.Reader, priv, h, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		} else {
			sig, err = rsa.SignPKCS1v15(rand.Reader, priv, h, digest)
		}
		if err != nil {
			panic(err)
		}
	case "ES256":
		priv := keys.ecKey
		if k.WrongKey {
			priv = keys.otherEC
		}
		sum := sha256.Sum256([]byte(input))
		der, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
		if err != nil {
			panic(err)
		}
		var rs struct{ R, S *big.Int }
		if _, err := asn1.Unmarshal(der, &rs); err != nil {
			panic(err)
		}
		sig = append(padded(rs.R, 32), padded(rs.S, 32)...)
	case "EdDSA":
		priv := keys.edKey
		if k.WrongKey {
			_, priv, _ = ed25519.GenerateKey(rand.Reader)
		}
		sig = ed25519.Sign(priv, []byte(input))
	default:
		panic("oidctest: unknown alg " + alg)
	}
	return input + "." + b64(sig)
}

// Snapshot returns the current knobs, for tests that call IDToken themselves.
func (p *Provider) Snapshot() *Provider { return p.snapshot() }
