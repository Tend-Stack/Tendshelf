package passkey

import "encoding/binary"

// Authenticator data flags (WebAuthn L2 section 6.1).
const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
	flagED = 0x80
)

// authData is the parsed authenticator data. AAGUID, CredentialID and COSEKey are set only when
// the AT flag is.
type authData struct {
	RPIDHash     [32]byte
	Flags        byte
	SignCount    uint32
	AAGUID       []byte
	CredentialID []byte
	COSEKey      []byte // the key's CBOR bytes exactly as sent
}

func (a authData) has(flag byte) bool { return a.Flags&flag != 0 }

// parseAuthData splits authenticator data. Extensions are read as one CBOR map and ignored; any
// byte left over is ErrMalformed.
func parseAuthData(b []byte) (authData, error) {
	var a authData
	if len(b) < 37 {
		return a, ErrMalformed
	}
	copy(a.RPIDHash[:], b[:32])
	a.Flags = b[32]
	a.SignCount = binary.BigEndian.Uint32(b[33:37])
	b = b[37:]
	if a.has(flagAT) {
		if len(b) < 18 {
			return a, ErrMalformed
		}
		a.AAGUID = append([]byte(nil), b[:16]...)
		idLen := int(binary.BigEndian.Uint16(b[16:18]))
		b = b[18:]
		if idLen == 0 || idLen > 1023 || len(b) < idLen {
			return a, ErrMalformed
		}
		a.CredentialID = append([]byte(nil), b[:idLen]...)
		b = b[idLen:]
		_, rest, err := decodeCBOR(b, 0)
		if err != nil {
			return a, ErrMalformed
		}
		a.COSEKey = append([]byte(nil), b[:len(b)-len(rest)]...)
		b = rest
	}
	if a.has(flagED) {
		v, rest, err := decodeCBOR(b, 0)
		if _, isMap := v.(cborMap); err != nil || !isMap {
			return a, ErrMalformed
		}
		b = rest
	}
	if len(b) != 0 {
		return a, ErrMalformed
	}
	return a, nil
}
