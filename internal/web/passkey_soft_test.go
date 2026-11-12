package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// softAuth is a software ES256 authenticator for the handler tests: it builds the bytes a browser
// sends back from navigator.credentials.create and .get. The ceremony rules and the other two
// algorithms are tested in internal/auth/passkey; this one only drives the HTTP routes.
type softAuth struct {
	key    *ecdsa.PrivateKey
	credID []byte
	handle []byte
	count  uint32
}

func newSoftAuth(t testing.TB) *softAuth {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &softAuth{key: k, credID: id}
}

func cborHead(major byte, n int) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 256:
		return []byte{major<<5 | 24, byte(n)}
	}
	return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
}

func cborBytes(b []byte) []byte { return append(cborHead(2, len(b)), b...) }
func cborText(s string) []byte  { return append(cborHead(3, len(s)), s...) }

func (s *softAuth) cose() []byte {
	pt, _ := s.key.PublicKey.Bytes()
	out := []byte{0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21}
	out = append(out, cborBytes(pt[1:33])...)
	out = append(out, 0x22)
	return append(out, cborBytes(pt[33:])...)
}

func softClient(typ string, challenge []byte, origin string) []byte {
	return []byte(fmt.Sprintf(`{"type":%q,"challenge":%q,"origin":%q}`, typ, b64(challenge), origin))
}

func softRP(rp string) []byte { h := sha256.Sum256([]byte(rp)); return h[:] }

// create answers a registration: the client data and attestation object, base64url.
func (s *softAuth) create(challenge []byte, rp, origin string) (clientData, attestation string) {
	ad := append(softRP(rp), 0x01|0x04|0x40)
	ad = binary.BigEndian.AppendUint32(ad, 0)
	ad = append(ad, make([]byte, 16)...)
	ad = binary.BigEndian.AppendUint16(ad, uint16(len(s.credID)))
	ad = append(ad, s.credID...)
	ad = append(ad, s.cose()...)
	obj := []byte{0xa3}
	obj = append(obj, cborText("fmt")...)
	obj = append(obj, cborText("none")...)
	obj = append(obj, cborText("attStmt")...)
	obj = append(obj, 0xa0)
	obj = append(obj, cborText("authData")...)
	obj = append(obj, cborBytes(ad)...)
	return b64(softClient("webauthn.create", challenge, origin)), b64(obj)
}

// get answers a sign-in as the JSON body /login/passkey takes.
func (s *softAuth) get(challenge []byte, rp, origin, next string) map[string]string {
	s.count++
	ad := append(softRP(rp), 0x01|0x04)
	ad = binary.BigEndian.AppendUint32(ad, s.count)
	cd := softClient("webauthn.get", challenge, origin)
	cdh := sha256.Sum256(cd)
	h := sha256.Sum256(append(append([]byte(nil), ad...), cdh[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, s.key, h[:])
	if err != nil {
		panic(err)
	}
	return map[string]string{
		"id": b64(s.credID), "type": "public-key", "clientDataJSON": b64(cd), "authenticatorData": b64(ad),
		"signature": b64(sig), "userHandle": b64(s.handle), "next": next,
	}
}

// jsonPost sends a JSON POST with the CSRF header. host is the Host header ("" keeps the test default).
func (a *testApp) jsonPost(path, token, host string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}
	if host != "" {
		req.Host = host
	}
	for _, c := range cookies {
		if c != nil {
			req.AddCookie(c)
		}
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func jsonOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON (%d): %q", rec.Code, rec.Body.String())
	}
	return m
}

func unb64t(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
