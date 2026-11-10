package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Time rules for an ID token (decision 0037 point 5).
const (
	Leeway = 2 * time.Minute  // clock skew allowed on every time check
	MaxAge = 10 * time.Minute // how old iat may be
)

// TokenError says why an ID token or the exchange was refused. Reason is one of malformed, alg, kid,
// signature, issuer, audience, azp, expired, not_yet, issued_at, nonce, sub, userinfo_sub. It carries
// no token text, so it is safe to audit.
type TokenError struct{ Reason string }

func (e *TokenError) Error() string { return "oidc: ID token refused (" + e.Reason + ")" }

// Claims is what Tendshelf keeps from a sign-in.
type Claims struct {
	Subject           string
	Email             string
	EmailVerified     bool
	Name              string
	PreferredUsername string
}

// RandomToken returns 32 random bytes as 43 base64url characters: a state, a nonce or a PKCE verifier.
func RandomToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("tendshelf: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// Challenge is the S256 PKCE challenge of a verifier.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthorizeURL is the address the browser is sent to.
func AuthorizeURL(p Provider, cfg Config, state, nonce, verifier string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {cfg.RedirectURL()},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {Challenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(p.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return p.AuthorizationEndpoint + sep + q.Encode()
}

// audience is a JWT aud claim: one string or a list.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

type idClaims struct {
	Iss               string       `json:"iss"`
	Sub               string       `json:"sub"`
	Aud               audience     `json:"aud"`
	Azp               string       `json:"azp"`
	Exp               *json.Number `json:"exp"`
	Nbf               *json.Number `json:"nbf"`
	Iat               *json.Number `json:"iat"`
	Nonce             string       `json:"nonce"`
	Email             string       `json:"email"`
	EmailVerified     any          `json:"email_verified"`
	Name              string       `json:"name"`
	PreferredUsername string       `json:"preferred_username"`
}

func secs(n *json.Number) (time.Time, bool) {
	if n == nil {
		return time.Time{}, false
	}
	f, err := n.Float64()
	if err != nil || f < 0 || f > 1e11 {
		return time.Time{}, false
	}
	return time.Unix(int64(f), 0), true
}

func boolClaim(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

// checkClaims applies OIDC Core 3.1.3.7 to a verified payload.
func (c *Client) checkClaims(payload []byte, issuer, clientID, nonce string) (idClaims, error) {
	var cl idClaims
	if err := json.Unmarshal(payload, &cl); err != nil {
		return cl, &TokenError{Reason: "malformed"}
	}
	now := c.now()
	switch {
	case cl.Iss != issuer:
		return cl, &TokenError{Reason: "issuer"}
	case !slices.Contains(cl.Aud, clientID):
		return cl, &TokenError{Reason: "audience"}
	case (len(cl.Aud) > 1 || cl.Azp != "") && cl.Azp != clientID:
		return cl, &TokenError{Reason: "azp"}
	}
	exp, ok := secs(cl.Exp)
	if !ok || !exp.Add(Leeway).After(now) {
		return cl, &TokenError{Reason: "expired"}
	}
	if cl.Nbf != nil {
		nbf, ok := secs(cl.Nbf)
		if !ok || nbf.After(now.Add(Leeway)) {
			return cl, &TokenError{Reason: "not_yet"}
		}
	}
	iat, ok := secs(cl.Iat)
	if !ok || iat.After(now.Add(Leeway)) || now.Sub(iat) > MaxAge {
		return cl, &TokenError{Reason: "issued_at"}
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(cl.Nonce), []byte(nonce)) != 1 {
		return cl, &TokenError{Reason: "nonce"}
	}
	if cl.Sub == "" || len(cl.Sub) > 255 || !utf8.ValidString(cl.Sub) {
		return cl, &TokenError{Reason: "sub"}
	}
	return cl, nil
}

// clientAuth says how the client proves itself at the token endpoint.
func clientAuth(p Provider, cfg Config, form url.Values, header http.Header) {
	if cfg.ClientSecret == "" {
		form.Set("client_id", cfg.ClientID)
		return
	}
	basic := slices.Contains(p.AuthMethods, "client_secret_basic")
	post := slices.Contains(p.AuthMethods, "client_secret_post")
	if post && !basic {
		form.Set("client_id", cfg.ClientID)
		form.Set("client_secret", cfg.ClientSecret)
		return
	}
	// RFC 6749 2.3.1: form-urlencode the id and the secret, then base64 the pair.
	cred := url.QueryEscape(cfg.ClientID) + ":" + url.QueryEscape(cfg.ClientSecret)
	header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cred)))
}

// Exchange trades the authorization code for tokens, checks the ID token and reads the userinfo
// endpoint when the provider lists one. The access token is dropped afterwards. Errors are
// ErrProviderDown, ErrProviderConfig, ErrProviderRefused or *TokenError.
func (c *Client) Exchange(ctx context.Context, p Provider, cfg Config, code, verifier, nonce string) (Claims, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURL()},
		"code_verifier": {verifier},
	}
	header := http.Header{}
	clientAuth(p, cfg, form, header)
	raw, err := c.postForm(ctx, p.Origin(), p.TokenEndpoint, form, header, capToken)
	if err != nil {
		return Claims{}, err
	}
	var tok struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil || tok.IDToken == "" {
		return Claims{}, &TokenError{Reason: "malformed"}
	}
	payload, err := c.verify(ctx, p, tok.IDToken)
	if err != nil {
		return Claims{}, err
	}
	cl, err := c.checkClaims(payload, p.Issuer, cfg.ClientID, nonce)
	if err != nil {
		return Claims{}, err
	}
	out := Claims{Subject: cl.Sub, Email: cl.Email, EmailVerified: boolClaim(cl.EmailVerified),
		Name: cl.Name, PreferredUsername: cl.PreferredUsername}
	if p.UserinfoEndpoint == "" || tok.AccessToken == "" {
		return clampClaims(out), nil
	}
	// A userinfo answer that cannot be had only means fewer claims; one that names another person fails.
	raw, err = c.get(ctx, p.Origin(), p.UserinfoEndpoint, http.Header{"Authorization": {"Bearer " + tok.AccessToken}}, capUserinfo)
	if err != nil {
		return clampClaims(out), nil
	}
	var ui idClaims
	if err := json.Unmarshal(raw, &ui); err != nil {
		return clampClaims(out), nil
	}
	if ui.Sub != cl.Sub {
		return Claims{}, &TokenError{Reason: "userinfo_sub"}
	}
	if ui.Email != "" {
		out.Email, out.EmailVerified = ui.Email, boolClaim(ui.EmailVerified)
	}
	if ui.Name != "" {
		out.Name = ui.Name
	}
	if ui.PreferredUsername != "" {
		out.PreferredUsername = ui.PreferredUsername
	}
	return clampClaims(out), nil
}

// clampClaims bounds the text a provider can make us store or show.
func clampClaims(c Claims) Claims {
	if len(c.Email) > 254 || !utf8.ValidString(c.Email) {
		c.Email, c.EmailVerified = "", false
	}
	c.Email = strings.TrimSpace(c.Email)
	c.Name = clampRunes(c.Name, 80)
	c.PreferredUsername = clampRunes(c.PreferredUsername, 80)
	return c
}

func clampRunes(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.ToValidUTF8(strings.TrimSpace(s), ""))
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n])
	}
	return s
}
