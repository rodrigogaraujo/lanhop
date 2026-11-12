package identity

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	codeBytes     = 10
	codeGroupSize = 4
)

type Fingerprint [sha256.Size]byte

func FingerprintOf(certificate *x509.Certificate) Fingerprint {
	return sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
}

func ParseFingerprint(text string) (Fingerprint, error) {
	var fingerprint Fingerprint
	decoded, err := hex.DecodeString(text)
	if err != nil || len(decoded) != len(fingerprint) {
		return Fingerprint{}, fmt.Errorf("identity: %q is not a fingerprint of %d hex digits", text, 2*len(fingerprint))
	}
	copy(fingerprint[:], decoded)
	return fingerprint, nil
}

func (f Fingerprint) String() string {
	return hex.EncodeToString(f[:])
}

func (f Fingerprint) Code() string {
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(f[:codeBytes])
	groups := make([]string, 0, len(encoded)/codeGroupSize)
	for start := 0; start < len(encoded); start += codeGroupSize {
		groups = append(groups, encoded[start:start+codeGroupSize])
	}
	return strings.Join(groups, "-")
}

func (f Fingerprint) MarshalText() ([]byte, error) {
	return []byte(f.String()), nil
}

func (f *Fingerprint) UnmarshalText(text []byte) error {
	parsed, err := ParseFingerprint(string(text))
	if err != nil {
		return err
	}
	*f = parsed
	return nil
}
