package update

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const AppIdentifier = "com.github.rodrigogaraujo.lanhop"

var (
	ErrAppNotOurs = errors.New("update: the new app is not signed with a certificate of the project")

	certificateLeafForm = regexp.MustCompile(`^[0-9a-f]{40}$`)
	appIdentifierForm   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
)

func AppCheck(identifier string, leaves []string) (func(app string) error, error) {
	requirement, err := appRequirement(identifier, leaves)
	if err != nil {
		return nil, err
	}
	return func(app string) error { return checkApp(app, requirement) }, nil
}

func appRequirement(identifier string, leaves []string) (string, error) {
	if !appIdentifierForm.MatchString(identifier) {
		return "", fmt.Errorf("update: %q is not an app identifier", identifier)
	}
	if len(leaves) == 0 {
		return "", errors.New("update: no certificate hash to check an app against")
	}
	certificates := make([]string, len(leaves))
	for i, leaf := range leaves {
		if !certificateLeafForm.MatchString(leaf) {
			return "", fmt.Errorf("update: %q is not the SHA-1 of a certificate in lowercase hexadecimal", leaf)
		}
		certificates[i] = fmt.Sprintf(`certificate leaf = H"%s"`, leaf)
	}
	return fmt.Sprintf(`identifier "%s" and (%s)`, identifier, strings.Join(certificates, " or ")), nil
}
