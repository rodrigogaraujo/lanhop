package update

import (
	"crypto/ed25519"
	"slices"
)

var projectKeys []ed25519.PublicKey

func ProjectKeys() []ed25519.PublicKey {
	return slices.Clone(projectKeys)
}
