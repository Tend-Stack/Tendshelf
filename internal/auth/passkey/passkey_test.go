package passkey

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
)

// ---- a test-only CBOR encoder ----

type kv struct{ k, v any }

type rawCBOR []byte

func head(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 1<<8:
		return []byte{major<<5 | 24, byte(n)}
	case n < 1<<16:
		return binary.BigEndian.AppendUint16([]byte{major<<5 | 25}, uint16(n))
	case n < 1<<32:
		return binary.BigEndian.AppendUint32([]byte{major<<5 | 26}, uint32(n))
	}
	return binary.BigEndian.AppendUint64([]byte{major<<5 | 27}, n)
}

func enc(v any) []byte {
	switch x := v.(type) {
	case int:
		if x >= 0 {
			return head(0, uint64(x))
		}
		return head(1, uint64(-1-x))
	case []byte:
		return append(head(2, uint64(len(x))), x...)
	case string:
		return append(head(3, uint64(len(x))), x...)
	case []any:
		out := head(4, uint64(len(x)))
		for _, e := range x {
			out = append(out, enc(e)...)
		}
		return out
	case []kv:
		out := head(5, uint64(len(x)))
		for _, e := range x {
			out = append(out, enc(e.k)...)
			out = append(out, enc(e.v)...)
		}
		return out
	case rawCBOR:
		return x
	}
	panic(fmt.Sprintf("enc: %T", v))
}

// ---- the software authenticator ----

var (
	rsaOnce sync.Once
	rsaKey  *rsa.PrivateKey
)

func rsa2048(t testing.TB) *rsa.PrivateKey {
	rsaOnce.Do(func() {
		var err error
		rsaKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
	})
	return rsaKey
}

type softAuth struct {
	alg    int
	key    crypto.Signer
	credID []byte
	count  uint32
	flags  byte
}

const (
	testRPID   = "books.example"
	testOrigin = "https://books.example"
)

func newSoftAuth(t testing.TB, alg int) *softAuth {
	var key crypto.Signer
	var err error
	switch alg {
	case AlgES256:
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case AlgEdDSA:
		_, key, err = ed25519.GenerateKey(rand.Reader)
	case AlgRS256:
		key = rsa2048(t)
	}
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	rand.Read(id)
	return &softAuth{alg: alg, key: key, credID: id, flags: flagUP | flagUV}
}

func (s *softAuth) cose() []byte {
	switch k := s.key.Public().(type) {
	case *ecdsa.PublicKey:
		pt, _ := k.Bytes()
		return enc([]kv{{1, 2}, {3, AlgES256}, {-1, 1}, {-2, pt[1:33]}, {-3, pt[33:]}})
	case ed25519.PublicKey:
		return enc([]kv{{1, 1}, {3, AlgEdDSA}, {-1, 6}, {-2, []byte(k)}})
	case *rsa.PublicKey:
		return enc([]kv{{1, 3}, {3, AlgRS256}, {-1, k.N.Bytes()}, {-2, big.NewInt(int64(k.E)).Bytes()}})
	}
	panic("key")
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func clientJSON(typ string, challenge []byte, origin string, extra string) []byte {
	return []byte(fmt.Sprintf(`{"type":%q,"challenge":%q,"origin":%q%s}`, typ, b64(challenge), origin, extra))
}

func rpHash(rp string) []byte { h := sha256.Sum256([]byte(rp)); return h[:] }

// regParts are the pieces of a registration; tests change one and rebuild.
type regParts struct {
	clientData []byte
	authData   []byte
	attObj     []byte
	challenge  []byte
	origin     string
	rpID       string
}

func (s *softAuth) register(challenge []byte) regParts {
	return s.registerWith(challenge, s.cose(), s.flags|flagAT)
}

func (s *softAuth) registerWith(challenge []byte, cose []byte, flags byte) regParts {
	ad := append([]byte(nil), rpHash(testRPID)...)
	ad = append(ad, flags)
	ad = binary.BigEndian.AppendUint32(ad, s.count)
	ad = append(ad, make([]byte, 16)...)
	ad = binary.BigEndian.AppendUint16(ad, uint16(len(s.credID)))
	ad = append(ad, s.credID...)
	ad = append(ad, cose...)
	return regParts{
		clientData: clientJSON("webauthn.create", challenge, testOrigin, ""),
		authData:   ad,
		attObj:     enc([]kv{{"fmt", "none"}, {"attStmt", []kv{}}, {"authData", ad}}),
		challenge:  challenge, origin: testOrigin, rpID: testRPID,
	}
}

func (p regParts) input() RegistrationInput {
	return RegistrationInput{ClientDataJSON: p.clientData, AttestationObject: p.attObj, Challenge: p.challenge,
		Origin: p.origin, RPID: p.rpID, Transports: []string{"internal", "bogus", "internal"}}
}

type getParts struct {
	clientData, authData, sig, challenge []byte
	origin, rpID                         string
}

func (s *softAuth) assert(challenge []byte) getParts {
	return s.assertWith(challenge, s.flags, s.count)
}

func (s *softAuth) assertWith(challenge []byte, flags byte, count uint32) getParts {
	ad := append([]byte(nil), rpHash(testRPID)...)
	ad = append(ad, flags)
	ad = binary.BigEndian.AppendUint32(ad, count)
	cd := clientJSON("webauthn.get", challenge, testOrigin, "")
	return getParts{clientData: cd, authData: ad, sig: s.sign(append(append([]byte(nil), ad...), sum(cd)...)),
		challenge: challenge, origin: testOrigin, rpID: testRPID}
}

func sum(b []byte) []byte { h := sha256.Sum256(b); return h[:] }

func (s *softAuth) sign(msg []byte) []byte {
	var sig []byte
	var err error
	switch s.alg {
	case AlgES256:
		sig, err = s.key.Sign(rand.Reader, sum(msg), crypto.SHA256)
	case AlgRS256:
		sig, err = s.key.Sign(rand.Reader, sum(msg), crypto.SHA256)
	case AlgEdDSA:
		sig, err = s.key.Sign(rand.Reader, msg, crypto.Hash(0))
	}
	if err != nil {
		panic(err)
	}
	return sig
}

func (p getParts) input() AssertionInput {
	return AssertionInput{ClientDataJSON: p.clientData, AuthenticatorData: p.authData, Signature: p.sig,
		Challenge: p.challenge, Origin: p.origin, RPID: p.rpID}
}

var algs = []struct {
	name string
	alg  int
}{{"EdDSA", AlgEdDSA}, {"ES256", AlgES256}, {"RS256", AlgRS256}}

func TestRegisterEachAlgorithm(t *testing.T) {
	for _, a := range algs {
		t.Run(a.name, func(t *testing.T) {
			s := newSoftAuth(t, a.alg)
			s.flags |= flagBE | flagBS
			s.count = 7
			cred, err := VerifyRegistration(s.register(NewChallenge()).input())
			if err != nil {
				t.Fatal(err)
			}
			if cred.Alg != a.alg || !bytes.Equal(cred.ID, s.credID) || !bytes.Equal(cred.PublicKey, s.cose()) {
				t.Errorf("credential = %+v", cred)
			}
			if !cred.BackupEligible || !cred.BackedUp || cred.SignCount != 7 || len(cred.AAGUID) != 16 {
				t.Errorf("flags/counter = %+v", cred)
			}
			if fmt.Sprint(cred.Transports) != "[internal]" {
				t.Errorf("transports = %v", cred.Transports)
			}
		})
	}
}

func TestRegisterRefusals(t *testing.T) {
	ch := NewChallenge()
	es := newSoftAuth(t, AlgES256)
	// A point that is not on the curve.
	offCurve := enc([]kv{{1, 2}, {3, AlgES256}, {-1, 1}, {-2, bytes.Repeat([]byte{1}, 32)}, {-3, bytes.Repeat([]byte{2}, 32)}})
	rsa1024, _ := rsa.GenerateKey(rand.Reader, 1024)
	small := enc([]kv{{1, 3}, {3, AlgRS256}, {-1, rsa1024.N.Bytes()}, {-2, big.NewInt(65537).Bytes()}})
	unknownAlg := enc([]kv{{1, 2}, {3, -36}, {-1, 1}, {-2, bytes.Repeat([]byte{1}, 32)}, {-3, bytes.Repeat([]byte{2}, 32)}})
	rebuild := func(p regParts, ad []byte) regParts {
		p.authData = ad
		p.attObj = enc([]kv{{"fmt", "none"}, {"attStmt", []kv{}}, {"authData", ad}})
		return p
	}
	nest := func(n int) rawCBOR {
		b := bytes.Repeat([]byte{0x81}, n)
		return append(b, 0x00)
	}
	tests := []struct {
		name string
		make func() regParts
		want error
	}{
		{"wrong type", func() regParts {
			p := es.register(ch)
			p.clientData = clientJSON("webauthn.get", ch, testOrigin, "")
			return p
		}, ErrType},
		{"wrong challenge", func() regParts { p := es.register(ch); p.challenge = NewChallenge(); return p }, ErrChallenge},
		{"origin host", func() regParts { p := es.register(ch); p.origin = "https://other.example"; return p }, ErrOrigin},
		{"origin port", func() regParts { p := es.register(ch); p.origin = testOrigin + ":8443"; return p }, ErrOrigin},
		{"origin scheme", func() regParts { p := es.register(ch); p.origin = "http://books.example"; return p }, ErrOrigin},
		{"crossOrigin true", func() regParts {
			p := es.register(ch)
			p.clientData = clientJSON("webauthn.create", ch, testOrigin, `,"crossOrigin":true`)
			return p
		}, ErrCrossOrigin},
		{"client data not JSON", func() regParts { p := es.register(ch); p.clientData = []byte("{"); return p }, ErrMalformed},
		{"client data too big", func() regParts {
			p := es.register(ch)
			p.clientData = clientJSON("webauthn.create", ch, testOrigin, `,"x":"`+string(bytes.Repeat([]byte{'a'}, 5000))+`"`)
			return p
		}, ErrMalformed},
		{"rpIdHash", func() regParts { p := es.register(ch); p.rpID = "evil.example"; return p }, ErrRPID},
		{"no UP", func() regParts { return es.registerWith(ch, es.cose(), flagUV|flagAT) }, ErrPresence},
		{"no UV", func() regParts { return es.registerWith(ch, es.cose(), flagUP|flagAT) }, ErrVerification},
		{"BS without BE", func() regParts { return es.registerWith(ch, es.cose(), flagUP|flagUV|flagAT|flagBS) }, ErrBackupState},
		{"no AT", func() regParts { return es.registerWith(ch, es.cose(), flagUP|flagUV) }, ErrMalformed},
		{"unknown algorithm", func() regParts { return es.registerWith(ch, unknownAlg, flagUP|flagUV|flagAT) }, ErrAlgorithm},
		{"off-curve point", func() regParts { return es.registerWith(ch, offCurve, flagUP|flagUV|flagAT) }, ErrKey},
		{"RSA 1024", func() regParts { return es.registerWith(ch, small, flagUP|flagUV|flagAT) }, ErrKey},
		{"trailing bytes in authData", func() regParts {
			p := es.register(ch)
			return rebuild(p, append(append([]byte(nil), p.authData...), 0))
		}, ErrMalformed},
		{"trailing bytes in object", func() regParts {
			p := es.register(ch)
			p.attObj = append(p.attObj, 0)
			return p
		}, ErrMalformed},
		{"duplicate map key", func() regParts {
			p := es.register(ch)
			p.attObj = enc([]kv{{"fmt", "none"}, {"fmt", "none"}, {"authData", p.authData}})
			return p
		}, ErrMalformed},
		{"tag", func() regParts {
			p := es.register(ch)
			p.attObj = enc([]kv{{"fmt", "none"}, {"attStmt", []kv{}}, {"authData", rawCBOR(append([]byte{0xc1}, enc(p.authData)...))}})
			return p
		}, ErrMalformed},
		{"indefinite length", func() regParts {
			p := es.register(ch)
			p.attObj = enc([]kv{{"fmt", "none"}, {"attStmt", rawCBOR{0xbf, 0xff}}, {"authData", p.authData}})
			return p
		}, ErrMalformed},
		{"depth 9", func() regParts {
			p := es.register(ch)
			p.attObj = enc([]kv{{"fmt", "none"}, {"attStmt", []kv{{"a", nest(8)}}}, {"authData", p.authData}})
			return p
		}, ErrMalformed},
		{"missing attStmt", func() regParts {
			p := es.register(ch)
			p.attObj = enc([]kv{{"fmt", "none"}, {"authData", p.authData}})
			return p
		}, ErrMalformed},
		{"credential id too short", func() regParts {
			s := *es
			s.credID = make([]byte, 15)
			return s.register(ch)
		}, ErrMalformed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyRegistration(tc.make().input())
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// Depth 8 is the limit and still decodes.
	p := es.register(ch)
	p.attObj = enc([]kv{{"fmt", "none"}, {"attStmt", []kv{{"a", rawCBOR(append(bytes.Repeat([]byte{0x81}, 6), 0x00))}}}, {"authData", p.authData}})
	if _, err := VerifyRegistration(p.input()); err != nil {
		t.Errorf("a statement nested 7 deep: %v", err)
	}
}

func TestAssertEachAlgorithm(t *testing.T) {
	for _, a := range algs {
		t.Run(a.name, func(t *testing.T) {
			s := newSoftAuth(t, a.alg)
			cred, err := VerifyRegistration(s.register(NewChallenge()).input())
			if err != nil {
				t.Fatal(err)
			}
			s.count = 1
			got, err := VerifyAssertion(s.assert(NewChallenge()).input(), cred)
			if err != nil {
				t.Fatal(err)
			}
			if got.SignCount != 1 || got.BackedUp {
				t.Errorf("assertion = %+v", got)
			}
		})
	}
}

func TestAssertRefusals(t *testing.T) {
	ch := NewChallenge()
	s := newSoftAuth(t, AlgES256)
	cred, err := VerifyRegistration(s.register(NewChallenge()).input())
	if err != nil {
		t.Fatal(err)
	}
	s.count = 1
	other := newSoftAuth(t, AlgES256)
	tests := []struct {
		name string
		make func() getParts
		want error
	}{
		{"wrong type", func() getParts {
			p := s.assert(ch)
			p.clientData = clientJSON("webauthn.create", ch, testOrigin, "")
			return p
		}, ErrType},
		{"wrong challenge", func() getParts { p := s.assert(ch); p.challenge = NewChallenge(); return p }, ErrChallenge},
		{"origin port", func() getParts { p := s.assert(ch); p.origin = testOrigin + ":8443"; return p }, ErrOrigin},
		{"origin scheme", func() getParts { p := s.assert(ch); p.origin = "http://books.example"; return p }, ErrOrigin},
		{"crossOrigin true", func() getParts {
			p := s.assert(ch)
			p.clientData = clientJSON("webauthn.get", ch, testOrigin, `,"crossOrigin":true`)
			return p
		}, ErrCrossOrigin},
		{"rpIdHash", func() getParts { p := s.assert(ch); p.rpID = "evil.example"; return p }, ErrRPID},
		{"no UP", func() getParts { return s.assertWith(ch, flagUV, 1) }, ErrPresence},
		{"no UV", func() getParts { return s.assertWith(ch, flagUP, 1) }, ErrVerification},
		{"BS without BE", func() getParts { return s.assertWith(ch, flagUP|flagUV|flagBS, 1) }, ErrBackupState},
		{"BE changed", func() getParts { return s.assertWith(ch, flagUP|flagUV|flagBE, 1) }, ErrBackupState},
		{"AT set", func() getParts { return s.assertWith(ch, flagUP|flagUV|flagAT, 1) }, ErrMalformed},
		{"trailing bytes", func() getParts { p := s.assert(ch); p.authData = append(p.authData, 0); return p }, ErrMalformed},
		{"short authData", func() getParts { p := s.assert(ch); p.authData = p.authData[:36]; return p }, ErrMalformed},
		{"bad signature", func() getParts { p := s.assert(ch); p.sig[len(p.sig)-1] ^= 1; return p }, ErrSignature},
		{"signature of another key", func() getParts { p := s.assert(ch); p.sig = other.assert(ch).sig; return p }, ErrSignature},
		{"signed data changed", func() getParts {
			p := s.assert(ch)
			p.authData[36] ^= 1 // the counter, after signing
			return p
		}, ErrSignature},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyAssertion(tc.make().input(), cred)
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCounter(t *testing.T) {
	tests := []struct {
		stored, got uint32
		ok          bool
	}{{0, 0, true}, {5, 6, true}, {6, 6, false}, {6, 2, false}, {0, 3, true}, {3, 0, false}}
	s := newSoftAuth(t, AlgEdDSA)
	cred, err := VerifyRegistration(s.register(NewChallenge()).input())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d_to_%d", tc.stored, tc.got), func(t *testing.T) {
			c := cred
			c.SignCount = tc.stored
			_, err := VerifyAssertion(s.assertWith(NewChallenge(), flagUP|flagUV, tc.got).input(), c)
			if tc.ok && err != nil {
				t.Errorf("refused: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrCounter) {
				t.Errorf("err = %v, want ErrCounter", err)
			}
		})
	}
	// A count that did not grow with a bad signature is a signature failure, not a copy.
	p := s.assertWith(NewChallenge(), flagUP|flagUV, 2)
	p.sig[0] ^= 1
	c := cred
	c.SignCount = 6
	if _, err := VerifyAssertion(p.input(), c); !errors.Is(err, ErrSignature) {
		t.Errorf("forged stale count: %v, want ErrSignature", err)
	}
}

func TestChallengeOf(t *testing.T) {
	ch := NewChallenge()
	got, err := ChallengeOf(clientJSON("webauthn.get", ch, testOrigin, ""))
	if err != nil || !bytes.Equal(got, ch) {
		t.Errorf("ChallengeOf = %x, %v", got, err)
	}
	for _, bad := range []string{``, `{`, `{"challenge":""}`, `{"challenge":"***"}`, `{"challenge":5}`} {
		if _, err := ChallengeOf([]byte(bad)); err == nil {
			t.Errorf("ChallengeOf(%q) accepted", bad)
		}
	}
}

func TestDecodeCBORShapes(t *testing.T) {
	v, rest, err := decodeCBOR(enc([]kv{{"a", []any{1, -3, []byte{9}}}, {5, "x"}}), 0)
	if err != nil || len(rest) != 0 {
		t.Fatal(err)
	}
	m := v.(cborMap)
	if fmt.Sprint(m["a"]) != "[1 -3 [9]]" || m[int64(5)] != "x" {
		t.Errorf("decoded = %v", m)
	}
	for name, b := range map[string][]byte{
		"float":         {0xf9, 0x3c, 0x00},
		"undefined":     {0xf7},
		"length past":   {0x45, 1, 2},
		"huge array":    {0x9b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		"bad utf8":      {0x61, 0xff},
		"array key":     {0xa1, 0x80, 0x00},
		"too big uint":  {0x1b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		"reserved info": {0x1c},
		"empty":         {},
	} {
		if _, _, err := decodeCBOR(b, 0); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func FuzzDecodeCBOR(f *testing.F) {
	f.Add(enc([]kv{{"fmt", "none"}, {"attStmt", []kv{}}, {"authData", []byte{1, 2, 3}}}))
	f.Add(enc([]kv{{1, 2}, {3, -7}, {-1, 1}, {-2, make([]byte, 32)}}))
	f.Add([]byte{0xbf, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, rest, err := decodeCBOR(b, 0)
		if err == nil && len(rest) > len(b) {
			t.Fatal("rest longer than input")
		}
		_, _ = parseAuthData(b)
		_, _, _ = parseCOSE(b)
	})
}

func FuzzVerifyRegistration(f *testing.F) {
	s := newSoftAuth(f, AlgES256)
	ch := bytes.Repeat([]byte{7}, 32)
	p := s.register(ch)
	f.Add(p.clientData, p.attObj)
	f.Add([]byte(`{}`), []byte{})
	f.Fuzz(func(t *testing.T, cd, obj []byte) {
		_, _ = VerifyRegistration(RegistrationInput{ClientDataJSON: cd, AttestationObject: obj, Challenge: ch, Origin: testOrigin, RPID: testRPID})
		_, _ = VerifyAssertion(AssertionInput{ClientDataJSON: cd, AuthenticatorData: obj, Signature: obj, Challenge: ch, Origin: testOrigin, RPID: testRPID}, Credential{PublicKey: obj, Alg: AlgES256})
		_, _ = ChallengeOf(cd)
	})
}
