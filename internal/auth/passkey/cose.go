package passkey

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"math/big"
)

// COSE algorithm numbers Tendshelf offers and accepts.
const (
	AlgEdDSA = -8
	AlgES256 = -7
	AlgRS256 = -257
)

// COSE key labels.
const (
	coseKty = 1
	coseAlg = 3
	// Parameters whose meaning depends on the key type: crv or n, x or e, y.
	coseP1 = -1
	coseP2 = -2
	coseP3 = -3
)

// parseCOSE reads a COSE_Key and returns the public key (*ecdsa.PublicKey, *rsa.PublicKey or
// ed25519.PublicKey) and its algorithm. It accepts exactly the three shapes of decision 0041.
func parseCOSE(b []byte) (crypto.PublicKey, int, error) {
	v, rest, err := decodeCBOR(b, 0)
	if err != nil || len(rest) != 0 {
		return nil, 0, ErrKey
	}
	m, ok := v.(cborMap)
	if !ok {
		return nil, 0, ErrKey
	}
	kty, ok1 := m[int64(coseKty)].(int64)
	alg, ok2 := m[int64(coseAlg)].(int64)
	if !ok1 || !ok2 {
		return nil, 0, ErrKey
	}
	switch {
	case kty == 2 && alg == AlgES256:
		crv, _ := m[int64(coseP1)].(int64)
		x, _ := m[int64(coseP2)].([]byte)
		y, _ := m[int64(coseP3)].([]byte)
		if crv != 1 || len(x) != 32 || len(y) != 32 {
			return nil, 0, ErrKey
		}
		point := append([]byte{0x04}, append(append([]byte(nil), x...), y...)...)
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point) // refuses a point off the curve
		if err != nil {
			return nil, 0, ErrKey
		}
		return pub, AlgES256, nil
	case kty == 3 && alg == AlgRS256:
		nb, _ := m[int64(coseP1)].([]byte)
		eb, _ := m[int64(coseP2)].([]byte)
		n := new(big.Int).SetBytes(nb)
		e := new(big.Int).SetBytes(eb)
		if bits := n.BitLen(); bits < 2048 || bits > 4096 || n.Bit(0) == 0 {
			return nil, 0, ErrKey
		}
		if !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 || e.Bit(0) == 0 {
			return nil, 0, ErrKey
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, AlgRS256, nil
	case kty == 1 && alg == AlgEdDSA:
		crv, _ := m[int64(coseP1)].(int64)
		x, _ := m[int64(coseP2)].([]byte)
		if crv != 6 || len(x) != ed25519.PublicKeySize {
			return nil, 0, ErrKey
		}
		return ed25519.PublicKey(append([]byte(nil), x...)), AlgEdDSA, nil
	}
	return nil, 0, ErrAlgorithm
}

// verifySig checks sig over msg with the key parseCOSE returned.
func verifySig(alg int, pub crypto.PublicKey, msg, sig []byte) bool {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if alg != AlgES256 {
			return false
		}
		h := sha256.Sum256(msg)
		return ecdsa.VerifyASN1(k, h[:], sig)
	case *rsa.PublicKey:
		if alg != AlgRS256 {
			return false
		}
		h := sha256.Sum256(msg)
		return rsa.VerifyPKCS1v15(k, crypto.SHA256, h[:], sig) == nil
	case ed25519.PublicKey:
		if alg != AlgEdDSA {
			return false
		}
		return ed25519.Verify(k, msg, sig)
	}
	return false
}
