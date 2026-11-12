package passkey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

// Errors. Each is also the reason the sign-in service writes to the audit log.
var (
	ErrMalformed    = errors.New("passkey: malformed")
	ErrType         = errors.New("passkey: wrong ceremony type")
	ErrChallenge    = errors.New("passkey: challenge mismatch")
	ErrOrigin       = errors.New("passkey: origin mismatch")
	ErrCrossOrigin  = errors.New("passkey: cross-origin ceremony")
	ErrRPID         = errors.New("passkey: relying party mismatch")
	ErrPresence     = errors.New("passkey: user presence missing")
	ErrVerification = errors.New("passkey: user verification missing")
	ErrBackupState  = errors.New("passkey: backup state not allowed")
	ErrAlgorithm    = errors.New("passkey: algorithm not accepted")
	ErrKey          = errors.New("passkey: bad public key")
	ErrSignature    = errors.New("passkey: bad signature")
	ErrCounter      = errors.New("passkey: counter did not grow")
)

const (
	maxClientData = 4 << 10
	minCredID     = 16
)

// RegistrationInput is what the browser returned from navigator.credentials.create, plus what this
// server issued: the challenge, the origin of the request and the relying-party id.
type RegistrationInput struct {
	ClientDataJSON, AttestationObject, Challenge []byte
	Origin, RPID                                 string
	Transports                                   []string
}

// Credential is what is stored for a passkey. PublicKey is the COSE key exactly as the
// authenticator sent it.
type Credential struct {
	ID, PublicKey            []byte
	Alg                      int
	SignCount                uint32
	BackupEligible, BackedUp bool
	AAGUID                   []byte
	Transports               []string
}

// AssertionInput is what the browser returned from navigator.credentials.get.
type AssertionInput struct {
	ClientDataJSON, AuthenticatorData, Signature, Challenge []byte
	Origin, RPID                                            string
}

// Assertion is the new state to store after a good sign-in.
type Assertion struct {
	SignCount uint32
	BackedUp  bool
}

// NewChallenge returns 32 random bytes.
func NewChallenge() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("passkey: no randomness: " + err.Error())
	}
	return b
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin *bool  `json:"crossOrigin"`
}

func parseClientData(raw []byte) (clientData, error) {
	var cd clientData
	if len(raw) == 0 || len(raw) > maxClientData || !utf8.Valid(raw) {
		return cd, ErrMalformed
	}
	if err := json.Unmarshal(raw, &cd); err != nil {
		return cd, ErrMalformed
	}
	return cd, nil
}

// ChallengeOf returns the challenge named in the client data, for finding the ceremony row.
// It checks nothing else.
func ChallengeOf(clientDataJSON []byte) ([]byte, error) {
	cd, err := parseClientData(clientDataJSON)
	if err != nil {
		return nil, err
	}
	ch, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	if err != nil || len(ch) == 0 {
		return nil, ErrMalformed
	}
	return ch, nil
}

// checkClientData runs the client-data steps shared by both ceremonies.
func checkClientData(raw []byte, typ string, challenge []byte, origin string) error {
	cd, err := parseClientData(raw)
	if err != nil {
		return err
	}
	if cd.Type != typ {
		return ErrType
	}
	got, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	if err != nil || subtle.ConstantTimeCompare(got, challenge) != 1 || len(challenge) == 0 {
		return ErrChallenge
	}
	if cd.Origin != origin || origin == "" {
		return ErrOrigin
	}
	if cd.CrossOrigin != nil && *cd.CrossOrigin {
		return ErrCrossOrigin
	}
	return nil
}

// checkFlags runs the rpIdHash and flag steps shared by both ceremonies.
func checkFlags(a authData, rpID string) error {
	want := sha256.Sum256([]byte(rpID))
	if subtle.ConstantTimeCompare(a.RPIDHash[:], want[:]) != 1 {
		return ErrRPID
	}
	if !a.has(flagUP) {
		return ErrPresence
	}
	if !a.has(flagUV) {
		return ErrVerification
	}
	if a.has(flagBS) && !a.has(flagBE) {
		return ErrBackupState
	}
	return nil
}

var allowedTransports = map[string]bool{"usb": true, "nfc": true, "ble": true, "internal": true, "hybrid": true, "smart-card": true}

// cleanTransports keeps the known transports, once each, in the order given.
func cleanTransports(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range in {
		if allowedTransports[t] && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// VerifyRegistration runs WebAuthn Level 2 section 7.1 with attestation "none": the statement is
// read for well-formedness only and never trusted.
func VerifyRegistration(in RegistrationInput) (Credential, error) {
	if err := checkClientData(in.ClientDataJSON, "webauthn.create", in.Challenge, in.Origin); err != nil {
		return Credential{}, err
	}
	v, rest, err := decodeCBOR(in.AttestationObject, 0)
	if err != nil || len(rest) != 0 {
		return Credential{}, ErrMalformed
	}
	m, ok := v.(cborMap)
	if !ok || len(m) != 3 {
		return Credential{}, ErrMalformed
	}
	_, fmtOK := m["fmt"].(string)
	_, stmtOK := m["attStmt"].(cborMap)
	raw, dataOK := m["authData"].([]byte)
	if !fmtOK || !stmtOK || !dataOK {
		return Credential{}, ErrMalformed
	}
	a, err := parseAuthData(raw)
	if err != nil {
		return Credential{}, err
	}
	if err := checkFlags(a, in.RPID); err != nil {
		return Credential{}, err
	}
	if !a.has(flagAT) {
		return Credential{}, ErrMalformed
	}
	if _, _, err := parseCOSE(a.COSEKey); err != nil {
		return Credential{}, err
	}
	_, alg, _ := parseCOSE(a.COSEKey)
	if len(a.CredentialID) < minCredID {
		return Credential{}, ErrMalformed
	}
	return Credential{
		ID: a.CredentialID, PublicKey: a.COSEKey, Alg: alg, SignCount: a.SignCount,
		BackupEligible: a.has(flagBE), BackedUp: a.has(flagBS), AAGUID: a.AAGUID,
		Transports: cleanTransports(in.Transports),
	}, nil
}

// VerifyAssertion runs WebAuthn Level 2 section 7.2 against the stored credential. The counter
// rule is checked last, so ErrCounter means the signature was good: the key signed with a count
// that did not grow.
func VerifyAssertion(in AssertionInput, cred Credential) (Assertion, error) {
	if err := checkClientData(in.ClientDataJSON, "webauthn.get", in.Challenge, in.Origin); err != nil {
		return Assertion{}, err
	}
	a, err := parseAuthData(in.AuthenticatorData)
	if err != nil {
		return Assertion{}, err
	}
	if a.has(flagAT) {
		return Assertion{}, ErrMalformed
	}
	if err := checkFlags(a, in.RPID); err != nil {
		return Assertion{}, err
	}
	if a.has(flagBE) != cred.BackupEligible {
		return Assertion{}, ErrBackupState
	}
	pub, alg, err := parseCOSE(cred.PublicKey)
	if err != nil {
		return Assertion{}, err
	}
	if alg != cred.Alg {
		return Assertion{}, ErrAlgorithm
	}
	hash := sha256.Sum256(in.ClientDataJSON)
	msg := append(append([]byte(nil), in.AuthenticatorData...), hash[:]...)
	if !verifySig(alg, pub, msg, in.Signature) {
		return Assertion{}, ErrSignature
	}
	if (cred.SignCount != 0 || a.SignCount != 0) && a.SignCount <= cred.SignCount {
		return Assertion{}, ErrCounter
	}
	return Assertion{SignCount: a.SignCount, BackedUp: a.has(flagBS)}, nil
}
