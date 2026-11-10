package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"
)

const (
	maxJWTBytes = 16 << 10
	minRSABits  = 2048
)

// jwk is one usable public key from the provider's key set.
type jwk struct {
	kid string
	alg string // the key's own alg, "" when it names none
	key any    // *rsa.PublicKey, *ecdsa.PublicKey or ed25519.PublicKey
}

type jwksEntry struct {
	keys []jwk
	at   time.Time
}

type rawJWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func b64(s string) ([]byte, bool) {
	if s == "" || strings.ContainsRune(s, '=') {
		return nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	return b, err == nil
}

// parseJWKS reads a key set and keeps the keys Tendshelf may verify with: RSA of at least 2048 bits,
// EC on P-256, P-384 or P-521, and Ed25519, each for signing (use absent or "sig"). Others are skipped.
func parseJWKS(raw []byte) ([]jwk, error) {
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, ErrProviderConfig
	}
	var out []jwk
	for _, one := range set.Keys {
		var r rawJWK
		if json.Unmarshal(one, &r) != nil || (r.Use != "" && r.Use != "sig") {
			continue
		}
		var key any
		switch r.Kty {
		case "RSA":
			n, ok1 := b64(r.N)
			e, ok2 := b64(r.E)
			if !ok1 || !ok2 || len(e) > 4 {
				continue
			}
			ev := new(big.Int).SetBytes(e)
			nv := new(big.Int).SetBytes(n)
			if nv.BitLen() < minRSABits || !ev.IsInt64() || ev.Int64() < 3 || ev.Int64()%2 == 0 {
				continue
			}
			key = &rsa.PublicKey{N: nv, E: int(ev.Int64())}
		case "EC":
			var curve elliptic.Curve
			switch r.Crv {
			case "P-256":
				curve = elliptic.P256()
			case "P-384":
				curve = elliptic.P384()
			case "P-521":
				curve = elliptic.P521()
			default:
				continue
			}
			x, ok1 := b64(r.X)
			y, ok2 := b64(r.Y)
			size := (curve.Params().BitSize + 7) / 8
			if !ok1 || !ok2 || len(x) > size || len(y) > size {
				continue
			}
			point := make([]byte, 1+2*size)
			point[0] = 4
			copy(point[1+size-len(x):], x)
			copy(point[1+2*size-len(y):], y)
			pub, err := ecdsa.ParseUncompressedPublicKey(curve, point)
			if err != nil {
				continue
			}
			key = pub
		case "OKP":
			x, ok := b64(r.X)
			if r.Crv != "Ed25519" || !ok || len(x) != ed25519.PublicKeySize {
				continue
			}
			key = ed25519.PublicKey(x)
		default:
			continue
		}
		out = append(out, jwk{kid: r.Kid, alg: r.Alg, key: key})
	}
	return out, nil
}

// jwtHeader is the part of a JWS header Tendshelf reads.
type jwtHeader struct {
	Alg  string          `json:"alg"`
	Kid  string          `json:"kid"`
	Crit json.RawMessage `json:"crit"`
}

// algHash is the digest an alg signs over.
func algHash(alg string) (crypto.Hash, bool) {
	switch alg {
	case "RS256", "PS256", "ES256":
		return crypto.SHA256, true
	case "RS384", "PS384", "ES384":
		return crypto.SHA384, true
	case "RS512", "PS512", "ES512":
		return crypto.SHA512, true
	}
	return 0, false
}

func digest(h crypto.Hash, msg []byte) []byte {
	switch h {
	case crypto.SHA384:
		s := sha512.Sum384(msg)
		return s[:]
	case crypto.SHA512:
		s := sha512.Sum512(msg)
		return s[:]
	}
	s := sha256.Sum256(msg)
	return s[:]
}

// keyFits reports whether k can verify alg.
func keyFits(alg string, k any) bool {
	switch pub := k.(type) {
	case *rsa.PublicKey:
		return strings.HasPrefix(alg, "RS") || strings.HasPrefix(alg, "PS")
	case *ecdsa.PublicKey:
		switch alg {
		case "ES256":
			return pub.Curve == elliptic.P256()
		case "ES384":
			return pub.Curve == elliptic.P384()
		case "ES512":
			return pub.Curve == elliptic.P521()
		}
	case ed25519.PublicKey:
		return alg == "EdDSA"
	}
	return false
}

func checkSignature(alg string, k any, signingInput, sig []byte) bool {
	if alg == "EdDSA" {
		pub, ok := k.(ed25519.PublicKey)
		return ok && ed25519.Verify(pub, signingInput, sig)
	}
	h, ok := algHash(alg)
	if !ok {
		return false
	}
	sum := digest(h, signingInput)
	switch pub := k.(type) {
	case *rsa.PublicKey:
		if strings.HasPrefix(alg, "PS") {
			return rsa.VerifyPSS(pub, h, sum, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: h}) == nil
		}
		return rsa.VerifyPKCS1v15(pub, h, sum, sig) == nil
	case *ecdsa.PublicKey:
		size := (pub.Curve.Params().BitSize + 7) / 8
		if len(sig) != 2*size {
			return false
		}
		der, err := asn1.Marshal(struct{ R, S *big.Int }{new(big.Int).SetBytes(sig[:size]), new(big.Int).SetBytes(sig[size:])})
		return err == nil && ecdsa.VerifyASN1(pub, sum, der)
	}
	return false
}

// verifyJWS checks a compact JWS against keys and returns its payload. Every refusal is a
// *TokenError whose Reason is malformed, alg, kid or signature.
func verifyJWS(raw string, keys []jwk) ([]byte, error) {
	if len(raw) == 0 || len(raw) > maxJWTBytes {
		return nil, &TokenError{Reason: "malformed"}
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, &TokenError{Reason: "malformed"}
	}
	hdrRaw, ok1 := b64(parts[0])
	payload, ok2 := b64(parts[1])
	sig, _ := b64(parts[2]) // checked after the alg: "none" has an empty signature
	if !ok1 || !ok2 {
		return nil, &TokenError{Reason: "malformed"}
	}
	var h jwtHeader
	if err := json.Unmarshal(hdrRaw, &h); err != nil || h.Crit != nil {
		return nil, &TokenError{Reason: "malformed"}
	}
	if !algAllowed(h.Alg) {
		return nil, &TokenError{Reason: "alg"}
	}
	var key *jwk
	if h.Kid != "" {
		for i := range keys {
			if keys[i].kid == h.Kid {
				key = &keys[i]
				break
			}
		}
	} else {
		n := 0
		for i := range keys {
			if keyFits(h.Alg, keys[i].key) {
				key = &keys[i]
				n++
			}
		}
		if n != 1 {
			key = nil
		}
	}
	if key == nil {
		return nil, &TokenError{Reason: "kid"}
	}
	if (key.alg != "" && key.alg != h.Alg) || !keyFits(h.Alg, key.key) {
		return nil, &TokenError{Reason: "alg"}
	}
	if !checkSignature(h.Alg, key.key, []byte(parts[0]+"."+parts[1]), sig) {
		return nil, &TokenError{Reason: "signature"}
	}
	return payload, nil
}

// keys returns the provider's key set. A cached set under an hour old is used; with refetch true a
// set at least five minutes old is fetched again (an unknown kid asks for that).
func (c *Client) keys(ctx context.Context, p Provider, refetch bool) ([]jwk, bool, error) {
	c.mu.Lock()
	e := c.jwks[p.Issuer]
	c.mu.Unlock()
	age := time.Duration(0)
	if e != nil {
		age = c.now().Sub(e.at)
	}
	if e != nil && age < cacheTTL && !(refetch && age >= jwksRefetch) {
		return e.keys, false, nil
	}
	raw, err := c.get(ctx, p.Origin(), p.JWKSURI, nil, capJWKS)
	if err != nil {
		return nil, false, err
	}
	ks, err := parseJWKS(raw)
	if err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	c.jwks[p.Issuer] = &jwksEntry{keys: ks, at: c.now()}
	c.mu.Unlock()
	return ks, true, nil
}

// verify checks raw against the provider's keys, fetching them once more when the kid is unknown.
func (c *Client) verify(ctx context.Context, p Provider, raw string) ([]byte, error) {
	ks, fetched, err := c.keys(ctx, p, false)
	if err != nil {
		return nil, err
	}
	payload, err := verifyJWS(raw, ks)
	var te *TokenError
	if errors.As(err, &te) && te.Reason == "kid" && !fetched {
		ks, fetched, ferr := c.keys(ctx, p, true)
		if ferr != nil {
			return nil, ferr
		}
		if fetched {
			return verifyJWS(raw, ks)
		}
	}
	return payload, err
}
