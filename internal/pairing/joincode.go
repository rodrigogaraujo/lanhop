package pairing

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
)

const (
	keyPrefixBytes = 10
	tokenBytes     = 5
	joinCodeGroup  = 4
	joinCodeLength = (keyPrefixBytes + tokenBytes) * 8 / 5
)

var ErrNotAJoinCode = errors.New("pairing: not a join code")

var joinCodeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type JoinCode struct {
	keyPrefix [keyPrefixBytes]byte
	token     [tokenBytes]byte
}

func NewJoinCode(central identity.Fingerprint) (JoinCode, error) {
	var code JoinCode
	copy(code.keyPrefix[:], central[:keyPrefixBytes])
	if _, err := rand.Read(code.token[:]); err != nil {
		return JoinCode{}, err
	}
	return code, nil
}

func ParseJoinCode(text string) (JoinCode, error) {
	compact := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(text), "-", ""))
	decoded, err := joinCodeEncoding.DecodeString(compact)
	if err != nil || len(compact) != joinCodeLength || len(decoded) != keyPrefixBytes+tokenBytes {
		return JoinCode{}, fmt.Errorf("%w: %q", ErrNotAJoinCode, text)
	}
	var code JoinCode
	copy(code.keyPrefix[:], decoded[:keyPrefixBytes])
	copy(code.token[:], decoded[keyPrefixBytes:])
	return code, nil
}

func (c JoinCode) String() string {
	encoded := joinCodeEncoding.EncodeToString(slices.Concat(c.keyPrefix[:], c.token[:]))
	groups := make([]string, 0, len(encoded)/joinCodeGroup)
	for start := 0; start < len(encoded); start += joinCodeGroup {
		groups = append(groups, encoded[start:start+joinCodeGroup])
	}
	return strings.Join(groups, "-")
}

func (c JoinCode) names(fingerprint identity.Fingerprint) bool {
	return bytes.Equal(c.keyPrefix[:], fingerprint[:keyPrefixBytes])
}
