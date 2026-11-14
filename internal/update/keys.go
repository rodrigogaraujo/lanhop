package update

import (
	"crypto/ed25519"
	"slices"
)

var (
	projectKeys              []ed25519.PublicKey
	projectCertificateLeaves []string
)

func ProjectKeys() []ed25519.PublicKey {
	return slices.Clone(projectKeys)
}

func ProjectAppCheck() func(app string) error {
	if len(projectCertificateLeaves) == 0 {
		return nil
	}
	check, err := AppCheck(AppIdentifier, projectCertificateLeaves)
	if err != nil {
		return func(string) error { return err }
	}
	return check
}
