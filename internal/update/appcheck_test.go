package update

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

const (
	currentLeaf = "d128a4df96312c2b76715bfb36725773783089e9"
	nextLeaf    = "0123456789abcdef0123456789abcdef01234567"
)

func TestTheRequirementNamesTheIdentifierAndEachCertificate(t *testing.T) {
	cases := []struct {
		leaves []string
		want   string
	}{
		{[]string{currentLeaf}, `identifier "com.github.rodrigogaraujo.lanhop" and (certificate leaf = H"` + currentLeaf + `")`},
		{[]string{currentLeaf, nextLeaf}, `identifier "com.github.rodrigogaraujo.lanhop" and (certificate leaf = H"` + currentLeaf + `" or certificate leaf = H"` + nextLeaf + `")`},
	}
	for _, c := range cases {
		got, err := appRequirement(AppIdentifier, c.leaves)
		if err != nil || got != c.want {
			t.Errorf("appRequirement(%v) = %q, %v, want %q", c.leaves, got, err, c.want)
		}
	}
}

func TestARequirementIsNotBuiltFromWhatCouldChangeItsMeaning(t *testing.T) {
	cases := []struct {
		identifier string
		leaves     []string
	}{
		{AppIdentifier, nil},
		{AppIdentifier, []string{strings.ToUpper(currentLeaf)}},
		{AppIdentifier, []string{currentLeaf[:39]}},
		{AppIdentifier, []string{currentLeaf + `") or anchor apple or (certificate leaf = H"` + currentLeaf}},
		{AppIdentifier, []string{currentLeaf, ""}},
		{"", []string{currentLeaf}},
		{`com.example" or identifier "x`, []string{currentLeaf}},
		{"com.example lanhop", []string{currentLeaf}},
	}
	for _, c := range cases {
		if got, err := appRequirement(c.identifier, c.leaves); err == nil {
			t.Errorf("appRequirement(%q, %q) = %q, want an error", c.identifier, c.leaves, got)
		}
		if check, err := AppCheck(c.identifier, c.leaves); err == nil || check != nil {
			t.Errorf("AppCheck(%q, %q) gave a check, want an error", c.identifier, c.leaves)
		}
	}
}

func TestWithoutACompiledCertificateHashNoAppIsTaken(t *testing.T) {
	leaves := projectCertificateLeaves
	t.Cleanup(func() { projectCertificateLeaves = leaves })
	projectCertificateLeaves = nil
	if ProjectAppCheck() != nil {
		t.Fatal("ProjectAppCheck gave a check without a certificate hash, want none, so that Prepare refuses every app")
	}
	projectCertificateLeaves = []string{"not a hash"}
	if err := ProjectAppCheck()("lanhop.app"); err == nil {
		t.Fatal("the check from a malformed compiled hash took an app")
	}
}

func TestTheCompiledCertificateHashIsTheOneTheReleaseChecks(t *testing.T) {
	kept, err := os.ReadFile("../../packaging/macos/certificate-leaf")
	switch {
	case errors.Is(err, os.ErrNotExist):
		if len(projectCertificateLeaves) > 0 {
			t.Fatalf("lanhop carries the certificate hashes %q, and packaging/macos/certificate-leaf is missing", projectCertificateLeaves)
		}
		return
	case err != nil:
		t.Fatal(err)
	}
	leaf := strings.TrimSpace(string(kept))
	if len(projectCertificateLeaves) == 0 || projectCertificateLeaves[0] != leaf {
		t.Fatalf("packaging/macos/certificate-leaf holds %q, and the first certificate hash in lanhop is not it: %q", leaf, projectCertificateLeaves)
	}
	if slices.Contains(projectCertificateLeaves[1:], leaf) {
		t.Fatalf("the next certificate hash is the current one: %q", projectCertificateLeaves)
	}
	if _, err := appRequirement(AppIdentifier, projectCertificateLeaves); err != nil {
		t.Fatal(err)
	}
}
