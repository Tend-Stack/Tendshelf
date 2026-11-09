package oidc

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
)

// The signing algorithms Tendshelf accepts: asymmetric only. HS256 and none are never accepted.
var allowedAlgs = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}

func algAllowed(alg string) bool { return slices.Contains(allowedAlgs, alg) }

// IssuerMismatchError says the discovery document names another issuer than the configured one.
type IssuerMismatchError struct{ Got string }

func (e *IssuerMismatchError) Error() string { return "oidc: the provider calls itself " + e.Got }

// AlgError says the provider signs ID tokens with no algorithm Tendshelf accepts.
type AlgError struct{ Offered []string }

func (e *AlgError) Error() string {
	return "oidc: the provider signs with " + strings.Join(e.Offered, ", ") + ", which Tendshelf does not accept"
}

// Provider is what discovery found.
type Provider struct {
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	JWKSURI               string
	UserinfoEndpoint      string // "" when the provider lists none
	Algs                  []string
	AuthMethods           []string // token_endpoint_auth_methods_supported; empty when not listed
}

// Origin is the issuer's origin, the only one the Client talks to.
func (p Provider) Origin() string {
	o, _ := originOfURL(p.Issuer)
	return o
}

// Origins lists the issuer's origin and the authorization endpoint's, without repeats. They are the
// origins a browser visits during a sign-in.
func Origins(p Provider) []string {
	var out []string
	for _, raw := range []string{p.Issuer, p.AuthorizationEndpoint} {
		if o, ok := originOfURL(raw); ok && !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	return out
}

type discoveryDoc struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	UserinfoEndpoint      string   `json:"userinfo_endpoint"`
	ResponseTypes         []string `json:"response_types_supported"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	IDTokenAlgs           []string `json:"id_token_signing_alg_values_supported"`
	TokenAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
}

// Discover reads the provider's discovery document and checks it against the rules of decision 0037
// point 3. The answer is cached for an hour per issuer.
func (c *Client) Discover(ctx context.Context, issuer string) (Provider, error) {
	c.mu.Lock()
	e, ok := c.disco[issuer]
	c.mu.Unlock()
	if ok && c.now().Sub(e.at) < cacheTTL {
		return e.p, nil
	}
	origin, ok := originOfURL(issuer)
	if !ok || !ParseIssuer(issuer) {
		return Provider{}, ErrProviderConfig
	}
	raw, err := c.get(ctx, origin, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration", nil, capDiscovery)
	if err != nil {
		return Provider{}, err
	}
	var d discoveryDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return Provider{}, ErrProviderConfig
	}
	if d.Issuer != issuer {
		return Provider{}, &IssuerMismatchError{Got: sanitize(d.Issuer)}
	}
	// The token, JWKS and userinfo endpoints are fetched by the server: same origin as the issuer.
	for _, ep := range []string{d.TokenEndpoint, d.JWKSURI} {
		if o, ok := originOfURL(ep); !ok || o != origin {
			return Provider{}, ErrProviderConfig
		}
	}
	if d.UserinfoEndpoint != "" {
		if o, ok := originOfURL(d.UserinfoEndpoint); !ok || o != origin {
			return Provider{}, ErrProviderConfig
		}
	}
	// The authorization endpoint is only a link for the browser: https (or loopback), anywhere.
	if _, ok := originOfURL(d.AuthorizationEndpoint); !ok {
		return Provider{}, ErrProviderConfig
	}
	if u, err := url.Parse(d.AuthorizationEndpoint); err != nil || u.Fragment != "" {
		return Provider{}, ErrProviderConfig
	}
	if !slices.Contains(d.ResponseTypes, "code") {
		return Provider{}, ErrProviderConfig
	}
	if len(d.CodeChallengeMethods) > 0 && !slices.Contains(d.CodeChallengeMethods, "S256") {
		return Provider{}, ErrProviderConfig
	}
	var algs []string
	for _, a := range d.IDTokenAlgs {
		if algAllowed(a) {
			algs = append(algs, a)
		}
	}
	if len(algs) == 0 {
		return Provider{}, &AlgError{Offered: sanitizeAll(d.IDTokenAlgs)}
	}
	p := Provider{Issuer: d.Issuer, AuthorizationEndpoint: d.AuthorizationEndpoint, TokenEndpoint: d.TokenEndpoint,
		JWKSURI: d.JWKSURI, UserinfoEndpoint: d.UserinfoEndpoint, Algs: algs, AuthMethods: d.TokenAuthMethods}
	c.mu.Lock()
	c.disco[issuer] = discoEntry{p: p, at: c.now()}
	c.mu.Unlock()
	return p, nil
}

// sanitize shortens text from the provider and drops control characters before it can reach a page.
func sanitize(s string) string {
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
}

func sanitizeAll(in []string) []string {
	if len(in) > 12 {
		in = in[:12]
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = sanitize(s)
	}
	if len(out) == 0 {
		out = []string{"nothing"}
	}
	return out
}
